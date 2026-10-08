package tickets

import "testing"

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Fix the auth timeout":           "fix-the-auth-timeout",
		"  --Refresh   tokens!! ":        "refresh-tokens",
		"Añadir caché (v2)":              "a-adir-cach-v2",
		"UPPER_snake.case":               "upper-snake-case",
		"":                               "",
		"???":                            "",
		"../../etc/passwd":               "etc-passwd",
		"a very long title that goes on": "a-very-long-title-th",
	} {
		if got := Slug(in, 20); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBranchFor(t *testing.T) {
	for _, c := range []struct{ ref, title, want string }{
		{"REG-5", "Fix token refresh", "REG-5-fix-token-refresh"},
		{"REG-5", "", "REG-5"},
		{"REG-5", "???", "REG-5"},
		{"note:ab12cd", "Cap the bid increment", "cap-the-bid-increment"},
		{"note:ab12cd", "", "ticket-ab12cd"},
		{"note:", "", "ticket-work"},
	} {
		got := BranchFor(c.ref, c.title)
		if got != c.want {
			t.Errorf("BranchFor(%q,%q) = %q, want %q", c.ref, c.title, got, c.want)
		}
		if got == "" || got[0] == '-' {
			t.Errorf("BranchFor(%q,%q) = %q is not a usable branch name", c.ref, c.title, got)
		}
	}
}
