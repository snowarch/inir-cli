package hub

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/coder/websocket"
)

// What people do in the hub lives with them, not on a server of ours: their votes and reactions are `hub.social`
// in their config.json, written by the shell's Hub page. `inir hub social` publishes that state with what they have
// installed as one Nostr event (NIP-78, kind 30078, d = "inir-hub") signed by a key kept in the hub's state folder,
// never in config.json. Relays keep only the newest event per key, so changing a vote replaces it. The hub's
// stats workflow reads every relay, keeps the newest event per key and counts them into stats.json.

// Relays carry the events; a publish goes to all of them and a count reads all of them. INIR_HUB_RELAYS (spaces or
// commas) points both somewhere else, as a nest or a test does.
var Relays = relaysFromEnv([]string{"wss://nos.lol", "wss://relay.primal.net", "wss://nostr.mom", "wss://relay.damus.io"})

func relaysFromEnv(fallback []string) []string {
	if env := strings.Fields(strings.ReplaceAll(os.Getenv("INIR_HUB_RELAYS"), ",", " ")); len(env) > 0 {
		return env
	}
	return fallback
}

// Reactions is the fixed set people react with; anything else is not counted. The shell's Hub.reactions matches it.
var Reactions = []string{"❤️", "🔥", "✨", "😂", "🤯", "👏"}

const (
	socialKind = 30078
	socialTag  = "inir-hub"
	socialPoW  = 16 // leading zero bits of the event id: a little work per change, more for a crowd of fake keys
	republish  = 7 * 24 * time.Hour
)

// Act is what one person did with one item.
type Act struct {
	Installed bool     `json:"installed,omitempty"`
	Vote      string   `json:"vote,omitempty"` // "up" or "down"
	Reactions []string `json:"reactions,omitempty"`
}

type socialFile struct {
	V     int            `json:"v"`
	Items map[string]Act `json:"items"`
}

// Event is a Nostr event (NIP-01).
type Event struct {
	ID        string     `json:"id"`
	PubKey    string     `json:"pubkey"`
	CreatedAt int64      `json:"created_at"`
	Kind      int        `json:"kind"`
	Tags      [][]string `json:"tags"`
	Content   string     `json:"content"`
	Sig       string     `json:"sig"`
}

func marshal(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}

func (ev *Event) hash() [32]byte {
	return sha256.Sum256(marshal([]any{0, ev.PubKey, ev.CreatedAt, ev.Kind, ev.Tags, ev.Content}))
}

func zeroBits(id [32]byte) int {
	n := 0
	for _, b := range id {
		if b != 0 {
			return n + bits.LeadingZeros8(b)
		}
		n += 8
	}
	return n
}

// verify says whether an event is a well-formed, signed inir-hub state with its proof of work.
func (ev *Event) verify() bool {
	if ev.Kind != socialKind || len(ev.Content) > 64<<10 || !slices.ContainsFunc(ev.Tags, func(t []string) bool {
		return len(t) >= 2 && t[0] == "d" && t[1] == socialTag
	}) {
		return false
	}
	id := ev.hash()
	if hex.EncodeToString(id[:]) != ev.ID || zeroBits(id) < socialPoW {
		return false
	}
	pk, err1 := hex.DecodeString(ev.PubKey)
	sig, err2 := hex.DecodeString(ev.Sig)
	if err1 != nil || err2 != nil {
		return false
	}
	key, err1 := schnorr.ParsePubKey(pk)
	s, err2 := schnorr.ParseSignature(sig)
	return err1 == nil && err2 == nil && s.Verify(id[:], key)
}

func sign(key *btcec.PrivateKey, content string, at time.Time) (*Event, error) {
	ev := &Event{PubKey: hex.EncodeToString(schnorr.SerializePubKey(key.PubKey())), CreatedAt: at.Unix(), Kind: socialKind, Content: content}
	for nonce := 0; ; nonce++ {
		ev.Tags = [][]string{{"d", socialTag}, {"nonce", strconv.Itoa(nonce), strconv.Itoa(socialPoW)}}
		if id := ev.hash(); zeroBits(id) >= socialPoW {
			sig, err := schnorr.Sign(key, id[:])
			if err != nil {
				return nil, err
			}
			ev.ID, ev.Sig = hex.EncodeToString(id[:]), hex.EncodeToString(sig.Serialize())
			return ev, nil
		}
	}
}

// key is this person's signing key, made on first use. Lost, it is a new person and nothing more.
func (e *Env) key() (*btcec.PrivateKey, error) {
	p := filepath.Join(e.State, "key")
	if b, err := os.ReadFile(p); err == nil {
		if raw, err := hex.DecodeString(string(bytes.TrimSpace(b))); err == nil && len(raw) == 32 {
			k, _ := btcec.PrivKeyFromBytes(raw)
			return k, nil
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(e.State, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, []byte(hex.EncodeToString(raw)+"\n"), 0o600); err != nil {
		return nil, err
	}
	k, _ := btcec.PrivKeyFromBytes(raw)
	return k, nil
}

// mine reads hub.social from config.json: whether to share, and each item's vote and reactions.
func (e *Env) mine() (bool, map[string]Act) {
	var config struct {
		Hub struct {
			Social struct {
				Share *bool          `json:"share"`
				Items map[string]Act `json:"items"`
			} `json:"social"`
		} `json:"hub"`
	}
	b, _ := os.ReadFile(filepath.Join(e.userConfig(), "config.json"))
	_ = json.Unmarshal(b, &config)
	s := config.Hub.Social
	return s.Share == nil || *s.Share, s.Items
}

// Social publishes this person's hub state when it changed or a week went by. With sharing off it publishes an
// empty state once, which takes them out of every count.
func (e *Env) Social(ctx context.Context) (string, error) {
	share, acts := e.mine()
	state := socialFile{V: 1, Items: map[string]Act{}}
	if share {
		for id := range e.records() {
			state.Items[id] = Act{Installed: true}
		}
		for id, a := range acts {
			a.Installed = state.Items[id].Installed
			if a.Vote != "up" && a.Vote != "down" {
				a.Vote = ""
			}
			a.Reactions = slices.DeleteFunc(slices.Clone(a.Reactions), func(r string) bool { return !slices.Contains(Reactions, r) })
			if a.Installed || a.Vote != "" || len(a.Reactions) > 0 {
				state.Items[id] = a
			}
		}
	}
	content := string(marshal(state))
	sum := sha256.Sum256([]byte(content))
	last := filepath.Join(e.State, "social.json")
	var was struct {
		Sum string `json:"sum"`
		At  int64  `json:"at"`
	}
	if b, err := os.ReadFile(last); err == nil {
		_ = json.Unmarshal(b, &was)
	} else if !share {
		return "sharing is off and nothing was published", nil
	}
	if was.Sum == hex.EncodeToString(sum[:]) && time.Since(time.Unix(was.At, 0)) < republish {
		return "already published", nil
	}
	key, err := e.key()
	if err != nil {
		return "", err
	}
	ev, err := sign(key, content, time.Now())
	if err != nil {
		return "", err
	}
	ok := publish(ctx, ev, Relays)
	if ok == 0 {
		return "", fail(ExitNetwork, "no relay took it; it goes out next time")
	}
	was.Sum, was.At = hex.EncodeToString(sum[:]), ev.CreatedAt
	if err := writeAtomic(last, append(marshal(was), '\n')); err != nil {
		return "", err
	}
	return fmt.Sprintf("published to %d of %d relays", ok, len(Relays)), nil
}

func publish(ctx context.Context, ev *Event, relays []string) int {
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for _, url := range relays {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			c, _, err := websocket.Dial(ctx, url, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			c.SetReadLimit(1 << 20)
			if c.Write(ctx, websocket.MessageText, marshal([]any{"EVENT", ev})) != nil {
				return
			}
			for {
				_, b, err := c.Read(ctx)
				if err != nil {
					return
				}
				var msg []json.RawMessage
				var kind, id string
				var accepted bool
				if json.Unmarshal(b, &msg) != nil || len(msg) < 3 || json.Unmarshal(msg[0], &kind) != nil || kind != "OK" {
					continue
				}
				if json.Unmarshal(msg[1], &id) == nil && id == ev.ID && json.Unmarshal(msg[2], &accepted) == nil && accepted {
					mu.Lock()
					ok++
					mu.Unlock()
				}
				return
			}
		})
	}
	wg.Wait()
	return ok
}

// fetch reads every inir-hub state from the relays and keeps the newest valid one per key.
func fetch(ctx context.Context, relays []string) (map[string]*Event, int) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	newest, answered := map[string]*Event{}, 0
	filter := map[string]any{"kinds": []int{socialKind}, "#d": []string{socialTag}}
	for _, url := range relays {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			c, _, err := websocket.Dial(ctx, url, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			c.SetReadLimit(4 << 20)
			if c.Write(ctx, websocket.MessageText, marshal([]any{"REQ", "count", filter})) != nil {
				return
			}
			for {
				_, b, err := c.Read(ctx)
				if err != nil {
					return
				}
				var msg []json.RawMessage
				var kind string
				if json.Unmarshal(b, &msg) != nil || len(msg) < 2 || json.Unmarshal(msg[0], &kind) != nil {
					continue
				}
				if kind == "EOSE" {
					mu.Lock()
					answered++
					mu.Unlock()
					return
				}
				var ev Event
				if kind != "EVENT" || len(msg) < 3 || json.Unmarshal(msg[2], &ev) != nil || !ev.verify() {
					continue
				}
				mu.Lock()
				if old := newest[ev.PubKey]; old == nil || ev.CreatedAt > old.CreatedAt {
					newest[ev.PubKey] = &ev
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return newest, answered
}

// fold counts each person's newest state into the items' stats.
func fold(events map[string]*Event, stats map[string]Stats) {
	for _, ev := range events {
		var state socialFile
		if json.Unmarshal([]byte(ev.Content), &state) != nil || state.V != 1 {
			continue
		}
		for id, a := range state.Items {
			s, ok := stats[id]
			if !ok {
				continue
			}
			if a.Installed {
				s.Installs++
			}
			switch a.Vote {
			case "up":
				s.Up++
			case "down":
				s.Down++
			}
			for _, r := range slicesUnique(a.Reactions) {
				if slices.Contains(Reactions, r) {
					if s.Reactions == nil {
						s.Reactions = map[string]int{}
					}
					s.Reactions[r]++
				}
			}
			stats[id] = s
		}
	}
}

func slicesUnique(in []string) []string {
	out := slices.Clone(in)
	sort.Strings(out)
	return slices.Compact(out)
}
