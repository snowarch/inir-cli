package hub

import (
	"fmt"
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
	r.Check("origin/main", true)
	return r.Errors
}

func checkAs(r *Repo, maintainer bool) []string {
	r.Check("origin/main", maintainer)
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

func TestCheckLanes(t *testing.T) {
	// A family changes only from its maintainers.
	r, write := checkRepo(t)
	write(map[string]string{
		"families/fam/Panel.qml": "import QtQuick\nItem { width: 3 }\n",
		"families/fam/hub.json":  `{"version": "1.0.1"}`,
	})
	if errs := checkAs(r, false); !has(errs, "kept by its maintainers") {
		t.Fatalf("a family change from outside passed: %v", errs)
	}
	r, write = checkRepo(t)
	write(map[string]string{
		"families/fam/Panel.qml": "import QtQuick\nItem { width: 3 }\n",
		"families/fam/hub.json":  `{"version": "1.0.1"}`,
	})
	if errs := checkAs(r, true); has(errs, "kept by its maintainers") {
		t.Fatalf("a maintainer's family change failed: %v", errs)
	}

	// A new item can't take an id another kind has.
	r, write = checkRepo(t)
	write(map[string]string{
		"components/clock/hub.json":  `{"version": "1.0.0"}`,
		"components/clock/Clock.qml": "import QtQuick\nItem {}\n",
	})
	if errs := checkAs(r, false); !has(errs, `the id "clock" is taken by widgets/clock`) {
		t.Fatalf("a duplicate id passed: %v", errs)
	}

	// Outside families: no writes to the person's settings, IPC targets carry the id.
	r, write = checkRepo(t)
	write(map[string]string{
		"widgets/clock/hub.json": `{"version": "1.0.1"}`,
		"widgets/clock/Clock.qml": `import QtQuick
import Quickshell.Io
Item {
    Component.onCompleted: Config.setNestedValue("appearance.globalStyle", "aurora")
    function f() { Config.options.bar.vertical = true }
    readonly property bool same: Config.options.bar.vertical == true
    /// Clock.
    IpcHandler {
        target: "mpris"
        /// Play
        function play(): void {}
    }
    /// Its own.
    IpcHandler {
        target: "clockFace"
        /// Show
        function show(): void {}
    }
}
`,
	})
	errs := checkAs(r, false)
	for _, want := range []string{"Clock.qml:4: writes the person's settings", "Clock.qml:5: writes the person's settings", `Clock.qml:9: IPC target "mpris"`} {
		if !has(errs, want) {
			t.Errorf("missing %q in %v", want, errs)
		}
	}
	for _, not := range []string{"Clock.qml:6:", `"clockFace"`} {
		if has(errs, not) {
			t.Errorf("flagged %q: %v", not, errs)
		}
	}
}

func TestCheckParts(t *testing.T) {
	r, write := checkRepo(t)
	write(map[string]string{
		"components/corner/hub.json":    `{"version": "1.0.0", "provides": "modules/corner"}`,
		"components/corner/plugin.json": `{"id": "corner", "kind": "component", "parts": {"panel": "Panel.qml", "gone": "Gone.qml"}, "families": ["iris", "Not An Id"]}`,
		"components/corner/Panel.qml":   "import QtQuick\nItem {}\n",
	})
	errs := checkAs(r, false)
	if !has(errs, `"parts" → "gone": "Gone.qml" is not a file`) || !has(errs, `"families" lists the family ids`) {
		t.Fatalf("a missing part or a bad families list passed: %v", errs)
	}
	if has(errs, `"panel"`) {
		t.Fatalf("a present part failed: %v", errs)
	}
}

func TestCheckReach(t *testing.T) {
	big := func(n int, line string) string {
		var b []byte
		for i := 0; i < n; i++ {
			b = append(b, fmt.Sprintf(line, i)...)
		}
		return string(b)
	}
	family := "import QtQuick\nItem {\n" + big(40, "    property int step%d: %[1]d * 2 + width\n") + "}\n"
	// A family's file copied into an item, a family's inside imported, the shell's binary started by name.
	r, write := checkRepo(t)
	write(map[string]string{"families/fam/Watch.qml": family})
	r.git("add", "-A")
	r.git("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "fam")
	r.git("update-ref", "refs/remotes/origin/main", "HEAD")
	write(map[string]string{
		"components/corner/hub.json":  `{"version": "1.0.0"}`,
		"components/corner/Watch.qml": family,
		"components/corner/Panel.qml": "import QtQuick\nimport qs.modules.iris.style\nimport qs.modules.iris.state\nItem {\n    // qs -c inir in a comment is fine\n    function f() { Quickshell.execDetached([\"qs\", \"-c\", \"inir\", \"ipc\"]) }\n}\n",
		"components/corner/Other.qml": "import qs.modules.iris.state\nItem {}\n",
		"components/corner/Big.qml":   "import QtQuick\nItem {\n" + big(700, "    // line %d\n") + "}\n",
	})
	errs := checkAs(r, false)
	for _, want := range []string{
		"Watch.qml: 100% of it is families/fam/Watch.qml",
		"Other.qml:1: imports qs.modules.iris.state (and 1 more files)",
		"Panel.qml:6: starts the shell's binary by name",
		"Big.qml: 703 lines in one file",
	} {
		if !has(errs, want) {
			t.Errorf("missing %q in %v", want, errs)
		}
	}
	for _, not := range []string{"imports qs.modules.iris.style", "Panel.qml:5:", "lines of code in one pull request"} {
		if has(errs, not) {
			t.Errorf("flagged %q: %v", not, errs)
		}
	}

	// From outside, a pull request reads whole: past 2500 lines of code it is sent in steps. A maintainer's is not held.
	for _, maintainer := range []bool{false, true} {
		r, write = checkRepo(t)
		files := map[string]string{"components/huge/hub.json": `{"version": "1.0.0"}`}
		for i := 0; i < 6; i++ {
			files[fmt.Sprintf("components/huge/Part%d.qml", i)] = "import QtQuick\nItem {\n" + big(450, fmt.Sprintf("    property int p%d_%%d: 1\n", i)) + "}\n"
		}
		write(files)
		if got := has(checkAs(r, maintainer), "lines of code in one pull request"); got == maintainer {
			t.Errorf("maintainer %v: size rule held %v", maintainer, got)
		}
	}
}

// A Design is values over its family's components: design.json against the family's schema, three parts at most, no
// shaders, fixed colours that read, and only what a family offers (plus the Island face's kit) imported.
func TestCheckDesign(t *testing.T) {
	schema := `{"type": "object", "additionalProperties": false, "required": ["tokens"], "properties": {"$schema": {"type": "string"},
		"tokens": {"type": "object", "additionalProperties": false, "properties": {
			"shape": {"type": "object", "additionalProperties": false, "properties": {"ladder": {"type": "object", "additionalProperties": false,
				"properties": {"panel": {"$ref": "#/$defs/rung"}}}}},
			"motion": {"type": "object", "additionalProperties": false, "properties": {"morph": {"type": "string", "enum": ["", "direct"]}}},
			"colour": {"type": "object", "additionalProperties": false, "properties": {"roles": {"type": "object", "additionalProperties": {"type": "object"}}}}}}},
		"$defs": {"rung": {"type": "integer", "minimum": 0, "maximum": 48}}}`
	design := func(tokens, parts string, extra map[string]string) map[string]string {
		files := map[string]string{
			"designs/ink/plugin.json":       `{"id": "ink", "kind": "design", "name": "Ink", "version": "1.0.0", "families": ["iris"], "provides": "modules/ink", "parts": ` + parts + `}`,
			"designs/ink/hub.json":          `{"version": "1.0.0", "summary": "A design for checking", "provides": "modules/ink", "license": "MIT", "authors": ["t"]}`,
			"designs/ink/design.json":       `{"tokens": ` + tokens + `}`,
			"designs/ink/parts/Preview.qml": "import QtQuick\nItem {}\n",
		}
		for k, v := range extra {
			files[k] = v
		}
		return files
	}
	// the family's schema is on main: a Design's pull request changes only its own folder
	onMain := func(r *Repo, write func(map[string]string)) {
		write(map[string]string{"families/iris/designs/design.schema.json": schema})
		for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "schema"}, {"update-ref", "refs/remotes/origin/main", "HEAD"}} {
			cmd := exec.Command("git", append([]string{"-C", r.Root}, args...)...)
			cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
	}
	r, write := checkRepo(t)
	onMain(r, write)
	write(design(`{"shape": {"ladder": {"panel": 60}}, "motion": {"morph": "elastic"}, "glow": 1}`, `{"preview": "parts/Preview.qml", "settingsPage": "parts/Preview.qml"}`,
		map[string]string{"designs/ink/field/Ink.frag": "void main() {}\n", "designs/ink/parts/Face.qml": "import QtQuick\nimport qs.modules.iris.settings\nItem {}\n"}))
	errs := checkAs(r, false)
	for _, want := range []string{"design.json.tokens.shape.ladder.panel: above 48", `"elastic" is not one of`, `"glow" is not a value the family reads`,
		`"parts" → "settingsPage"`, "ships no shaders", "imports qs.modules.iris.settings"} {
		if !has(errs, want) {
			t.Errorf("want %q in %v", want, errs)
		}
	}

	r, write = checkRepo(t)
	onMain(r, write)
	write(design(`{"colour": {"roles": {"dark": {"text": "#555555", "surface": "#000000"}}}}`, `{"preview": "parts/Preview.qml"}`,
		map[string]string{"designs/ink/island/Face.qml": "import QtQuick\nimport qs.modules.iris.style\nimport qs.modules.iris.bar.island\nItem {}\n"}))
	errs = checkAs(r, false)
	if !has(errs, "dark: text on surface reads") {
		t.Errorf("unreadable text passed: %v", errs)
	}
	if has(errs, "imports qs.modules.iris.bar.island") || has(errs, "imports qs.modules.iris.style") {
		t.Errorf("a Design's face may take the Island kit and the style: %v", errs)
	}
}
