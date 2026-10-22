package notify

import (
	"bytes"
	"testing"
	"time"
)

func TestBusSocket(t *testing.T) {
	for addr, want := range map[string]string{
		"unix:path=/run/user/1000/bus":           "/run/user/1000/bus",
		"unix:abstract=/tmp/dbus-x,guid=abc":     "@/tmp/dbus-x",
		"tcp:host=x;unix:path=/tmp/a%20b,guid=1": "/tmp/a b",
	} {
		if got, err := busSocket(addr); err != nil || got != want {
			t.Errorf("%s: got %q %v, want %q", addr, got, err, want)
		}
	}
	if _, err := busSocket("tcp:host=localhost"); err == nil {
		t.Error("tcp only should fail")
	}
}

func TestMessageRoundTrip(t *testing.T) {
	var e enc
	e.str("hi")
	e.u32(7)
	raw := encode(typeCall, 3, 0, dest, path, iface, "Notify", "su", e.b)
	m, err := readMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	d := m.dec()
	if m.member != "Notify" || m.iface != iface || m.sig != "su" || m.serial != 3 || d.str() != "hi" || d.u32() != 7 {
		t.Fatalf("got %+v", m)
	}
	if _, err := readMessage(bytes.NewReader(raw[:len(raw)-1])); err == nil {
		t.Error("a cut message should fail")
	}
}

// Through a real dbus-daemon, which rejects a malformed message.
func TestNotifyThroughBus(t *testing.T) {
	f, err := StartFake()
	if err != nil {
		t.Skip("no dbus-daemon:", err)
	}
	defer f.Stop()
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", f.Address)
	c, err := Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	id, err := c.Notify(Note{App: "inir", Summary: "make", Body: "42% · 0:13", Actions: []string{"cancel", "Stop"},
		Hints: map[string]any{"value": int32(42), "urgency": Low, "transient": true, "category": "x"}, Timeout: 0})
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.Notify(Note{App: "inir", Summary: "make", Replaces: id, Timeout: -1})
	if err != nil || again != id {
		t.Fatalf("replace: %d %v", again, err)
	}
	n := f.Notes()
	if len(n) != 2 || n[0].Hints["value"] != int32(42) || n[0].Hints["urgency"] != Low || n[0].Hints["transient"] != true ||
		n[0].Hints["category"] != "x" || len(n[0].Actions) != 2 || n[0].Body != "42% · 0:13" || n[1].Timeout != -1 {
		t.Fatalf("server saw %+v", n)
	}
	f.Invoke(id, "cancel")
	select {
	case ev := <-c.Events:
		if ev.ID != id || ev.Action != "cancel" {
			t.Fatalf("event %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no ActionInvoked")
	}
}
