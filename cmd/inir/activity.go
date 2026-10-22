package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/snowarch/inir-cli/internal/activity"
	"github.com/snowarch/inir-cli/internal/ui"
)

const runUsage = `usage: inir run [--title TEXT] -- COMMAND [ARGS...]
Runs COMMAND here with a notification that follows it: the percentage it
prints, or the time so far, then how it ended. Plain "inir run" starts the shell.`

const hookUsage = `usage: inir hook bash|zsh|fish [--after SECONDS]
Commands that run longer than SECONDS (10) get a live notification.
  bash  eval "$(inir hook bash)"       in ~/.bashrc
  zsh   eval "$(inir hook zsh)"        in ~/.zshrc
  fish  inir hook fish | source        in ~/.config/fish/config.fish
INIR_HOOK_AFTER changes the seconds, INIR_HOOK_IGNORE adds programs to leave alone.`

// activityArgs reads `inir run [--title T] -- CMD...`. Without the "--" the
// word still starts the shell, through the launcher.
func activityArgs(args []string) (argv []string, title string, ok bool) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			return args[i+1:], title, true
		case (a == "--title" || a == "-t") && i+1 < len(args):
			i++
			title = args[i]
		default:
			return nil, "", false
		}
	}
	return nil, "", false
}

func runActivity(argv []string, title string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, runUsage)
		return 2
	}
	return activity.Run(argv, title)
}

func runHook(args []string) int {
	shell, after := "", activity.DefaultAfter
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-h" || a == "--help":
			fmt.Println(hookUsage)
			return 0
		case a == "--after" && i+1 < len(args):
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				ui.Log("inir hook").Error("--after takes whole seconds", "got", args[i])
				return 2
			}
			after = n
		case shell == "":
			shell = a
		default:
			ui.Log("inir hook").Error("unknown argument", "arg", a)
			return 2
		}
	}
	if shell == "" {
		fmt.Fprintln(os.Stderr, hookUsage)
		return 2
	}
	exe, err := os.Executable()
	if err != nil {
		ui.Log("inir hook").Error(err)
		return 1
	}
	s, err := activity.Script(shell, after, exe)
	if err != nil {
		ui.Log("inir hook").Error(err)
		return 2
	}
	fmt.Print(s)
	return 0
}
