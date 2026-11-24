package catalog

// Ported from the MeshCore web flasher, flasher.meshcore.dev @ 45a5257, js/catalog.js (loadCatalog,
// matchReleaseFiles, releaseSourceKey, hasVersions, roleValue, renderNotice, firmwareUrl).
// MIT License, Copyright (c) 2025 Rastislav Vysoky (see THIRD_PARTY_NOTICES).

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// releaseSourceKey is the first key of a firmware entry, in Object.keys order, that starts with "github": the
// release list ("/releases?repo=<key>") its versions come from. "" when it has none (inline versions only).
func releaseSourceKey(fw *jsValue) string {
	if fw == nil || fw.kind != kObject {
		return ""
	}
	for _, k := range fw.obj.ordered() {
		if strings.HasPrefix(k, "github") {
			return k
		}
	}
	return ""
}

// catalogFirmware is config.device.flatMap(device => device.firmware). Where the JavaScript would throw on a
// malformed catalog (no device list, a device that isn't an object, a firmware list that isn't an array), this
// returns an error, as loadCatalog then fails and the web flasher shows nothing.
func catalogFirmware(cfg *jsValue) ([]*jsValue, error) {
	devs := cfg.field("device")
	if devs == nil || devs.kind != kArray {
		return nil, fmt.Errorf("the catalog has no device list")
	}
	var all []*jsValue
	for i, d := range devs.arr {
		if d.nullish() || d.kind != kObject {
			return nil, fmt.Errorf("device %d is not an object", i)
		}
		fws := d.field("firmware")
		if fws == nil || fws.kind != kArray {
			return nil, fmt.Errorf("device %d (%s) has no firmware list", i, jsString(d.field("name")))
		}
		for j, fw := range fws.arr {
			// JavaScript skips a number or a string here and throws on null; meshflash refuses any of them.
			if fw.kind != kObject {
				return nil, fmt.Errorf("device %d (%s) firmware %d is not an object", i, jsString(d.field("name")), j)
			}
			all = append(all, fw)
		}
	}
	return all, nil
}

// releaseKeys lists the release sources a catalog names, in the order loadCatalog first meets them: the
// "/releases?repo=<key>" requests to make.
func releaseKeys(cfg *jsValue) ([]string, error) {
	fws, err := catalogFirmware(cfg)
	if err != nil {
		return nil, err
	}
	var keys []string
	seen := map[string]bool{}
	for _, fw := range fws {
		if k := releaseSourceKey(fw); k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return keys, nil
}

// resolveTree is loadCatalog after the fetches: every firmware entry with a release source gets its "version"
// object rebuilt from the releases (matchReleaseFiles), then every device none of whose firmware has a version with
// a file is dropped. It changes cfg in place, as the JavaScript changes the parsed config.
func resolveTree(cfg *jsValue, releases map[string]*jsValue) error {
	fws, err := catalogFirmware(cfg)
	if err != nil {
		return err
	}
	prepared := map[string]*releaseIndex{}
	for _, fw := range fws {
		key := releaseSourceKey(fw)
		if key == "" {
			continue
		}
		source := fw.field(key)
		if !source.field("files").truthy() { // firmware[key]?.files
			continue
		}
		list, ok := prepared[key]
		if !ok {
			v, ok := releases[key]
			if !ok {
				return fmt.Errorf("no release list for %q", key)
			}
			list = prepareReleases(v)
			prepared[key] = list
		}
		versions, err := matchReleaseFiles(list, source, key)
		if err != nil {
			return fmt.Errorf("release source %q: %w", key, err)
		}
		fw.obj.set("version", versions)
	}

	devs := cfg.field("device")
	kept := devs.arr[:0:0]
	for _, d := range devs.arr {
		keep := false
		for _, fw := range d.field("firmware").arr {
			has, err := hasVersions(fw)
			if err != nil {
				return fmt.Errorf("device %s: %w", jsString(d.field("name")), err)
			}
			if has {
				keep = true
				break
			}
		}
		if keep {
			kept = append(kept, d)
		}
	}
	devs.arr = kept
	return nil
}

// matchReleaseFiles builds a firmware's versions from a release list, exactly as js/catalog.js:26-47 does:
//
//	for each [fileType, regex] of source.files, in key order       (outer loop: patterns)
//	  for each release, in list order (newest first)                (inner loop: releases)
//	    skip it unless release.type === source.type
//	    versions[release.version] ??= {notes: release.notes, files: []}
//	    push {type: fileType, name: file.url + "?repo=" + key, title: file.name} for every file whose name the regex
//	    finds (unanchored, case-sensitive, like RegExp.test)
//	then delete the versions with no files.
//
// So versions come in release-list order, and within a version the files come in pattern order, then file order.
// The regexes are compiled with Go's RE2, which gives the same matches for every pattern the catalogs use (no
// look-arounds, back-references or anchors; docs/meshcore-flashing.md 3.4 checked 4041 pattern/file pairs). A
// pattern RE2 can't compile, or one it would read differently from JavaScript (checkJSPattern), is an error, so the
// update is refused and the saved catalog stays.
func matchReleaseFiles(releases *releaseIndex, source *jsValue, key string) (*jsValue, error) {
	files := source.field("files")
	if files.kind != kObject {
		return nil, fmt.Errorf("its files patterns are not an object")
	}
	type pattern struct {
		fileType string
		re       *regexp.Regexp
		// prefix is the literal every match starts with, so a name without it can't match: a quick test that
		// skips most of the list (a board's pattern starts with the board's name). The regex still decides:
		// LiteralPrefix's "complete" can't stand in for it, since RE2 calls "a" complete for ^a$, which "xa"
		// contains but doesn't match.
		prefix string
	}
	var patterns []pattern
	for _, t := range files.obj.ordered() {
		src := jsString(files.obj.get(t)) // new RegExp(re) takes the string form
		if err := checkJSPattern(src); err != nil {
			return nil, fmt.Errorf("pattern %q for %s: %v", src, t, err)
		}
		re, err := regexp.Compile(src)
		if err != nil {
			return nil, fmt.Errorf("pattern %q for %s does not compile: %v", src, t, err)
		}
		prefix, _ := re.LiteralPrefix()
		patterns = append(patterns, pattern{t, re, prefix})
	}

	versions := newObject()
	sourceType := source.field("type")
	for _, p := range patterns {
		if releases.list.kind != kArray {
			return nil, fmt.Errorf("the release list is not an array")
		}
		for i := range releases.releases {
			rel := &releases.releases[i]
			if rel.null {
				return nil, fmt.Errorf("a release is null")
			}
			if !strictEqual(rel.typ, sourceType) {
				continue
			}
			if rel.version == "__proto__" {
				// versions["__proto__"] is Object.prototype in JavaScript, and pushing to its files throws.
				return nil, fmt.Errorf("a release is named __proto__")
			}
			version := versions.get(rel.version)
			if version == nil {
				version = &jsValue{kind: kObject, obj: newObject()}
				// {notes: release.notes, files: []}: an undefined notes is left out, as JSON.stringify leaves it out.
				if rel.notes != nil {
					version.obj.set("notes", rel.notes)
				}
				version.obj.set("files", &jsValue{kind: kArray})
				versions.set(rel.version, version)
			}
			if rel.noFiles {
				return nil, fmt.Errorf("release %s has no file list", rel.version)
			}
			out := version.obj.get("files")
			rel.candidates(p.prefix, func(f *preparedFile) {
				if !p.re.MatchString(f.name) {
					return
				}
				entry := newObject()
				entry.set("type", jsStr(p.fileType))
				entry.set("name", jsStr(f.url+"?repo="+key))
				if f.title != nil {
					entry.set("title", f.title)
				}
				out.arr = append(out.arr, &jsValue{kind: kObject, obj: entry})
			})
			if rel.nullFile {
				return nil, fmt.Errorf("release %s lists a null file", rel.version)
			}
		}
	}

	for _, name := range versions.ordered() {
		if len(versions.get(name).obj.get("files").arr) == 0 {
			versions.del(name)
		}
	}
	return &jsValue{kind: kObject, obj: versions}, nil
}

// posixClass is a POSIX class inside a bracket expression ("[:alpha:]"), at the '[' that starts it.
var posixClass = regexp.MustCompile(`^\[:\^?[A-Za-z]+:\]`)

// checkJSPattern refuses the regex syntax that Go's RE2 compiles with another meaning than JavaScript's RegExp has
// (no flags, the non-Unicode mode new RegExp(re) uses), so such a catalog is refused rather than matched differently
// from the web flasher. Running both over the same patterns showed these (none is in the saved catalogs):
//
//   - \a is a bell in Go and the letter a in JavaScript; \A and \z are text anchors in Go and the letters in
//     JavaScript; \Q…\E quotes in Go; \p and \P are Unicode classes in Go and the letters in JavaScript; \x{…} is a
//     code point in Go and "x" repeated in JavaScript; \1…\9 are octal or an error in Go and back-references in
//     JavaScript (\C too is refused, an error in Go and a C in JavaScript).
//   - [[:alpha:]] is a POSIX class in Go and a set of characters followed by "]" in JavaScript; []a] and [^]a] put "]"
//     in the set in Go and are an empty or a full set in JavaScript.
//   - A flag group such as (?i) or (?P<name>…) compiles in Go and is a SyntaxError in JavaScript (which stops the web
//     flasher's whole catalog). (?:…) and (?<name>…) mean the same in both; RE2 refuses look-arounds itself.
//
// They still differ where no file name goes: JavaScript's "." also refuses \r,   and  , its \s also takes
// \v and Unicode spaces, and it counts a character outside the BMP as two. Catalog file names are plain ASCII.
func checkJSPattern(re string) error {
	inClass := false
	classStart := 0 // where a bracket expression's members start: after "[" or "[^"
	for i := 0; i < len(re); i++ {
		c := re[i]
		switch {
		case c == '\\':
			if i+1 == len(re) {
				return nil // a trailing backslash: RE2 refuses it, as JavaScript does
			}
			n := re[i+1]
			if strings.IndexByte("aAzQECpP123456789", n) >= 0 || (n == 'x' && i+2 < len(re) && re[i+2] == '{') {
				return fmt.Errorf(`\%c means something else to Go's regexp than to JavaScript's`, n)
			}
			i++ // the escaped character is never syntax
		case inClass:
			switch {
			case c == ']' && i == classStart:
				return errors.New(`a set starting with "]" means something else to Go's regexp than to JavaScript's`)
			case c == ']':
				inClass = false
			case c == '[' && posixClass.MatchString(re[i:]):
				return errors.New("a POSIX class ([:name:]) means something else to Go's regexp than to JavaScript's")
			}
		case c == '[':
			inClass = true
			classStart = i + 1
			if classStart < len(re) && re[classStart] == '^' {
				classStart++
			}
		case c == '(' && i+1 < len(re) && re[i+1] == '?':
			if i+2 < len(re) && strings.IndexByte(":<=!", re[i+2]) >= 0 {
				continue
			}
			return errors.New("a flag group (?…) is not JavaScript")
		}
	}
	return nil
}

// releaseIndex is a release list with each release's fields read once: matchReleaseFiles runs over the same list for
// every firmware entry and every pattern, and reading the fields each time was most of the work.
type releaseIndex struct {
	list     *jsValue
	releases []preparedRelease
}

type preparedRelease struct {
	null     bool     // the release is null (an error once reached)
	typ      *jsValue // release.type
	version  string   // String(release.version)
	notes    *jsValue
	noFiles  bool // release.files isn't an array (an error once a release of the right type is reached)
	nullFile bool // a file after files[len(files)-1] is null (an error once reached)
	files    []preparedFile

	// names is every file name joined by "\n", and starts where each begins in it, so one strings.Index pass finds
	// the files containing a pattern's literal prefix. joined is false when a name itself holds a "\n".
	names  string
	starts []int
	joined bool
}

// candidates calls f, in file order, for every file whose name contains prefix: the only ones a pattern starting
// with that literal can match.
func (r *preparedRelease) candidates(prefix string, f func(*preparedFile)) {
	if prefix == "" || !r.joined || strings.Contains(prefix, "\n") {
		for i := range r.files {
			if strings.Contains(r.files[i].name, prefix) {
				f(&r.files[i])
			}
		}
		return
	}
	for pos := 0; pos < len(r.names); {
		at := strings.Index(r.names[pos:], prefix)
		if at < 0 {
			return
		}
		at += pos
		// The file holding the match: the last one starting at or before it.
		i := sort.SearchInts(r.starts, at+1) - 1
		f(&r.files[i])
		if i+1 < len(r.starts) {
			pos = r.starts[i+1]
		} else {
			return
		}
	}
}

type preparedFile struct {
	name  string   // String(file.name), what the regex tests
	title *jsValue // file.name as it is
	url   string   // String(file.url)
}

func prepareReleases(list *jsValue) *releaseIndex {
	ri := &releaseIndex{list: list}
	if list == nil || list.kind != kArray {
		return ri
	}
	ri.releases = make([]preparedRelease, len(list.arr))
	for i, rel := range list.arr {
		p := &ri.releases[i]
		if rel.nullish() {
			p.null = true
			continue
		}
		p.typ = rel.field("type")
		p.version = jsString(rel.field("version"))
		p.notes = rel.field("notes")
		rf := rel.field("files")
		if rf == nil || rf.kind != kArray {
			p.noFiles = true
			continue
		}
		var names strings.Builder
		p.joined = true
		for _, f := range rf.arr {
			if f.nullish() {
				p.nullFile = true
				break
			}
			name := f.field("name")
			pf := preparedFile{name: jsString(name), title: name, url: jsString(f.field("url"))}
			p.files = append(p.files, pf)
			if strings.Contains(pf.name, "\n") {
				p.joined = false
			}
			p.starts = append(p.starts, names.Len())
			names.WriteString(pf.name)
			names.WriteByte('\n')
		}
		p.names = names.String()
	}
	return ri
}

// hasVersions is js/catalog.js:49: some version of the firmware has at least one file. Like the JavaScript's
// Object.values(...).some(...), it stops at the first version with files, and a version without a file list that it
// reaches is an error (the JavaScript throws reading .files.length).
func hasVersions(fw *jsValue) (bool, error) {
	v := fw.field("version")
	if v.nullish() {
		return false, nil
	}
	var values []*jsValue
	switch v.kind {
	case kObject:
		for _, k := range v.obj.ordered() {
			values = append(values, v.obj.get(k))
		}
	case kArray:
		values = v.arr
	case kString:
		if v.s != "" {
			return false, fmt.Errorf("a firmware's version is a string")
		}
	}
	for _, ver := range values {
		files := ver.field("files")
		if files == nil || files.kind != kArray {
			return false, fmt.Errorf("a version has no file list")
		}
		if len(files.arr) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// roleValue is js/catalog.js:72: the firmware's own title (or subTitle, icon, tooltip), else its role's, else "".
func roleValue(cfg, fw *jsValue, key string) string {
	if v := fw.field(key); !v.nullish() {
		return jsString(v)
	}
	if v := cfg.field("role").field(jsString(fw.field("role"))).field(key); !v.nullish() {
		return jsString(v)
	}
	return ""
}

var noticeField = regexp.MustCompile(`\$\{(\w+)\}`)

// renderNotice is js/catalog.js:75-82 applied to one notice value: a key of the catalog's "notice" table, or the
// notice's own HTML when it isn't one; then every ${field} becomes that device field (an array's first item), or ""
// when it's empty. It returns HTML; noticeText makes it plain text.
func renderNotice(cfg, device, notice *jsValue) string {
	var html string
	if n := cfg.field("notice").field(jsString(notice)); n.truthy() {
		html = jsString(n)
	} else if notice.truthy() {
		html = jsString(notice)
	}
	return noticeField.ReplaceAllStringFunc(html, func(m string) string {
		v := device.field(m[2 : len(m)-1])
		if v != nil && v.kind == kArray {
			if len(v.arr) == 0 {
				return ""
			}
			v = v.arr[0]
		}
		if !v.truthy() {
			return ""
		}
		return jsString(v)
	})
}
