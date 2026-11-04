package hub

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// A Design (designs/<id>) dresses one family: its design.json holds values in the schema that family owns
// (families/<family>/designs/design.schema.json), read over the family's own, and it draws at most three parts of its
// own. Everything else is the family's one set of components: a Design that ships its own controls, scenes or shaders
// is a second shell that drifts from the first (iRiS's AIRISU once carried 11k lines of them).
var designParts = []string{"islandFace", "insignia", "preview"}

// designOffered: what a Design may import beyond what every item may (offered): the Island face's kit, for its face.
var designOffered = map[string][]string{"iris": {"bar.island"}}

// What fixed colours hold against each other: text on paper, the accent's mark, ink on a filled colour.
var designPairs = []struct {
	ink, ground string
	need        float64
}{
	{"text", "surface", 7}, {"textSecondary", "surface", 4.5}, {"accent", "surface", 3},
	{"onAccentFill", "accentFill", 4.5}, {"onDangerFill", "dangerFill", 4.5}, {"text", "surfaceHigh", 4.5},
}

// designRules: the family it dresses, its parts, no shaders, design.json valid against the family's schema, the faces
// it lists in its folder, and the contrast of every colour it fixes.
func (r *Repo) designRules(folder string, native map[string]any) []string {
	nativeFile := filepath.Join(folder, "plugin.json")
	families := strs(native, "families")
	if len(families) != 1 {
		r.errorf(nativeFile, `"families" names the one family this Design dresses, e.g. ["iris"]`)
		return families
	}
	family := families[0]
	if parts, ok := native["parts"].(map[string]any); ok {
		for _, name := range sortedKeysAny(parts) {
			if !slices.Contains(designParts, name) {
				r.errorf(nativeFile, `"parts" → "%s": a Design draws only %s of its own; the rest is the family's components, dressed by design.json`, name, strings.Join(designParts, ", "))
			}
		}
	}
	for _, p := range filesOf(folder) {
		switch strings.ToLower(filepath.Ext(p)) {
		case ".frag", ".vert", ".qsb", ".glsl":
			r.errorf(p, "a Design ships no shaders: a new material goes in the family for every Design (ask for it in an issue)")
		}
	}
	schemaFile := filepath.Join(r.Root, "families", family, "designs", "design.schema.json")
	if !exists(schemaFile) {
		r.errorf(nativeFile, "%s takes no Designs (no families/%s/designs/design.schema.json in this hub)", family, family)
		return families
	}
	schema := r.loadJSON(schemaFile)
	dataFile := filepath.Join(folder, "design.json")
	b, err := os.ReadFile(dataFile)
	if err != nil {
		r.errorf(dataFile, "missing: a Design's values live here (start from templates/design)")
		return families
	}
	var data any
	if err := json.Unmarshal(b, &data); err != nil {
		r.errorf(dataFile, "not valid JSON (%v)", err)
		return families
	}
	if schema != nil {
		for _, problem := range validateSchema(data, schema, schema, "design.json") {
			r.errorf(dataFile, "%s", problem)
		}
	}
	doc, _ := data.(map[string]any)
	tokens, _ := doc["tokens"].(map[string]any)
	typeTokens, _ := tokens["type"].(map[string]any)
	for _, f := range strs(typeTokens, "files") {
		if strings.Contains(f, "..") || !exists(filepath.Join(folder, f)) {
			r.errorf(dataFile, "type.files names %s, which is not in the Design's folder", f)
		}
	}
	colour, _ := tokens["colour"].(map[string]any)
	roles, _ := colour["roles"].(map[string]any)
	for _, scheme := range sortedKeysAny(roles) {
		set, _ := roles[scheme].(map[string]any)
		for _, pair := range designPairs {
			ink, ground := str(set, pair.ink), str(set, pair.ground)
			if ink == "" || ground == "" {
				continue
			}
			if c, ok := contrastOf(ink, ground); ok && c < pair.need {
				r.errorf(dataFile, "%s: %s on %s reads %.2f:1, it needs %.1f:1", scheme, pair.ink, pair.ground, c, pair.need)
			}
		}
	}
	return families
}

func luminanceOf(hex string) (float64, bool) {
	if !hexRE.MatchString(hex) {
		return 0, false
	}
	var rgb [3]float64
	for i := range rgb {
		var v int
		fmt.Sscanf(hex[1+2*i:3+2*i], "%x", &v)
		c := float64(v) / 255
		if c <= 0.04045 {
			rgb[i] = c / 12.92
		} else {
			rgb[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*rgb[0] + 0.7152*rgb[1] + 0.0722*rgb[2], true
}

func contrastOf(a, b string) (float64, bool) {
	la, okA := luminanceOf(a)
	lb, okB := luminanceOf(b)
	if !okA || !okB {
		return 0, false
	}
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05), true
}

// validateSchema checks the subset of JSON Schema a family's design.schema.json uses (type, enum, pattern, maxLength,
// minimum, maximum, items, minItems, maxItems, uniqueItems, properties, additionalProperties false, required, anyOf
// and $ref into $defs). The devkit reads the same file with the same rules.
func validateSchema(value any, schema, root map[string]any, at string) []string {
	for ref, ok := schema["$ref"].(string); ok; ref, ok = schema["$ref"].(string) {
		node := any(root)
		for _, key := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			m, _ := node.(map[string]any)
			node = m[key]
		}
		next, isMap := node.(map[string]any)
		if !isMap {
			return []string{fmt.Sprintf("%s: the schema's %s is missing", at, ref)}
		}
		schema = next
	}
	if options, ok := schema["anyOf"].([]any); ok {
		for _, o := range options {
			if m, isMap := o.(map[string]any); isMap && len(validateSchema(value, m, root, at)) == 0 {
				return nil
			}
		}
		return []string{fmt.Sprintf("%s: %s fits none of the allowed forms", at, jsonText(value))}
	}
	if kind, ok := schema["type"].(string); ok && !isKind(value, kind) {
		return []string{fmt.Sprintf("%s: must be a %s", at, kind)}
	}
	var out []string
	if enum, ok := schema["enum"].([]any); ok && !slices.ContainsFunc(enum, func(e any) bool { return jsonText(e) == jsonText(value) }) {
		out = append(out, fmt.Sprintf("%s: %s is not one of %s", at, jsonText(value), jsonText(enum)))
	}
	switch v := value.(type) {
	case string:
		if p, ok := schema["pattern"].(string); ok {
			if re, err := regexp.Compile(p); err == nil && !re.MatchString(v) {
				out = append(out, fmt.Sprintf("%s: %q does not match %s", at, v, p))
			}
		}
		if n, ok := schema["maxLength"].(float64); ok && float64(len([]rune(v))) > n {
			out = append(out, fmt.Sprintf("%s: longer than %d", at, int(n)))
		}
	case float64:
		if n, ok := schema["minimum"].(float64); ok && v < n {
			out = append(out, fmt.Sprintf("%s: below %v", at, n))
		}
		if n, ok := schema["maximum"].(float64); ok && v > n {
			out = append(out, fmt.Sprintf("%s: above %v", at, n))
		}
	case []any:
		if n, ok := schema["minItems"].(float64); ok && float64(len(v)) < n {
			out = append(out, fmt.Sprintf("%s: needs at least %d items", at, int(n)))
		}
		if n, ok := schema["maxItems"].(float64); ok && float64(len(v)) > n {
			out = append(out, fmt.Sprintf("%s: takes at most %d items", at, int(n)))
		}
		if unique, _ := schema["uniqueItems"].(bool); unique {
			seen := map[string]bool{}
			for _, item := range v {
				if seen[jsonText(item)] {
					out = append(out, fmt.Sprintf("%s: repeats %s", at, jsonText(item)))
				}
				seen[jsonText(item)] = true
			}
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range v {
				out = append(out, validateSchema(item, items, root, fmt.Sprintf("%s[%d]", at, i))...)
			}
		}
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		for _, key := range strs(schema, "required") {
			if _, ok := v[key]; !ok {
				out = append(out, fmt.Sprintf("%s: needs %q", at, key))
			}
		}
		closed := schema["additionalProperties"] == false
		for _, key := range sortedKeysAny(v) {
			if sub, ok := props[key].(map[string]any); ok {
				out = append(out, validateSchema(v[key], sub, root, at+"."+key)...)
			} else if closed {
				out = append(out, fmt.Sprintf("%s: %q is not a value the family reads", at, key))
			}
		}
	}
	return out
}

func isKind(v any, kind string) bool {
	switch kind {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f)
	}
	return true
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
