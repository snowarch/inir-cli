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

// Reactions are the ones the shell offers first (its Hub.quickReactions); people react with any emoji
// (validReaction), up to maxReactions on one item.
var Reactions = []string{"❤️", "🔥", "✨", "😂", "🤯", "👏"}

const maxReactions = 12

// validReaction says whether r is one emoji, any of them: a pictograph with its skin tone, variation selector and
// joined parts, a flag, a keycap or a tag sequence. Never text: a state can't carry words.
func validReaction(r string) bool {
	runes := []rune(r)
	if len(runes) == 0 || len(runes) > 16 {
		return false
	}
	bases, flags := 0, 0
	for i, c := range runes {
		joined := i > 0 && runes[i-1] == 0x200D
		switch {
		case c >= 0x1F1E6 && c <= 0x1F1FF: // regional indicators: two make one flag
			if flags%2 == 0 && !joined {
				bases++
			}
			flags++
		case c >= 0x1F3FB && c <= 0x1F3FF, c == 0x200D, c == 0xFE0F, c == 0xFE0E, c == 0x20E3, c >= 0xE0020 && c <= 0xE007F:
			// skin tone, joiner, variation selectors, keycap, tags: parts of the emoji before them
			if i == 0 {
				return false
			}
		case c >= 0x1F000 && c <= 0x1FAFF, c >= 0x2600 && c <= 0x27BF, c >= 0x2300 && c <= 0x23FF,
			c >= 0x2B00 && c <= 0x2BFF, c >= 0x2190 && c <= 0x21FF, c >= 0x25A0 && c <= 0x25FF, c >= 0x2900 && c <= 0x297F,
			c == 0x00A9, c == 0x00AE, c == 0x203C, c == 0x2049, c == 0x2122, c == 0x2139, c == 0x24C2, c == 0x3030,
			c == 0x303D, c == 0x3297, c == 0x3299:
			if !joined {
				bases++
			}
		case (c >= '0' && c <= '9') || c == '#' || c == '*': // a keycap's base, only as one
			if i != 0 || !slices.Contains(runes, 0x20E3) {
				return false
			}
			bases++
		default:
			return false
		}
	}
	return bases == 1
}

// reactionsOf keeps a state's reactions that count: valid, each once, the first maxReactions.
func reactionsOf(in []string) []string {
	out := []string{}
	for _, r := range in {
		if validReaction(r) && !slices.Contains(out, r) && len(out) < maxReactions {
			out = append(out, r)
		}
	}
	return out
}

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
			a.Reactions = reactionsOf(a.Reactions)
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

// fold counts each person's newest state into the items' stats: the items already in stats, or every item when
// open (a live count, which has no catalogue to hold it to).
func fold(events map[string]*Event, stats map[string]Stats, open bool) {
	for _, ev := range events {
		var state socialFile
		if json.Unmarshal([]byte(ev.Content), &state) != nil || state.V != 1 {
			continue
		}
		for id, a := range state.Items {
			s, ok := stats[id]
			if !ok && (!open || id == "" || len(id) > 64) {
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
			for _, r := range reactionsOf(a.Reactions) {
				if s.Reactions == nil {
					s.Reactions = map[string]int{}
				}
				s.Reactions[r]++
			}
			stats[id] = s
		}
	}
}

// Counts is what everyone else did with each item, read from the relays now: the shell adds the person's own
// act to it (hub.social), so a change of theirs shows at once and is never counted twice.
type Counts struct {
	At     int64            `json:"at"`
	Relays int              `json:"relays"`
	Items  map[string]Stats `json:"items"`
	Stale  bool             `json:"stale,omitempty"`
}

// Counts reads every relay, unless the last read is younger than maxAge; with no relay answering it gives the last
// read, marked stale, or fails.
func (e *Env) Counts(ctx context.Context, maxAge time.Duration) (*Counts, error) {
	cache := filepath.Join(e.State, "social-counts.json")
	var last Counts
	b, err := os.ReadFile(cache)
	haveLast := err == nil && json.Unmarshal(b, &last) == nil && last.Items != nil
	if haveLast && time.Since(time.Unix(last.At, 0)) < maxAge {
		return &last, nil
	}
	events, answered := fetch(ctx, Relays)
	if answered == 0 {
		if haveLast {
			last.Stale = true
			return &last, nil
		}
		return nil, fail(ExitNetwork, "no relay answered")
	}
	// the person's own state is theirs to add, from their config: a key never made has published nothing
	if raw, err := os.ReadFile(filepath.Join(e.State, "key")); err == nil {
		if k, err := hex.DecodeString(string(bytes.TrimSpace(raw))); err == nil && len(k) == 32 {
			priv, _ := btcec.PrivKeyFromBytes(k)
			delete(events, hex.EncodeToString(schnorr.SerializePubKey(priv.PubKey())))
		}
	}
	now := &Counts{At: time.Now().Unix(), Relays: answered, Items: map[string]Stats{}}
	fold(events, now.Items, true)
	if os.MkdirAll(e.State, 0o755) == nil {
		_ = writeAtomic(cache, append(marshal(now), '\n'))
	}
	return now, nil
}
