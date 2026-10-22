package main

// A small ordered JSON object, so the manifest keeps its key order when it is
// rewritten (encoding/json sorts map keys).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// object is a JSON object whose keys keep their order.
type object struct {
	keys   []string
	values map[string]any
}

func newObject() *object { return &object{values: map[string]any{}} }

func (o *object) get(key string) any { return o.values[key] }

func (o *object) has(key string) bool {
	_, ok := o.values[key]
	return ok
}

// set adds or replaces a key; a new key goes at the end.
func (o *object) set(key string, v any) *object {
	if !o.has(key) {
		o.keys = append(o.keys, key)
	}
	o.values[key] = v
	return o
}

// child returns the object at key, or nil.
func (o *object) child(key string) *object {
	if o == nil {
		return nil
	}
	c, _ := o.values[key].(*object)
	return c
}

// parse decodes JSON, with objects as *object and numbers as json.Number.
func parse(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := newObject()
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				o.set(k.(string), v)
			}
			_, err := dec.Token()
			return o, err
		case '[':
			list := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				list = append(list, v)
			}
			_, err := dec.Token()
			return list, err
		}
		return nil, fmt.Errorf("unexpected %v", t)
	}
	return tok, nil
}

// format writes v as JSON indented with two spaces, like json.MarshalIndent but
// keeping object key order and without escaping <, > and &.
func format(v any) string {
	var b strings.Builder
	write(&b, v, "")
	return b.String()
}

func write(b *strings.Builder, v any, indent string) {
	inner := indent + "  "
	switch x := v.(type) {
	case *object:
		if len(x.keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, k := range x.keys {
			b.WriteString(inner)
			b.WriteString(scalar(k))
			b.WriteString(": ")
			write(b, x.values[k], inner)
			if i < len(x.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "}")
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, e := range x {
			b.WriteString(inner)
			write(b, e, inner)
			if i < len(x)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "]")
	default:
		b.WriteString(scalar(x))
	}
}

func scalar(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "null"
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	}
	return strings.Trim(scalar(v), `"`)
}

func strings2any(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}
