package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// The catalog resolution is a port of js/catalog.js, which works on whatever JSON.parse gives it. Go's maps lose key
// order, and the order of keys decides things here: matchReleaseFiles walks a release source's "files" patterns in
// key order (so it decides file order within a version), and a firmware's inline "version" object gives the version
// order. So the catalog is decoded into jsValue, a JSON tree that keeps keys the way a JavaScript object does.

// jsKind is the type of a JSON value.
type jsKind uint8

const (
	kNull jsKind = iota
	kBool
	kNumber
	kString
	kArray
	kObject
)

// jsValue is one JSON value. A nil *jsValue stands for JavaScript's undefined: a key that isn't there.
type jsValue struct {
	kind jsKind
	b    bool
	s    string // a string's text, or a number's literal as written
	arr  []*jsValue
	obj  *jsObject
}

// jsObject is a JSON object with JavaScript's key order: keys that are array indices ("0", "17") come first in
// numeric order, then the other keys in the order they were first added (ECMAScript OrdinaryOwnPropertyKeys). A
// repeated key keeps its first place and its last value, as JSON.parse does. Most catalog objects have a handful of
// keys, so they are kept as two slices and searched in order; a map is added once an object grows big.
type jsObject struct {
	keys  []string
	vals  []*jsValue
	index map[string]int // key → place, only for objects of indexFrom keys or more
}

const indexFrom = 12

func newObject() *jsObject { return &jsObject{} }

func (o *jsObject) find(key string) int {
	if o.index != nil {
		if i, ok := o.index[key]; ok {
			return i
		}
		return -1
	}
	for i, k := range o.keys {
		if k == key {
			return i
		}
	}
	return -1
}

func (o *jsObject) get(key string) *jsValue {
	if o == nil {
		return nil
	}
	if i := o.find(key); i >= 0 {
		return o.vals[i]
	}
	return nil
}

// set replaces the value of an existing key in place, or adds the key at the end.
func (o *jsObject) set(key string, v *jsValue) {
	if i := o.find(key); i >= 0 {
		o.vals[i] = v
		return
	}
	o.keys = append(o.keys, key)
	o.vals = append(o.vals, v)
	if o.index != nil {
		o.index[key] = len(o.keys) - 1
	} else if len(o.keys) >= indexFrom {
		o.reindex()
	}
}

func (o *jsObject) del(key string) {
	i := o.find(key)
	if i < 0 {
		return
	}
	o.keys = append(o.keys[:i], o.keys[i+1:]...)
	o.vals = append(o.vals[:i], o.vals[i+1:]...)
	if o.index != nil {
		o.reindex()
	}
}

func (o *jsObject) reindex() {
	o.index = make(map[string]int, len(o.keys))
	for i, k := range o.keys {
		o.index[k] = i
	}
}

// ordered is Object.keys(): array-index keys ascending, then the rest in insertion order.
func (o *jsObject) ordered() []string {
	if o == nil {
		return nil
	}
	var idx, rest []string
	rest = make([]string, 0, len(o.keys))
	for _, k := range o.keys {
		if isArrayIndex(k) {
			idx = append(idx, k)
		} else {
			rest = append(rest, k)
		}
	}
	if len(idx) == 0 {
		return rest
	}
	sort.Slice(idx, func(i, j int) bool {
		a, _ := strconv.ParseUint(idx[i], 10, 32)
		b, _ := strconv.ParseUint(idx[j], 10, 32)
		return a < b
	})
	return append(idx, rest...)
}

// isArrayIndex says whether a property key is an ECMAScript array index: the canonical decimal form of an integer
// from 0 to 2^32-2. Version names like "v1.18.0" or "1.17.1" aren't, so they keep their order; a bare "2" would not.
func isArrayIndex(k string) bool {
	if k == "" || len(k) > 10 || (len(k) > 1 && k[0] == '0') {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < '0' || k[i] > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(k, 10, 64)
	return err == nil && n < math.MaxUint32
}

// parseJS decodes JSON as the browser's Response.json() does: a leading UTF-8 byte order mark is dropped (the
// TextDecoder does that), and anything after the value is an error. encoding/json checks the grammar first
// (json.Valid), so the walk below only has to split up text it knows is valid; strings with escapes or non-ASCII
// bytes are decoded by encoding/json itself, so their text is exactly what it gives (invalid UTF-8 becomes U+FFFD).
func parseJS(data []byte) (*jsValue, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !json.Valid(data) {
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return nil, errors.New("invalid JSON")
	}
	p := jsonWalker{data: data}
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	return v, nil
}

// jsonWalker splits up valid JSON into jsValues.
type jsonWalker struct {
	data []byte
	i    int
}

func (p *jsonWalker) space() {
	for p.i < len(p.data) {
		switch p.data[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *jsonWalker) value() (*jsValue, error) {
	p.space()
	if p.i >= len(p.data) {
		return nil, errors.New("invalid JSON: empty")
	}
	switch c := p.data[p.i]; c {
	case '{':
		p.i++
		v := &jsValue{kind: kObject, obj: newObject()}
		for {
			p.space()
			if p.data[p.i] == '}' {
				p.i++
				return v, nil
			}
			if p.data[p.i] == ',' {
				p.i++
				p.space()
			}
			key, err := p.str()
			if err != nil {
				return nil, err
			}
			p.space()
			p.i++ // ':'
			e, err := p.value()
			if err != nil {
				return nil, err
			}
			v.obj.set(key, e)
		}
	case '[':
		p.i++
		v := &jsValue{kind: kArray}
		for {
			p.space()
			if p.data[p.i] == ']' {
				p.i++
				return v, nil
			}
			if p.data[p.i] == ',' {
				p.i++
			}
			e, err := p.value()
			if err != nil {
				return nil, err
			}
			v.arr = append(v.arr, e)
		}
	case '"':
		s, err := p.str()
		if err != nil {
			return nil, err
		}
		return &jsValue{kind: kString, s: s}, nil
	case 't':
		p.i += 4
		return &jsValue{kind: kBool, b: true}, nil
	case 'f':
		p.i += 5
		return &jsValue{kind: kBool}, nil
	case 'n':
		p.i += 4
		return &jsValue{kind: kNull}, nil
	default: // a number
		start := p.i
		for p.i < len(p.data) && strings.IndexByte("+-.0123456789eE", p.data[p.i]) >= 0 {
			p.i++
		}
		return &jsValue{kind: kNumber, s: string(p.data[start:p.i])}, nil
	}
}

// str reads a string at p.i (its opening quote).
func (p *jsonWalker) str() (string, error) {
	start := p.i
	plain := true
	p.i++
	for {
		c := p.data[p.i]
		if c == '"' {
			break
		}
		if c == '\\' {
			plain = false
			p.i++ // skip the escaped character, which may be a quote
		} else if c >= 0x80 {
			plain = false
		}
		p.i++
	}
	p.i++
	raw := p.data[start:p.i]
	if plain {
		return string(raw[1 : len(raw)-1]), nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}
	return s, nil
}

// field is v[key] for an object, and undefined (nil) for anything else, as a property read on a JSON value is
// (strings and arrays have no properties the catalog uses).
func (v *jsValue) field(key string) *jsValue {
	if v == nil || v.kind != kObject {
		return nil
	}
	return v.obj.get(key)
}

// nullish is JavaScript's "== null": undefined or null.
func (v *jsValue) nullish() bool { return v == nil || v.kind == kNull }

// truthy is JavaScript's ToBoolean.
func (v *jsValue) truthy() bool {
	if v == nil {
		return false
	}
	switch v.kind {
	case kNull:
		return false
	case kBool:
		return v.b
	case kNumber:
		f, err := strconv.ParseFloat(v.s, 64)
		return err == nil && f != 0
	case kString:
		return v.s != ""
	}
	return true
}

// str is the value's text when it is a JSON string, and "" otherwise: what the app model uses.
func (v *jsValue) str() string {
	if v == nil || v.kind != kString {
		return ""
	}
	return v.s
}

// jsString is JavaScript's ToString, as a template literal or a property key applies it: undefined becomes
// "undefined", an array its items joined by commas, an object "[object Object]".
func jsString(v *jsValue) string {
	if v == nil {
		return "undefined"
	}
	switch v.kind {
	case kNull:
		return "null"
	case kBool:
		if v.b {
			return "true"
		}
		return "false"
	case kNumber:
		f, err := strconv.ParseFloat(v.s, 64)
		if err != nil {
			return v.s
		}
		return jsNumberString(f)
	case kString:
		return v.s
	case kArray:
		parts := make([]string, len(v.arr))
		for i, e := range v.arr {
			if !e.nullish() {
				parts[i] = jsString(e)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// jsNumberString is Number.prototype.toString for a finite double: plain digits from 1e-6 up to 1e21, exponent form
// ("1e+21", "1e-7") outside that.
func jsNumberString(f float64) string {
	if f == 0 {
		return "0" // also -0
	}
	if a := math.Abs(f); a >= 1e-6 && a < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	mant, exp, _ := strings.Cut(s, "e")
	sign := exp[:1]
	exp = strings.TrimLeft(exp[1:], "0")
	return mant + "e" + sign + exp
}

// strictEqual is JavaScript's ===: undefined equals only undefined, numbers compare by value, and two objects or
// arrays from JSON are never the same object.
func strictEqual(a, b *jsValue) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case kNull:
		return true
	case kBool:
		return a.b == b.b
	case kNumber:
		x, err1 := strconv.ParseFloat(a.s, 64)
		y, err2 := strconv.ParseFloat(b.s, 64)
		return err1 == nil && err2 == nil && x == y
	case kString:
		return a.s == b.s
	}
	return a == b
}

func jsStr(s string) *jsValue { return &jsValue{kind: kString, s: s} }
