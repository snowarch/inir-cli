package notify

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Fake is a private session bus with a notification server on it that
// records what it is sent, for tests.
type Fake struct {
	Address string
	daemon  *exec.Cmd
	srv     *Conn
	mu      sync.Mutex
	notes   []Note
	next    uint32
}

// StartFake starts dbus-daemon on a private bus and owns the notifications
// name there. Point DBUS_SESSION_BUS_ADDRESS at Address to use it.
func StartFake() (*Fake, error) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		return nil, err
	}
	d := exec.Command("dbus-daemon", "--session", "--nofork", "--nopidfile", "--print-address=1")
	out, err := d.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := d.Start(); err != nil {
		return nil, err
	}
	addr, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		d.Process.Kill()
		return nil, err
	}
	f := &Fake{Address: strings.TrimSpace(addr), daemon: d}
	old := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	os.Setenv("DBUS_SESSION_BUS_ADDRESS", f.Address)
	f.srv, err = Dial()
	os.Setenv("DBUS_SESSION_BUS_ADDRESS", old)
	if err != nil {
		f.Stop()
		return nil, err
	}
	f.srv.mu.Lock()
	f.srv.onCall = f.serve
	f.srv.mu.Unlock()
	var e enc
	e.str(dest)
	e.u32(4) // do not queue
	m, err := f.srv.bus("RequestName", "su", e.b)
	if err != nil || m.dec().u32() != 1 {
		f.Stop()
		return nil, errors.Join(errors.New("could not own "+dest), err)
	}
	return f, nil
}

func (f *Fake) serve(m *message) {
	if m.member != "Notify" {
		return
	}
	d := m.dec()
	n := Note{App: d.str(), Replaces: d.u32(), Icon: d.str(), Summary: d.str(), Body: d.str(), Hints: map[string]any{}}
	end := int(d.u32())
	end += d.p
	for d.p < end && !d.bad {
		n.Actions = append(n.Actions, d.str())
	}
	end = int(d.u32())
	d.align(8)
	end += d.p
	for d.p < end && !d.bad {
		d.align(8)
		k := d.str()
		switch d.sig() {
		case "s":
			n.Hints[k] = d.str()
		case "b":
			n.Hints[k] = d.u32() != 0
		case "y":
			if b := d.take(1); b != nil {
				n.Hints[k] = b[0]
			}
		case "i":
			n.Hints[k] = int32(d.u32())
		case "u":
			n.Hints[k] = d.u32()
		default:
			d.bad = true
		}
	}
	n.Timeout = int32(d.u32())
	f.mu.Lock()
	id := n.Replaces
	if id == 0 {
		f.next++
		id = f.next
	}
	f.notes = append(f.notes, n)
	f.mu.Unlock()
	var e enc
	e.u32(id)
	f.srv.send(typeReturn, m.serial, m.sender, "", "", "", "u", e.b)
}

// Notes is every notification received so far, in order.
func (f *Fake) Notes() []Note {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Note(nil), f.notes...)
}

// Invoke presses an action of notification id, as a person clicking it would.
func (f *Fake) Invoke(id uint32, action string) error {
	var e enc
	e.u32(id)
	e.str(action)
	return f.srv.send(typeSignal, 0, "", path, iface, "ActionInvoked", "us", e.b)
}

func (f *Fake) Stop() {
	if f.srv != nil {
		f.srv.Close()
	}
	f.daemon.Process.Kill()
	f.daemon.Wait()
}
