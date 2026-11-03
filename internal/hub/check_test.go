package hub

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A hub repo in git with main holding one widget and one family; each case commits a branch on top and runs Check.
func checkRepo(t *testing.T) (*Repo, func(files map[string]string)) {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(files map[string]string) {
		for name, body := range files {
			p := filepath.Join(root, name)
			os.MkdirAll(filepath.Dir(p), 0o755)
			if body == "" {
				os.Remove(p)
				continue
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	run("init", "-q", "-b", "main")
	write(map[string]string{
		"schema/hub.schema.json":               "{}",
		"README.md":                            "hub\n",
		"families/fam/hub.json":                `{"version": "1.0.0"}`,
		"families/fam/Panel.qml":               "import QtQuick\nItem {}\n",
		"families/fam/translations/en_US.json": `{"Hello": "Hello"}`,
		"families/fam/translations/de_DE.json": `{"Hello": "Hallo"}`,
		"widgets/clock/hub.json":               `{"version": "1.0.0"}`,
		"widgets/clock/Clock.qml":              "import QtQuick\nItem {}\n",
	})
	run("add", "-A")
	run("commit", "-q", "-m", "main")
	run("update-ref", "refs/remotes/origin/main", "HEAD")
	run("switch", "-q", "-c", "pr")
	return &Repo{Root: root}, write
}

// Check's errors; the cases assert on the pull request's own rules, the bare listings' errors pass by.
func checkOnly(r *Repo) []string {
	r.Check("origin/main")
	return r.Errors
}

func TestCheckOneItemOnly(t *testing.T) {
	r, write := checkRepo(t)
	write(map[string]string{
		"widgets/clock/Clock.qml": "import QtQuick\nItem { width: 2 }\n",
		"widgets/clock/hub.json":  `{"version": "1.0.1"}`,
		"families/fam/Panel.qml":  "import QtQuick\nItem { width: 3 }\n",
	})
	if errs := checkOnly(r); !has(errs, "one item per pull request") {
		t.Fatalf("two items passed: %v", errs)
	}

	r, write = checkRepo(t)
	write(map[string]string{
		"widgets/clock/Clock.qml": "import QtQuick\nItem { width: 2 }\n",
		"widgets/clock/hub.json":  `{"version": "1.0.1"}`,
		"README.md":               "hub, changed\n",
		"schema/hub.schema.json":  `{"x": 1}`,
	})
	errs := checkOnly(r)
	if !has(errs, "README.md: outside any item") || !has(errs, "schema/hub.schema.json: outside any item") {
		t.Fatalf("files outside the item passed: %v", errs)
	}

	r, write = checkRepo(t)
	write(map[string]string{
		"families/fam/Panel.qml": "import QtQuick\nItem { width: 3 }\n",
		"families/fam/hub.json":  `{"version": "1.0.1"}`,
		"docs/fam/FAM.md":        "# Fam\n",
		"docs/clock/CLOCK.md":    "# Clock\n",
	})
	errs = checkOnly(r)
	if has(errs, "docs/fam") || !has(errs, "docs/clock/ is another item's page") {
		t.Fatalf("an item's own page must pass and another's fail: %v", errs)
	}
}

func TestCheckVersionRaised(t *testing.T) {
	r, write := checkRepo(t)
	write(map[string]string{"widgets/clock/Clock.qml": "import QtQuick\nItem { width: 2 }\n"})
	if errs := checkOnly(r); !has(errs, `raise "version" past 1.0.0`) {
		t.Fatalf("a change without a new version passed: %v", errs)
	}
}

func TestCheckContracts(t *testing.T) {
	r, write := checkRepo(t)
	write(map[string]string{
		"families/fam/hub.json": `{"version": "1.0.1"}`,
		"families/fam/Panel.qml": `import QtQuick
import Quickshell.Io
Item {
    IpcHandler {
        target: "famThing"
        function toggle(): void {}
        /// Open it
        function open(): void {}
    }
    /// The thing.
    IpcHandler {
        target: "famOther"
        /// Close it
        function close(): void {}
    }
}
`,
		"families/fam/translations/en_US.json": `{"Hello": "Hello", "Bye": "Bye"}`,
	})
	errs := checkOnly(r)
	if !has(errs, "Panel.qml:4: IpcHandler famThing has no `///`") || !has(errs, "Panel.qml:6: IPC function toggle") {
		t.Fatalf("undocumented IPC passed: %v", errs)
	}
	if has(errs, "famOther") || has(errs, "function open") || has(errs, "function close") {
		t.Fatalf("documented IPC failed: %v", errs)
	}
	if !has(errs, `de_DE.json: 1 of en_US.json's strings are missing ("Bye")`) {
		t.Fatalf("a half-translated language passed: %v", errs)
	}
}

func TestCheckAddedLinesOnly(t *testing.T) {
	r, write := checkRepo(t)
	// What main already holds never fails a pull request: only the lines it adds are read.
	write(map[string]string{
		"widgets/clock/hub.json": `{"version": "1.0.1"}`,
		"widgets/clock/Clock.qml": `import QtQuick
import Qt5Compat.GraphicalEffects
Item {
    Rectangle { color: "#ff0000" }
    Rectangle { color: Qt.rgba(1, 0, 0, 0.5) }
    Rectangle { color: Appearance.colors.colPrimary }
    Rectangle { color: "#ff0000" } // literal: the flag's own red
    Text { font.pixelSize: 14 }
    Text { font.pixelSize: Appearance.font.pixelSize.small }
    Rectangle { color: ColorUtils.applyAlpha(root.widgetInk, 0.5) }
}
`,
	})
	errs := checkOnly(r)
	for _, want := range []string{"Clock.qml:2: Qt5Compat", "Clock.qml:4: a colour of its own", "Clock.qml:5: a colour of its own", "Clock.qml:8: a fixed font size"} {
		if !has(errs, want) {
			t.Errorf("missing %q in %v", want, errs)
		}
	}
	for _, not := range []string{"Clock.qml:6:", "Clock.qml:7:", "Clock.qml:9:", "Clock.qml:10:"} {
		if has(errs, not) {
			t.Errorf("flagged %q: %v", not, errs)
		}
	}

	r, write = checkRepo(t)
	write(map[string]string{
		"families/fam/hub.json": `{"version": "1.0.1"}`,
		"families/fam/Panel.qml": `import QtQuick
import qs.modules.iris.style
Item {
    Rectangle { color: ColorUtils.applyAlpha(IrisStyle.surfaceOpaque, 0.38) }
    Rectangle { color: ColorUtils.applyAlpha(IrisStyle.surfaceOpaque, light ? 0.12 : 0.14) }
    Rectangle { color: IrisStyle.fillQuiet }
    Rectangle { color: ColorUtils.applyAlpha(IrisStyle.surfaceOpaque, 0.9) } // iris-literal: the banner's fade
}
`,
		"families/fam/style/FamStyle.qml": "import QtQuick\nQtObject { readonly property color ink: \"#101010\" }\n",
	})
	errs = checkOnly(r)
	if !has(errs, "Panel.qml:4: an alpha of its own in iRiS") || !has(errs, "Panel.qml:5: an alpha of its own in iRiS") {
		t.Fatalf("an iRiS alpha passed: %v", errs)
	}
	if has(errs, "Panel.qml:6") || has(errs, "Panel.qml:7") || has(errs, "FamStyle.qml") {
		t.Fatalf("tokens, a said literal or the family's token file failed: %v", errs)
	}
}
