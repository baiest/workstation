package tickets

import (
	"strings"
	"unicode"
)

// Slug turns a title into a branch-safe fragment: lower case ASCII letters and
// digits joined by single dashes, at most max characters, no dash at either end.
func Slug(s string, max int) string {
	var b strings.Builder
	dash := true // a leading separator is dropped
	for _, r := range strings.ToLower(s) {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := b.String()
	if len(out) > max {
		out = out[:max]
	}
	return strings.Trim(out, "-")
}

// BranchFor names the branch for a ticket that has none yet: its tracker key and
// a few words of the title (REG-5-fix-token-refresh), or the words alone for a
// ticket without a key. Never empty and always safe as a branch and a folder name.
func BranchFor(ref, title string) string {
	slug := Slug(title, 40)
	switch {
	case IsKey(ref) && slug != "":
		return ref + "-" + slug
	case IsKey(ref):
		return ref
	case slug != "":
		return slug
	}
	id := Slug(strings.TrimPrefix(ref, notePrefix), 12)
	if id == "" {
		id = "work"
	}
	return "ticket-" + id
}
