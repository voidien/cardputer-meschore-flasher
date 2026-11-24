package catalog

import (
	"html"
	"strings"
)

// noticeText turns a rendered catalog notice (HTML) into plain text for a terminal screen: tags go, entities are
// decoded, and links go. A link's words stay where they are part of the sentence ("installing OTAFIX bootloader by
// @oltaco"), but its address is never shown, and a link that is only "Read more" is dropped whole, since without the
// address it asks for something the screen can't do. Block ends (</article>, </p>, <br>) become line breaks; spaces
// are tidied.
func noticeText(h string) string {
	var out strings.Builder
	var link strings.Builder // the words of the <a> being read
	inLink := false
	text := func(s string) {
		if inLink {
			link.WriteString(s)
		} else {
			out.WriteString(s)
		}
	}
	for i := 0; i < len(h); {
		if h[i] != '<' || !startsTag(h[i+1:]) {
			j := strings.IndexByte(h[i+1:], '<')
			if j < 0 {
				j = len(h)
			} else {
				j += i + 1
			}
			text(h[i:j])
			i = j
			continue
		}
		end := tagEnd(h, i)
		name, closing := tagName(h[i+1 : end])
		i = end + 1
		switch {
		case name == "a" && !closing:
			if inLink { // an unclosed <a>: keep what it had
				out.WriteString(link.String())
			}
			inLink = true
			link.Reset()
		case name == "a" && closing:
			if inLink {
				words := link.String()
				if !isBareLink(words) {
					out.WriteString(words)
				}
			}
			inLink = false
		case name == "br" || (closing && blockTag[name]):
			text("\n")
		case !closing && blockTag[name]:
			text("\n")
		}
	}
	if inLink {
		out.WriteString(link.String())
	}

	var lines []string
	for _, line := range strings.Split(html.UnescapeString(out.String()), "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// blockTag lists the elements that start a new line.
var blockTag = map[string]bool{
	"article": true, "p": true, "div": true, "li": true, "ul": true, "ol": true, "section": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "tr": true,
}

// startsTag says whether the text after a '<' opens a tag (a letter, '/', '!' or '?'), as an HTML parser decides;
// "a < b" stays text.
func startsTag(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return c == '/' || c == '!' || c == '?' || (c|0x20 >= 'a' && c|0x20 <= 'z')
}

// tagEnd finds the '>' that closes the tag starting at h[start], skipping quoted attribute values; the end of the
// text when it never closes.
func tagEnd(h string, start int) int {
	var quote byte
	for i := start + 1; i < len(h); i++ {
		c := h[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i
		}
	}
	return len(h)
}

// tagName gives a tag's lower-case name and whether it is a closing tag, from the text between '<' and '>'.
func tagName(tag string) (string, bool) {
	closing := strings.HasPrefix(tag, "/")
	tag = strings.TrimPrefix(tag, "/")
	n := 0
	for n < len(tag) && (tag[n]|0x20 >= 'a' && tag[n]|0x20 <= 'z' || tag[n] >= '0' && tag[n] <= '9') {
		n++
	}
	return strings.ToLower(tag[:n]), closing
}

// isBareLink says whether a link's words mean nothing without its address.
func isBareLink(words string) bool {
	switch strings.ToLower(strings.Join(strings.Fields(html.UnescapeString(words)), " ")) {
	case "", "read more", "more", "here", "click here", "link":
		return true
	}
	return false
}
