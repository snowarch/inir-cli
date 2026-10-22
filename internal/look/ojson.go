package look

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// Object is a JSON object that keeps its keys in file order, so a config
// written back reads like the one that was read (numbers keep their spelling
// too: 1.0 stays 1.0).
type Object struct {
	keys []string
	vals map[string]any
}

func NewObject() *Object { return &Object{vals: map[string]any{}} }

func (o *Object) Keys() []string {
	if o == nil {
		return nil
	}
	return o.keys
}

func (o *Object) Len() int { return len(o.Keys()) }

func (o *Object) Get(k string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.vals[k]
	return v, ok
}

func (o *Object) Set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

// At follows a path of keys.
func (o *Object) At(path ...string) (any, bool) {
	var v any = o
	for _, k := range path {
		obj, ok := v.(*Object)
		if !ok {
			return nil, false
		}
		if v, ok = obj.Get(k); !ok {
			return nil, false
		}
	}
	return v, true
}

// Parse reads JSON into *Object, []any, string, json.Number, bool and nil.
func Parse(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, err := parseValue(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("more after the JSON value")
	}
	return v, nil
}

func parseValue(d *json.Decoder) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('{'):
		o := NewObject()
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			v, err := parseValue(d)
			if err != nil {
				return nil, err
			}
			o.Set(k.(string), v)
		}
		_, err := d.Token()
		return o, err
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, err := parseValue(d)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err := d.Token()
		return a, err
	}
	if _, ok := t.(json.Delim); ok {
		return nil, fmt.Errorf("unexpected %v", t)
	}
	return t, nil
}

// Format writes v indented by four spaces, the way the shell writes its config.
func Format(v any) []byte {
	var b bytes.Buffer
	write(&b, v, "")
	b.WriteByte('\n')
	return b.Bytes()
}

// Compact is v on one line.
func Compact(v any) string {
	var b bytes.Buffer
	write(&b, v, "")
	var out bytes.Buffer
	if json.Compact(&out, b.Bytes()) != nil {
		return b.String()
	}
	return out.String()
}

func write(b *bytes.Buffer, v any, ind string) {
	switch v := v.(type) {
	case *Object:
		if v.Len() == 0 {
			b.WriteString("{\n" + ind + "}") // Qt's spelling, so a rewrite diffs quietly
			return
		}
		b.WriteString("{\n")
		for i, k := range v.keys {
			b.WriteString(ind + "    ")
			writeString(b, k)
			b.WriteString(": ")
			write(b, v.vals[k], ind+"    ")
			if i < len(v.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(ind + "}")
	case []any:
		if len(v) == 0 {
			b.WriteString("[\n" + ind + "]")
			return
		}
		b.WriteString("[\n")
		for i, e := range v {
			b.WriteString(ind + "    ")
			write(b, e, ind+"    ")
			if i < len(v)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(ind + "]")
	case string:
		writeString(b, v)
	case json.Number:
		b.WriteString(string(v))
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case nil:
		b.WriteString("null")
	default:
		data, _ := json.Marshal(v)
		b.Write(data)
	}
}

func writeString(b *bytes.Buffer, s string) {
	e := json.NewEncoder(b)
	e.SetEscapeHTML(false)
	e.Encode(s)
	b.Truncate(b.Len() - 1)
}

// Equal compares JSON values; numbers by value (1 equals 1.0).
func Equal(a, b any) bool {
	switch a := a.(type) {
	case *Object:
		bo, ok := b.(*Object)
		if !ok || a.Len() != bo.Len() {
			return false
		}
		for _, k := range a.keys {
			bv, ok := bo.vals[k]
			if !ok || !Equal(a.vals[k], bv) {
				return false
			}
		}
		return true
	case []any:
		ba, ok := b.([]any)
		if !ok || len(a) != len(ba) {
			return false
		}
		for i := range a {
			if !Equal(a[i], ba[i]) {
				return false
			}
		}
		return true
	case json.Number:
		bn, ok := b.(json.Number)
		if !ok {
			return false
		}
		af, e1 := a.Float64()
		bf, e2 := bn.Float64()
		return a == bn || (e1 == nil && e2 == nil && af == bf)
	}
	return a == b
}

func kind(v any) string {
	switch v.(type) {
	case *Object:
		return "an object"
	case []any:
		return "a list"
	case string:
		return "text"
	case json.Number:
		return "a number"
	case bool:
		return "true or false"
	}
	return "null"
}
