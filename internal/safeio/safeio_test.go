package safeio

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, n int) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, bytes.Repeat([]byte("x"), n), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	if data, err := ReadFile(write("small", 10), 100); err != nil || len(data) != 10 {
		t.Fatalf("small file: %d bytes, %v", len(data), err)
	}
	if data, err := ReadFile(write("exact", 100), 100); err != nil || len(data) != 100 {
		t.Fatalf("a file of exactly max bytes is fine: %d, %v", len(data), err)
	}
	if _, err := ReadFile(write("big", 101), 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("one byte over must be ErrTooLarge, got %v", err)
	}
	if _, err := ReadFile(filepath.Join(dir, "missing"), 100); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a missing file must stay ErrNotExist, got %v", err)
	}
}

func TestReadFileDoesNotTrustTheReportedSize(t *testing.T) {
	// the size is checked while reading, not only by Stat (the file may grow)
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(p, 1000); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v", err)
	}
}
