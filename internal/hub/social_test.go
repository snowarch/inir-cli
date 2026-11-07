package hub

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/coder/websocket"
)

// A signed state verifies and counts once per person; a changed byte or a foreign emoji does not count.
func TestSocialSignFold(t *testing.T) {
	raw, _ := hex.DecodeString("0101010101010101010101010101010101010101010101010101010101010101")
	key, _ := btcec.PrivKeyFromBytes(raw)
	old, err := sign(key, `{"v":1,"items":{"clock":{"vote":"down"}}}`, time.Unix(100, 0))
	if err != nil || !old.verify() {
		t.Fatalf("signed event does not verify: %v", err)
	}
	now, _ := sign(key, `{"v":1,"items":{"clock":{"installed":true,"vote":"up","reactions":["🔥","🔥","🍕"]}}}`, time.Unix(200, 0))
	bad := *now
	bad.Content = `{"v":1,"items":{"clock":{"vote":"down"}}}`
	if bad.verify() {
		t.Error("a changed event verified")
	}
	stats := map[string]Stats{"clock": {}}
	fold(map[string]*Event{now.PubKey: now}, stats)
	got := stats["clock"]
	if got.Installs != 1 || got.Up != 1 || got.Down != 0 || got.Reactions["🔥"] != 1 || len(got.Reactions) != 1 {
		t.Errorf("fold: %+v", got)
	}
}

// A relay that keeps the newest event per key, as NIP-78 relays do: publish reaches it, fetch reads it back.
func TestSocialRelayRoundTrip(t *testing.T) {
	var mu sync.Mutex
	kept := map[string]*Event{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			_, b, err := c.Read(r.Context())
			if err != nil {
				return
			}
			var msg []json.RawMessage
			var kind string
			json.Unmarshal(b, &msg)
			json.Unmarshal(msg[0], &kind)
			switch kind {
			case "EVENT":
				var ev Event
				json.Unmarshal(msg[1], &ev)
				mu.Lock()
				if old := kept[ev.PubKey]; old == nil || ev.CreatedAt > old.CreatedAt {
					kept[ev.PubKey] = &ev
				}
				mu.Unlock()
				c.Write(r.Context(), websocket.MessageText, marshal([]any{"OK", ev.ID, ev.verify(), ""}))
			case "REQ":
				mu.Lock()
				for _, ev := range kept {
					c.Write(r.Context(), websocket.MessageText, marshal([]any{"EVENT", "count", ev}))
				}
				mu.Unlock()
				c.Write(r.Context(), websocket.MessageText, marshal([]any{"EOSE", "count"}))
			}
		}
	}))
	defer srv.Close()
	relay := "ws" + strings.TrimPrefix(srv.URL, "http")
	key, _ := btcec.NewPrivateKey()
	ctx := context.Background()
	for i, content := range []string{`{"v":1,"items":{"clock":{"vote":"down"}}}`, `{"v":1,"items":{"clock":{"vote":"up"}}}`} {
		ev, _ := sign(key, content, time.Unix(int64(100+i), 0))
		if publish(ctx, ev, []string{relay, "ws://127.0.0.1:1"}) != 1 {
			t.Fatal("the relay did not take the event")
		}
	}
	events, answered := fetch(ctx, []string{relay, "ws://127.0.0.1:1"})
	stats := map[string]Stats{"clock": {}}
	fold(events, stats)
	if answered != 1 || stats["clock"].Up != 1 || stats["clock"].Down != 0 {
		t.Errorf("answered %d, stats %+v", answered, stats["clock"])
	}
}
