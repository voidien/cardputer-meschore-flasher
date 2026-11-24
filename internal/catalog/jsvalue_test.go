package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// JSON.parse keeps a repeated key at its first place with its last value, and puts array-index keys first.
func TestParseJSKeyOrder(t *testing.T) {
	v, err := parseJS([]byte(`{"b":1,"a":2,"10":3,"2":4,"b":5,"v1.18.0":6,"01":7,"4294967295":8}`))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(v.obj.ordered(), " ")
	want := "2 10 b a v1.18.0 01 4294967295"
	if got != want {
		t.Fatalf("keys %q, want %q", got, want)
	}
	if b := v.obj.get("b"); b.s != "5" {
		t.Fatalf("repeated key kept %s, want the last value 5", b.s)
	}
	v.obj.set("a", jsStr("x")) // replaced in place
	v.obj.set("z", jsStr("y")) // added at the end
	v.obj.del("10")
	if got := strings.Join(v.obj.ordered(), " "); got != "2 b a v1.18.0 01 4294967295 z" {
		t.Fatalf("after set/del: %q", got)
	}
}

func TestParseJSInput(t *testing.T) {
	if _, err := parseJS([]byte("\xef\xbb\xbf{\"a\":1}")); err != nil {
		t.Fatalf("a byte order mark is dropped like the browser's TextDecoder does: %v", err)
	}
	for _, bad := range []string{``, `{`, `{"a":1} x`, `{"a":1}{}`, `[1,]`, `<!DOCTYPE html>`} {
		if _, err := parseJS([]byte(bad)); err == nil {
			t.Errorf("parseJS(%q) accepted", bad)
		}
	}
}

func TestJSString(t *testing.T) {
	cases := map[string]string{
		`"v1.18.0"`: "v1.18.0", `null`: "null", `true`: "true", `12`: "12", `1.50`: "1.5", `1e21`: "1e+21",
		`1e-7`: "1e-7", `0.000001`: "0.000001", `-0`: "0", `[1,null,"a"]`: "1,,a", `{}`: "[object Object]",
		`123456789012345680000`: "123456789012345680000",
	}
	for in, want := range cases {
		v, err := parseJS([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if got := jsString(v); got != want {
			t.Errorf("jsString(%s) = %q, want %q", in, got, want)
		}
	}
	if jsString(nil) != "undefined" {
		t.Error("undefined")
	}
}

func TestStrictEqual(t *testing.T) {
	p := func(s string) *jsValue {
		v, err := parseJS([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	obj := p(`{}`)
	for _, c := range []struct {
		a, b *jsValue
		want bool
	}{
		{nil, nil, true}, {nil, p(`null`), false}, {p(`null`), p(`null`), true}, {p(`"repeater"`), p(`"repeater"`), true},
		{p(`"1"`), p(`1`), false}, {p(`1.0`), p(`1`), true}, {p(`{}`), p(`{}`), false}, {obj, obj, true},
	} {
		if got := strictEqual(c.a, c.b); got != c.want {
			t.Errorf("strictEqual(%v, %v) = %v", c.a, c.b, got)
		}
	}
}

// matchReleaseFiles follows the JavaScript on the odd cases: a release with no type matches a source with no type
// (undefined === undefined), a missing version becomes "undefined", and versions keep release order except that
// array-index names go first, as in a JavaScript object.
func TestMatchReleaseFilesEdges(t *testing.T) {
	releases, err := parseJS([]byte(`[
		{"version":"v2","files":[{"name":"a-2.bin","url":"/r/a-2.bin"}]},
		{"version":"7","type":"t","files":[{"name":"a-7.bin","url":"/r/a-7.bin"}]},
		{"type":"t","notes":"n","files":[{"name":"a-u.bin","url":"/r/a-u.bin"},{"url":"/r/x"}]},
		{"version":"v1","type":"t","files":[{"name":"b.bin","url":"/r/b.bin"}]}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	run := func(source string) string {
		src, err := parseJS([]byte(source))
		if err != nil {
			t.Fatal(err)
		}
		v, err := matchReleaseFiles(prepareReleases(releases), src, "k")
		if err != nil {
			return "error: " + err.Error()
		}
		var parts []string
		for _, name := range v.obj.ordered() {
			for _, f := range v.obj.get(name).field("files").arr {
				parts = append(parts, fmt.Sprintf("%s:%s:%s", name, f.field("type").s, f.field("name").s))
			}
		}
		return strings.Join(parts, " ")
	}
	if got, want := run(`{"type":"t","files":{"u":"undefined","a":"a-"}}`),
		"7:a:/r/a-7.bin?repo=k undefined:u:/r/x?repo=k undefined:a:/r/a-u.bin?repo=k"; got != want {
		t.Errorf("typed source:\n got %s\nwant %s", got, want)
	}
	if got, want := run(`{"files":{"a":"a-"}}`), "v2:a:/r/a-2.bin?repo=k"; got != want {
		t.Errorf("untyped source: got %s, want %s", got, want)
	}
	if got := run(`{"type":"t","files":{"a":"(?=x)"}}`); !strings.HasPrefix(got, "error: pattern") {
		t.Errorf("a look-ahead (valid JavaScript, not RE2) must refuse the catalog, got %s", got)
	}
	if got := run(`{"type":"t","files":["a-"]}`); !strings.HasPrefix(got, "error:") {
		t.Errorf("patterns that aren't an object: %s", got)
	}
}

// Where js/catalog.js would throw, the catalog is refused: the update fails and the saved catalog stays.
func TestResolveRefusesBrokenCatalogs(t *testing.T) {
	for name, cfg := range map[string]string{
		"no devices":      `{"staticPath":"/firmware"}`,
		"device null":     `{"device":[null]}`,
		"no firmware":     `{"device":[{"name":"x"}]}`,
		"firmware null":   `{"device":[{"name":"x","firmware":[null]}]}`,
		"version no list": `{"device":[{"name":"x","firmware":[{"version":{"v1":{}}}]}]}`,
		"no releases":     `{"device":[{"name":"x","firmware":[{"github-x":{"type":"t","files":{"flash":"x"}}}]}]}`,
	} {
		if _, err := Resolve(Raw{Config: []byte(cfg)}, Raw{}, DefaultSites); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A release list that isn't one, or a release of the right type without files.
	cfg := []byte(`{"device":[{"name":"x","type":"esp32","firmware":[{"github":{"type":"t","files":{"flash":"x"}}}]}]}`)
	for name, rel := range map[string]string{
		"object":   `{}`,
		"null rel": `[null]`,
		"no files": `[{"version":"v1","type":"t"}]`,
		"bad json": `[`,
	} {
		_, err := Resolve(Raw{Config: cfg, Releases: map[string][]byte{"github": []byte(rel)}}, Raw{}, DefaultSites)
		if err == nil {
			t.Errorf("release list %s: accepted", name)
		}
	}
	// A release of another type is never read, as in the JavaScript.
	c, err := Resolve(Raw{Config: cfg, Releases: map[string][]byte{"github": []byte(
		`[{"version":"v1","type":"other"},{"version":"v1","type":"t","files":[{"name":"x.bin","url":"/x.bin"}]}]`)}},
		Raw{}, DefaultSites)
	if err != nil || len(c.Devices) != 1 {
		t.Fatalf("%v %v", c, err)
	}
}

// The walker gives what encoding/json gives (key order aside), on every committed input and golden and on tricky
// strings: the goldens are read with the same walker, so this is what keeps them honest.
func TestParseJSMatchesEncodingJSON(t *testing.T) {
	t.Parallel() // heavy, independent: the shared catalogs are resolved once and only read
	// %u stands for JSON's backslash-u escape, spelled out so no tool turns it into the character.
	esc := strings.NewReplacer("%u", "\\"+"u")
	inputs := map[string][]byte{
		"escapes": []byte(esc.Replace(`{"%u0041%u00e9%ud83d%ude00":"%u0032%u000a%u0022","%u0031%u0030":[1],"x":"%ud800"}`)),
		// JSON escapes (\" \\ \/ \n \t, \u with a surrogate pair), raw UTF-8, invalid UTF-8, a number in exponent
		// form, and a key that is an escaped array index.
		"tricky": []byte(`{"a\"b":"x\\y\/zé😀\n\t","2":[1,-0.5e+3,true,false,null,{}],` +
			`"` + "é" + `":"` + "\xff\xfe" + `","s":" spaced \" quote","e":""}`),
	}
	for _, name := range []string{"meshcore-config.json.gz", "meshcore-releases-github.json.gz",
		"meshcore-releases-github-ripple.json.gz", "zephcore-config.json.gz", "zephcore-releases-github-zephcore.json.gz",
		"git-config.json.gz", "golden-zephcore.json.gz", "edges-config.json.gz", "edges-releases-github.json.gz",
		"golden-edges.json.gz"} {
		inputs[name] = readGz(t, name)
	}
	for name, data := range inputs {
		v, err := parseJS(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		var want any
		if err := dec.Decode(&want); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := sameAsStd(v, want, name); err != nil {
			t.Error(err)
		}
	}
}

func sameAsStd(v *jsValue, want any, path string) error {
	switch w := want.(type) {
	case nil:
		if v.kind != kNull {
			return fmt.Errorf("%s: kind %d, want null", path, v.kind)
		}
	case bool:
		if v.kind != kBool || v.b != w {
			return fmt.Errorf("%s: want %v", path, w)
		}
	case json.Number:
		if v.kind != kNumber || v.s != w.String() {
			return fmt.Errorf("%s: %q, want number %s", path, v.s, w)
		}
	case string:
		if v.kind != kString || v.s != w {
			return fmt.Errorf("%s: %q, want %q", path, v.s, w)
		}
	case []any:
		if v.kind != kArray || len(v.arr) != len(w) {
			return fmt.Errorf("%s: want an array of %d", path, len(w))
		}
		for i := range w {
			if err := sameAsStd(v.arr[i], w[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case map[string]any:
		if v.kind != kObject || len(v.obj.keys) != len(w) {
			return fmt.Errorf("%s: want an object of %d keys", path, len(w))
		}
		for k, e := range w {
			if err := sameAsStd(v.obj.get(k), e, path+"."+k); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%s: %T", path, want)
	}
	return nil
}

// RE2 compiles some syntax with another meaning than JavaScript's RegExp gives it (each refused pattern here was run
// through both: Node 22 and Go matched different names, or Node threw). A catalog using one is refused, so meshflash
// never offers a file the web flasher wouldn't; every construct the saved catalogs use still passes.
func TestCheckJSPattern(t *testing.T) {
	t.Parallel() // heavy, independent: the shared catalogs are resolved once and only read
	for _, p := range []string{
		`\Aabc`, `abc\z`, `\Qa.c\E`, `\ab`, `a\Ab`, `\pL+c`, `\P{L}`, `\x{61}bc`, `(a)\1`, `\Cbc`,
		`[[:alpha:]]bc`, `[x[:^digit:]]`, `[]a]`, `[^]a]`, `(?i)abc`, `(?P<n>abc)`, `(?s:.)`, `(?-m)a`,
	} {
		if err := checkJSPattern(p); err == nil {
			t.Errorf("%q accepted", p)
		}
		src, _ := parseJS([]byte(`{"type":"t","files":{"flash":` + strconvQuote(p) + `}}`))
		rel, _ := parseJS([]byte(`[{"version":"v1","type":"t","files":[{"name":"abc","url":"/abc"}]}]`))
		if _, err := matchReleaseFiles(prepareReleases(rel), src, "github"); err == nil {
			t.Errorf("%q: matchReleaseFiles accepted it", p)
		}
	}
	for _, p := range []string{
		`Heltec_v3_[rR]epeater.*?-merged\.bin`, `Heltec_v3_[rR]epeater.*?-[a-f0-9]{7}\.bin`, `Ultra-TDeck-[^-]+\.bin`,
		`RAK_4631(_SX1276)?_[rR]oom_server.*?\.zip`, `(?:ProMicro|Faketec)_companion`, `^abc$`, `(?<n>abc)`, `\[:x:]`,
		`[\]]a`, `[a\-z]`, `a\.b\/c\_d`, `\d\w\s\b\B\t\n\x41\0`, `x]`, `a{,2}`, `[[]`, `(?:a)`,
	} {
		if err := checkJSPattern(p); err != nil {
			t.Errorf("%q refused: %v", p, err)
		}
	}
	// Every pattern in the saved catalogs passes: each golden case resolves (once, shared with the golden tests).
	for _, gc := range goldenCases {
		gc.resolved(t)
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
