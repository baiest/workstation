package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "notes.json")
	return New(path), path
}

func TestSetAndGetSurviveARestart(t *testing.T) {
	s, path := newStore(t)
	if err := s.Set("/r/a", Note{Starred: true, Text: "  next: add the migration  "}); err != nil {
		t.Fatal(err)
	}
	got := New(path).All()
	if n := got["/r/a"]; !n.Starred || n.Text != "next: add the migration" {
		t.Fatalf("got %+v", n)
	}
}

func TestEmptyNoteIsForgotten(t *testing.T) {
	s, path := newStore(t)
	_ = s.Set("/r/a", Note{Text: "x"})
	if err := s.Set("/r/a", Note{}); err != nil {
		t.Fatal(err)
	}
	if len(New(path).All()) != 0 {
		t.Fatal("an unstarred note without text must not be kept")
	}
}

func TestTextIsCleanedAndCapped(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Set("/r/a", Note{Text: "a\x00b\nc\x1b"}); err != nil {
		t.Fatal(err)
	}
	if got := s.All()["/r/a"].Text; got != "ab c" {
		t.Fatalf("control characters must go, newlines become spaces: %q", got)
	}
	if err := s.Set("/r/a", Note{Text: strings.Repeat("x", MaxText+1)}); err == nil {
		t.Fatal("over-long text must be rejected")
	}
}

func TestEntryLimit(t *testing.T) {
	s, _ := newStore(t)
	for i := 0; i < MaxEntries; i++ {
		if err := s.Set(filepath.Join("/r", string(rune('a'+i%26)), strings.Repeat("d", i)), Note{Starred: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Set("/r/one-too-many", Note{Starred: true}); err == nil {
		t.Fatal("the store must be bounded")
	}
}

func TestDelete(t *testing.T) {
	s, path := newStore(t)
	_ = s.Set("/r/a", Note{Starred: true})
	if err := s.Delete("/r/a"); err != nil {
		t.Fatal(err)
	}
	if len(New(path).All()) != 0 {
		t.Fatal("not deleted")
	}
}

func TestCorruptOrOversizedFileCountsAsEmpty(t *testing.T) {
	_, path := newStore(t)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte("{not json"), 0o600)
	if len(New(path).All()) != 0 {
		t.Fatal("corrupt file must read as empty")
	}
	_ = os.WriteFile(path, []byte(strings.Repeat(" ", maxFile+1)), 0o600)
	if len(New(path).All()) != 0 {
		t.Fatal("oversized file must read as empty")
	}
}

func TestEmptyPathMeansMemoryOnly(t *testing.T) {
	s := New("")
	if err := s.Set("/r/a", Note{Starred: true}); err != nil {
		t.Fatal(err)
	}
	if !s.All()["/r/a"].Starred {
		t.Fatal("must still work in memory")
	}
}
