// Package notify talks to the desktop's notification server
// (org.freedesktop.Notifications) on the session bus. It speaks just enough
// D-Bus for that, so the binary needs neither a D-Bus library nor notify-send.
package notify

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	dest  = "org.freedesktop.Notifications"
	path  = "/org/freedesktop/Notifications"
	iface = "org.freedesktop.Notifications"
)

// Urgency levels of the urgency hint.
const (
	Low      byte = 0
	Normal   byte = 1
	Critical byte = 2
)

// Note is one notification. Hints take string, bool, byte, int32 or uint32
// values; Timeout is in ms, -1 for the server's default and 0 for never.
type Note struct {
	App, Icon, Summary, Body string
	Replaces                 uint32
	Actions                  []string // key, label, key, label...
	Hints                    map[string]any
	Timeout                  int32
}

// Event is a signal the server sent about a notification.
type Event struct {
	ID     uint32
	Action string // ActionInvoked
	Closed bool   // NotificationClosed
	Reason uint32 // 1 expired, 2 dismissed, 3 closed by a call
}

// Conn is a connection to the session bus.
type Conn struct {
	c      net.Conn
	wmu    sync.Mutex
	serial uint32
	mu     sync.Mutex
	calls  map[uint32]chan *message
	onCall func(*message) // method calls to us; only the fake server takes them
	Events chan Event
}

var errClosed = errors.New("session bus connection closed")

// Dial connects to the session bus and subscribes to the server's signals.
func Dial() (*Conn, error) {
	addr, err := busSocket(os.Getenv("DBUS_SESSION_BUS_ADDRESS"))
	if err != nil {
		return nil, err
	}
	nc, err := net.DialTimeout("unix", addr, time.Second)
	if err != nil {
		return nil, err
	}
	nc.SetDeadline(time.Now().Add(3 * time.Second))
	if err := auth(nc); err != nil {
		nc.Close()
		return nil, err
	}
	nc.SetDeadline(time.Time{})
	c := &Conn{c: nc, calls: map[uint32]chan *message{}, Events: make(chan Event, 16)}
	go c.read()
	if _, err := c.bus("Hello", "", nil); err != nil {
		c.Close()
		return nil, err
	}
	var e enc
	e.str("type='signal',interface='" + iface + "'")
	if _, err := c.bus("AddMatch", "s", e.b); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Notify shows a notification, or replaces n.Replaces, and returns its id.
func (c *Conn) Notify(n Note) (uint32, error) {
	var e enc
	e.str(n.App)
	e.u32(n.Replaces)
	e.str(n.Icon)
	e.str(n.Summary)
	e.str(n.Body)
	e.array(4, func() {
		for _, a := range n.Actions {
			e.str(a)
		}
	})
	keys := make([]string, 0, len(n.Hints))
	for k := range n.Hints {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	e.array(8, func() {
		for _, k := range keys {
			e.align(8)
			e.str(k)
			e.variant(n.Hints[k])
		}
	})
	e.u32(uint32(n.Timeout))
	m, err := c.call(dest, path, iface, "Notify", "susssasa{sv}i", e.b)
	if err != nil {
		return 0, err
	}
	return m.dec().u32(), nil
}

// Dismiss closes a notification.
func (c *Conn) Dismiss(id uint32) error {
	var e enc
	e.u32(id)
	_, err := c.call(dest, path, iface, "CloseNotification", "u", e.b)
	return err
}

func (c *Conn) Close() error { return c.c.Close() }

func (c *Conn) bus(member, sig string, body []byte) (*message, error) {
	return c.call("org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", member, sig, body)
}

// busSocket is the unix socket of a bus address ("unix:path=/run/user/1000/bus,guid=...").
func busSocket(addr string) (string, error) {
	if addr == "" {
		run := os.Getenv("XDG_RUNTIME_DIR")
		if run == "" {
			run = fmt.Sprintf("/run/user/%d", os.Getuid())
		}
		return filepath.Join(run, "bus"), nil
	}
	for _, a := range strings.Split(addr, ";") {
		kind, kv, ok := strings.Cut(a, ":")
		if !ok || kind != "unix" {
			continue
		}
		for _, p := range strings.Split(kv, ",") {
			k, v, _ := strings.Cut(p, "=")
			v, _ = url.PathUnescape(v)
			switch k {
			case "path":
				return v, nil
			case "abstract":
				return "@" + v, nil
			}
		}
	}
	return "", fmt.Errorf("no unix socket in bus address %q", addr)
}

func auth(c net.Conn) error {
	uid := hex.EncodeToString([]byte(strconv.Itoa(os.Getuid())))
	if _, err := io.WriteString(c, "\x00AUTH EXTERNAL "+uid+"\r\n"); err != nil {
		return err
	}
	// Byte by byte: whatever follows the line already belongs to the messages.
	var line []byte
	one := make([]byte, 1)
	for len(line) < 512 {
		if _, err := io.ReadFull(c, one); err != nil {
			return err
		}
		if one[0] == '\n' {
			break
		}
		line = append(line, one[0])
	}
	if !strings.HasPrefix(string(line), "OK ") {
		return fmt.Errorf("session bus refused us: %q", strings.TrimSpace(string(line)))
	}
	_, err := io.WriteString(c, "BEGIN\r\n")
	return err
}

const (
	typeCall   = 1
	typeReturn = 2
	typeError  = 3
	typeSignal = 4
)

type message struct {
	typ                                 byte
	serial, replyTo                     uint32
	iface, member, errName, sig, sender string
	body                                []byte
	order                               binary.ByteOrder
}

func (m *message) dec() *dec { return &dec{b: m.body, o: m.order} }

func (c *Conn) call(dst, obj, ifc, member, sig string, body []byte) (*message, error) {
	ch := make(chan *message, 1)
	c.wmu.Lock()
	c.serial++
	s := c.serial
	c.mu.Lock()
	if c.calls == nil {
		c.mu.Unlock()
		c.wmu.Unlock()
		return nil, errClosed
	}
	c.calls[s] = ch
	c.mu.Unlock()
	_, err := c.c.Write(encode(typeCall, s, 0, dst, obj, ifc, member, sig, body))
	c.wmu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return nil, errClosed
		}
		if m.typ == typeError {
			text := m.errName
			if strings.HasPrefix(m.sig, "s") {
				text += ": " + m.dec().str()
			}
			return nil, errors.New(text)
		}
		return m, nil
	case <-time.After(5 * time.Second):
		c.mu.Lock()
		delete(c.calls, s)
		c.mu.Unlock()
		return nil, fmt.Errorf("%s: no answer on the session bus", member)
	}
}

// send writes a message that expects no answer (replies and signals).
func (c *Conn) send(typ byte, replyTo uint32, dst, obj, ifc, member, sig string, body []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.serial++
	_, err := c.c.Write(encode(typ, c.serial, replyTo, dst, obj, ifc, member, sig, body))
	return err
}

func (c *Conn) read() {
	r := bufio.NewReader(c.c)
	defer func() {
		c.mu.Lock()
		for _, ch := range c.calls {
			close(ch)
		}
		c.calls = nil
		c.mu.Unlock()
		close(c.Events)
	}()
	for {
		m, err := readMessage(r)
		if err != nil {
			return
		}
		switch m.typ {
		case typeReturn, typeError:
			c.mu.Lock()
			ch := c.calls[m.replyTo]
			delete(c.calls, m.replyTo)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		case typeCall:
			c.mu.Lock()
			f := c.onCall
			c.mu.Unlock()
			if f != nil {
				f(m)
			}
		case typeSignal:
			if m.iface != iface {
				continue
			}
			d := m.dec()
			var ev Event
			switch m.member {
			case "ActionInvoked":
				ev = Event{ID: d.u32(), Action: d.str()}
			case "NotificationClosed":
				ev = Event{ID: d.u32(), Closed: true, Reason: d.u32()}
			default:
				continue
			}
			select {
			case c.Events <- ev:
			default:
			}
		}
	}
}

// enc builds a little-endian message; offsets count from the message start
// (a body starts 8-aligned, so a body encoded on its own aligns the same).
type enc struct{ b []byte }

func (e *enc) align(n int) {
	for len(e.b)%n != 0 {
		e.b = append(e.b, 0)
	}
}
func (e *enc) u32(v uint32) { e.align(4); e.b = binary.LittleEndian.AppendUint32(e.b, v) }
func (e *enc) str(s string) { e.u32(uint32(len(s))); e.b = append(append(e.b, s...), 0) }
func (e *enc) sig(s string) { e.b = append(append(append(e.b, byte(len(s))), s...), 0) }

func (e *enc) array(align int, items func()) {
	e.u32(0)
	at := len(e.b) - 4
	e.align(align)
	start := len(e.b)
	items()
	binary.LittleEndian.PutUint32(e.b[at:], uint32(len(e.b)-start))
}

func (e *enc) variant(v any) {
	switch v := v.(type) {
	case string:
		e.sig("s")
		e.str(v)
	case bool:
		e.sig("b")
		b := uint32(0)
		if v {
			b = 1
		}
		e.u32(b)
	case byte:
		e.sig("y")
		e.b = append(e.b, v)
	case int32:
		e.sig("i")
		e.u32(uint32(v))
	case int:
		e.sig("i")
		e.u32(uint32(int32(v)))
	case uint32:
		e.sig("u")
		e.u32(v)
	default:
		e.sig("s")
		e.str(fmt.Sprint(v))
	}
}

func encode(typ byte, serial, replyTo uint32, dst, obj, ifc, member, sig string, body []byte) []byte {
	var e enc
	e.b = append(e.b, 'l', typ, 0, 1)
	e.u32(uint32(len(body)))
	e.u32(serial)
	e.array(8, func() {
		field := func(code byte, t, v string) {
			if v == "" {
				return
			}
			e.align(8)
			e.b = append(e.b, code)
			e.sig(t)
			if t == "g" {
				e.sig(v)
			} else {
				e.str(v)
			}
		}
		field(1, "o", obj)
		field(2, "s", ifc)
		field(3, "s", member)
		if replyTo != 0 {
			e.align(8)
			e.b = append(e.b, 5)
			e.sig("u")
			e.u32(replyTo)
		}
		field(6, "s", dst)
		field(8, "g", sig)
	})
	e.align(8)
	return append(e.b, body...)
}

// dec reads a message; a short or broken one leaves zero values and bad set.
type dec struct {
	b   []byte
	p   int
	o   binary.ByteOrder
	bad bool
}

func (d *dec) align(n int) { d.p = (d.p + n - 1) / n * n }

func (d *dec) take(n int) []byte {
	if d.bad || n < 0 || d.p+n > len(d.b) {
		d.bad = true
		return nil
	}
	s := d.b[d.p : d.p+n]
	d.p += n
	return s
}

func (d *dec) u32() uint32 {
	d.align(4)
	if s := d.take(4); s != nil {
		return d.o.Uint32(s)
	}
	return 0
}

func (d *dec) str() string {
	n := int(d.u32())
	if s := d.take(n + 1); s != nil {
		return string(s[:n])
	}
	return ""
}

func (d *dec) sig() string {
	l := d.take(1)
	if l == nil {
		return ""
	}
	if s := d.take(int(l[0]) + 1); s != nil {
		return string(s[:l[0]])
	}
	return ""
}

func readMessage(r io.Reader) (*message, error) {
	head := make([]byte, 16)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, err
	}
	var o binary.ByteOrder = binary.LittleEndian
	if head[0] == 'B' {
		o = binary.BigEndian
	}
	bodyLen, fieldsLen := o.Uint32(head[4:]), o.Uint32(head[12:])
	if bodyLen > 1<<26 || fieldsLen > 1<<26 {
		return nil, errors.New("message too large")
	}
	fieldsEnd := 16 + int(fieldsLen)
	bodyAt := (fieldsEnd + 7) &^ 7
	buf := make([]byte, bodyAt+int(bodyLen))
	copy(buf, head)
	if _, err := io.ReadFull(r, buf[16:]); err != nil {
		return nil, err
	}
	m := &message{typ: head[1], serial: o.Uint32(head[8:]), order: o, body: buf[bodyAt:]}
	d := &dec{b: buf[:fieldsEnd], p: 16, o: o}
	for d.p < fieldsEnd && !d.bad {
		d.align(8)
		code := d.take(1)
		if code == nil {
			break
		}
		switch t := d.sig(); t {
		case "u":
			if v := d.u32(); code[0] == 5 {
				m.replyTo = v
			}
		case "g":
			if v := d.sig(); code[0] == 8 {
				m.sig = v
			}
		case "s", "o":
			switch v := d.str(); code[0] {
			case 2:
				m.iface = v
			case 3:
				m.member = v
			case 4:
				m.errName = v
			case 7:
				m.sender = v
			}
		default:
			return nil, fmt.Errorf("header field of type %q", t)
		}
	}
	if d.bad {
		return nil, errors.New("malformed message")
	}
	return m, nil
}
