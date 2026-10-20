package installer

import (
	"errors"
	"os/exec"
	"os/user"
	"strings"
	"time"

	"charm.land/huh/v2"

	"github.com/snowarch/inir-cli/internal/ui"
)

// Authenticate gets sudo's ticket before the install starts, so setup's own
// sudo calls never stop to ask while the progress view owns the terminal. The
// password goes to `sudo -S` on a pipe, never in argv, and is not kept.
func Authenticate(t ui.Theme, accessible bool) error {
	if exec.Command("sudo", "-n", "true").Run() == nil {
		return nil
	}
	name := "your user"
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	var pw string
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Password for " + name).
			Description("For the package manager and system services. Asked once, for the whole install.").
			EchoMode(huh.EchoModePassword).
			Value(&pw).
			Validate(func(s string) error {
				if s == "" {
					return errors.New("type your password")
				}
				if err := sudoValidate(s); err != nil {
					return err
				}
				return nil
			}),
	)).WithTheme(ui.FormTheme(t)).WithKeyMap(ui.FormKeys()).WithWidth(min(ui.Width()-4, 88)).WithAccessible(accessible).Run()
	pw = ""
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrCancelled
	}
	return err
}

func sudoValidate(pw string) error {
	cmd := exec.Command("sudo", "-S", "-p", "", "-v")
	cmd.Stdin = strings.NewReader(pw + "\n")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if strings.Contains(string(out), "not in the sudoers") || strings.Contains(string(out), "not allowed") {
		return errors.New("this user cannot use sudo")
	}
	return errors.New("that password did not work")
}

// KeepSudo refreshes the ticket until stop closes, so a long build does not
// outlive it.
func KeepSudo(stop <-chan struct{}) {
	tick := time.NewTicker(45 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			exec.Command("sudo", "-n", "-v").Run()
		}
	}
}
