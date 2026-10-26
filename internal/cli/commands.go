// Package cli describes the commands of the iNiR launcher that are not IPC
// calls: their words, flags and subcommands, as scripts/inir parses them.
package cli

// Flag is an option of a command. Value names what the flag takes, if anything;
// Values lists the finite set it accepts, when there is one.
type Flag struct {
	Name   string
	Desc   string
	Value  string
	Values []string
	Dir    bool // the value is a directory
}

// Sub is a subcommand with its own flags.
type Sub struct {
	Name  string
	Desc  string
	Flags []Flag
}

// Command is a top-level word of `inir` that the launcher handles itself.
type Command struct {
	Name   string
	Desc   string
	Flags  []Flag
	Subs   []Sub
	Hidden bool // run by the service or the shell, not offered to people
	// Target names the IPC target whose functions also follow this word
	// (`inir hub <function>`, `inir dev <function>`).
	Target string
}

var config = Flag{Name: "--config", Desc: "Shell directory to use", Value: "PATH", Dir: true}
var configShort = Flag{Name: "-c", Desc: "Shell directory to use", Value: "PATH", Dir: true}
var help = Flag{Name: "--help", Desc: "Show help"}

func withConfig(flags ...Flag) []Flag {
	return append(flags, configShort, config, help)
}

var yes = Flag{Name: "--yes", Desc: "Answer yes to every question"}
var quiet = Flag{Name: "--quiet", Desc: "Print less"}

var installFlags = []Flag{
	yes, quiet,
	{Name: "--skip-deps", Desc: "Leave system packages alone"},
	{Name: "--skip-setups", Desc: "Skip services and permissions"},
	{Name: "--skip-files", Desc: "Skip copying config files"},
	{Name: "--skip-quickshell", Desc: "Skip syncing the shell files"},
	{Name: "--skip-niri", Desc: "Skip the Niri config"},
	{Name: "--skip-backup", Desc: "No snapshot before installing"},
	{Name: "--skip-sysupdate", Desc: "Skip the system upgrade first"},
	{Name: "--no-audio", Desc: "Without the audio tools"},
	{Name: "--no-toolkit", Desc: "Without ydotool and backlight tools"},
	{Name: "--no-screencapture", Desc: "Without screenshots and recording"},
	{Name: "--no-fonts", Desc: "Without fonts and themes"},
	{Name: "--set-default-shell", Desc: "Make fish the default shell"},
	{Name: "--firstrun", Desc: "Act as a first run, backing up configs"},
	{Name: "--reset-dolphin-layout", Desc: "Reset Dolphin's panel layout"},
	help,
}

// Commands is every word scripts/inir's dispatch takes before it treats a word
// as an IPC target, in the order `inir help` lists them.
var Commands = []Command{
	{Name: "run", Desc: "Run the shell; inir run -- CMD runs CMD with a live notification", Flags: withConfig(
		Flag{Name: "--daemon", Desc: "In the background"},
		Flag{Name: "--foreground", Desc: "In this terminal"},
		Flag{Name: "--session", Desc: "As the session service starts it"})},
	{Name: "start", Desc: "Start the shell", Flags: withConfig()},
	{Name: "stop", Desc: "Stop the shell", Flags: withConfig()},
	{Name: "restart", Desc: "Restart the shell", Flags: withConfig(quiet)},
	{Name: "kill", Desc: "Kill the shell process", Flags: withConfig()},
	{Name: "settings", Desc: "Toggle settings", Flags: withConfig()},
	{Name: "open", Desc: "Open settings", Flags: withConfig()},
	{Name: "welcome", Desc: "Open the welcome tour again", Flags: withConfig()},
	{Name: "logs", Desc: "Runtime logs", Flags: []Flag{
		{Name: "--issues", Desc: "Every warning and error since start, grouped"},
		{Name: "-n", Desc: "Last N lines", Value: "N"},
		{Name: "--lines", Desc: "Last N lines", Value: "N"},
		{Name: "--full", Desc: "All categories"},
		{Name: "--debug", Desc: "Run in the foreground with debug output"},
		help}},
	{Name: "status", Desc: "Install mode, update strategy and health"},
	{Name: "doctor", Desc: "Check the install and fix what it can", Flags: withConfig(
		Flag{Name: "--check", Desc: "Only check, change nothing"},
		Flag{Name: "--json", Desc: "The report as JSON"},
		Flag{Name: "--offline", Desc: "Do not fetch the repo's remote"},
		Flag{Name: "--only", Desc: "Just these steps (--only=5,8)", Value: "STEPS"},
		Flag{Name: "--perf", Desc: "Snapshot of graphics and media at runtime"},
		Flag{Name: "--fix-abi", Desc: "Rebuild Quickshell against the installed Qt"},
		Flag{Name: "--yes", Desc: "Rebuild without asking"})},
	{Name: "repair", Desc: "Doctor, restart and recent logs", Flags: withConfig()},
	{Name: "install", Desc: "Install iNiR: packages, config and files", Flags: installFlags},
	{Name: "reinstall", Desc: "Run the install again over this setup", Flags: []Flag{yes, quiet, help}},
	{Name: "update", Desc: "Pull the latest iNiR, sync and restart", Flags: []Flag{
		yes, quiet,
		{Name: "--local", Desc: "Sync this checkout, no git pull"},
		{Name: "--realign", Desc: "Realign an old checkout after rewritten history"},
		{Name: "--dry-run", Desc: "Say what would change, change nothing"},
		help}},
	{Name: "migrate", Desc: "Review and apply config migrations"},
	{Name: "rollback", Desc: "Restore a previous snapshot"},
	{Name: "my-changes", Desc: "View or restore your preserved changes"},
	{Name: "uninstall", Desc: "Remove iNiR from the system"},
	{Name: "config", Desc: "View and edit shell settings"},
	{Name: "info", Desc: "System and shell details"},
	{Name: "backup", Desc: "Create a manual snapshot"},
	{Name: "setup", Desc: "Run setup directly", Flags: withConfig()},
	{Name: "version", Desc: "Show the version", Flags: []Flag{{Name: "--json", Desc: "As JSON"}, help}},
	{Name: "theme", Desc: "Theming targets for other apps", Subs: []Sub{
		{Name: "list-targets", Desc: "List declared theming targets"},
		{Name: "inspect", Desc: "Manifest, module and inputs of a target"},
		{Name: "doctor", Desc: "Validate all targets or one"},
		{Name: "scaffold", Desc: "Create a target manifest and module stub", Flags: []Flag{
			{Name: "--label", Desc: "Name people see", Value: "TEXT"},
			{Name: "--category", Desc: "Group it belongs to", Value: "NAME"},
			{Name: "--inputs", Desc: "Inputs it reads", Value: "LIST"}}},
		{Name: "apply", Desc: "Apply one or more targets, or all"}}},
	{Name: "hub", Desc: "Community hub: widgets, themes, web apps", Target: "hub", Flags: []Flag{
		{Name: "--json", Desc: "As JSON"}, help}, Subs: []Sub{
		{Name: "list", Desc: "What the hub has", Flags: []Flag{
			{Name: "--installed", Desc: "Only what you installed"},
			{Name: "--updates", Desc: "Only what has an update"},
			{Name: "--kind", Desc: "Only one kind", Value: "KIND",
				Values: []string{"widget", "theme", "iris-theme", "webapp"}}}},
		{Name: "search", Desc: "Search by text"},
		{Name: "info", Desc: "One item in detail"},
		{Name: "install", Desc: "Install items"},
		{Name: "update", Desc: "Update items, or all"},
		{Name: "remove", Desc: "Remove items"},
		{Name: "sync", Desc: "Fetch the catalogue"},
		{Name: "status", Desc: "Catalogue age and source", Flags: []Flag{
			{Name: "--max-age", Desc: "Fetch if older than this", Value: "SECONDS"},
			{Name: "--cached", Desc: "Never fetch"}}},
		{Name: "sources", Desc: "Where the catalogue comes from"},
		{Name: "open", Desc: "Open the hub page, or one item"},
		{Name: "refresh", Desc: "Tell the shell to read the hub again"},
		{Name: "changed", Desc: "Tell the shell a kind changed"}}},
	{Name: "bind", Desc: "Make a Niri keybind for any inir call"},
	{Name: "completions", Desc: "Shell completions", Subs: []Sub{
		{Name: "bash", Desc: "Print the bash completion"},
		{Name: "zsh", Desc: "Print the zsh completion"},
		{Name: "fish", Desc: "Print the fish completion"},
		{Name: "install", Desc: "Install them where your shell looks"}}},
	{Name: "service", Desc: "Manage the user service", Subs: []Sub{
		{Name: "install", Desc: "Install the service"},
		{Name: "uninstall", Desc: "Remove the service"},
		{Name: "enable", Desc: "Start it with the session"},
		{Name: "disable", Desc: "Do not start it with the session"},
		{Name: "start", Desc: "Start it now"},
		{Name: "stop", Desc: "Stop it now"},
		{Name: "restart", Desc: "Restart it"},
		{Name: "status", Desc: "Whether it runs"},
		{Name: "logs", Desc: "Its journal", Flags: []Flag{{Name: "--full", Desc: "All categories"}}}}},
	{Name: "ipc", Desc: "Call an IPC function", Flags: withConfig()},
	{Name: "terminal", Desc: "Open the terminal", Flags: withConfig()},
	{Name: "browser", Desc: "Open the browser", Flags: withConfig()},
	{Name: "close-window", Desc: "Close the focused window", Flags: withConfig()},
	{Name: "colorpicker", Desc: "Pick a color from the screen", Flags: []Flag{help}},
	{Name: "settings-window", Desc: "Open the family's Settings window", Flags: withConfig(
		Flag{Name: "--toggle", Desc: "Close it if open"})},
	{Name: "waffle-settings-window", Desc: "Old name of settings-window", Flags: withConfig(
		Flag{Name: "--toggle", Desc: "Close it if open"})},
	{Name: "path", Desc: "Print the shell directory", Flags: withConfig()},
	{Name: "dev", Desc: "Developer tools", Target: "dev", Subs: []Sub{
		{Name: "audit", Desc: "Audit destinations touched by changed files", Flags: []Flag{
			{Name: "--all", Desc: "Every destination"},
			{Name: "--all-families", Desc: "Material, Waffle and iRiS"},
			help}}}},
	{Name: "help", Desc: "Show help"},
	{Name: "cleanup-orphans", Desc: "Kill what an old shell left running", Hidden: true},
	{Name: "kill-dialog", Desc: "The dialog that offers to stop conflicting programs", Hidden: true},
}

// BinaryCommands are words only the inir binary answers; scripts/inir does
// not dispatch them, so they stay out of Commands.
var BinaryCommands = []Command{
	{Name: "cli", Desc: "The inir binary itself", Subs: []Sub{
		{Name: "version", Desc: "Version of the binary (not of the shell)"}}},
	{Name: "deps", Desc: "What an install puts on this machine", Subs: []Sub{
		{Name: "plan", Desc: "Packages and providers from the shell's deps.toml", Flags: []Flag{
			{Name: "--distro", Desc: "Plan for another distro", Value: "DISTRO",
				Values: []string{"arch", "fedora", "debian", "ubuntu", "void"}},
			{Name: "--no-audio", Desc: "Without the audio group"},
			{Name: "--no-toolkit", Desc: "Without the toolkit group"},
			{Name: "--no-screencapture", Desc: "Without the screencapture group"},
			{Name: "--no-fonts", Desc: "Without the fonts group"},
			{Name: "--table", Desc: "Another deps.toml", Value: "PATH"},
			{Name: "--json", Desc: "As JSON"}}},
		{Name: "install", Desc: "Install what the plan says is missing", Flags: []Flag{
			{Name: "-y", Desc: "Don't ask"},
			{Name: "--yes", Desc: "Don't ask"},
			{Name: "--dry-run", Desc: "Show the steps, change nothing"},
			{Name: "--no-audio", Desc: "Without the audio group"},
			{Name: "--no-toolkit", Desc: "Without the toolkit group"},
			{Name: "--no-screencapture", Desc: "Without the screencapture group"},
			{Name: "--no-fonts", Desc: "Without the fonts group"}}}}},
	{Name: "look", Desc: "Your whole look in one file: share it, apply one, go back", Subs: []Sub{
		{Name: "export", Desc: "Write your look to FILE (stdout when piped or -)", Flags: []Flag{
			{Name: "--name", Desc: "What to call it", Value: "NAME"},
			{Name: "--wallpaper", Desc: "Link to the wallpaper for people who don't have it", Value: "URL"}}},
		{Name: "import", Desc: "Apply a look file or https link, with a snapshot first", Flags: []Flag{
			{Name: "--dry-run", Desc: "Show every setting it would change"},
			{Name: "--no-wallpaper", Desc: "Keep your wallpaper"}}},
		{Name: "history", Desc: "Snapshots taken before each import or restore", Flags: []Flag{
			{Name: "--json", Desc: "As JSON"}}},
		{Name: "restore", Desc: "Go back to snapshot N of the history", Flags: []Flag{
			{Name: "--dry-run", Desc: "Show every setting it would change"},
			{Name: "--no-wallpaper", Desc: "Keep your wallpaper"}}}}},
	{Name: "hook", Desc: "Live notifications for long commands in your shell", Subs: []Sub{
		{Name: "bash", Desc: `eval "$(inir hook bash)" in ~/.bashrc`, Flags: []Flag{hookAfter}},
		{Name: "zsh", Desc: `eval "$(inir hook zsh)" in ~/.zshrc`, Flags: []Flag{hookAfter}},
		{Name: "fish", Desc: "inir hook fish | source in config.fish", Flags: []Flag{hookAfter}}}},
}

var hookAfter = Flag{Name: "--after", Desc: "Seconds a command runs before it shows (10)", Value: "SECONDS"}

// BindFlags are the options of `inir bind` before the keys.
var BindFlags = []Flag{
	{Name: "--add", Desc: "Write the bind into your own block of 90-user-extra.kdl"},
	{Name: "--remove", Desc: "Take a bind you added back out"},
	{Name: "--list", Desc: "The binds you added"},
	{Name: "--examples", Desc: "Ready binds for what people do most",
		Values: []string{"iris", "ii", "waffle", "all"}, Value: "FAMILY"},
	help,
}

var byName = func() map[string]*Command {
	m := make(map[string]*Command, len(Commands))
	for i := range Commands {
		m[Commands[i].Name] = &Commands[i]
	}
	return m
}()

var binaryByName = func() map[string]*Command {
	m := make(map[string]*Command, len(BinaryCommands))
	for i := range BinaryCommands {
		m[BinaryCommands[i].Name] = &BinaryCommands[i]
	}
	return m
}()

// Lookup finds a launcher command by its word.
func Lookup(name string) (*Command, bool) {
	c, ok := byName[name]
	return c, ok
}

// LookupAny also finds the binary's own commands.
func LookupAny(name string) (*Command, bool) {
	if c, ok := byName[name]; ok {
		return c, true
	}
	c, ok := binaryByName[name]
	return c, ok
}

// Flag finds a flag of the command, or of one of its subcommands.
func (c *Command) Flag(name string, sub *Sub) (*Flag, bool) {
	flags := c.Flags
	if sub != nil {
		flags = append(append([]Flag{}, sub.Flags...), c.Flags...)
	}
	for i := range flags {
		if flags[i].Name == name {
			return &flags[i], true
		}
	}
	return nil, false
}

// Sub finds a subcommand by name.
func (c *Command) Sub(name string) (*Sub, bool) {
	for i := range c.Subs {
		if c.Subs[i].Name == name {
			return &c.Subs[i], true
		}
	}
	return nil, false
}
