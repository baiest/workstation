package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file gives defaults", func(t *testing.T) {
		c, err := Load(filepath.Join(dir, "none.json"))
		if err != nil || len(c.Repos) != 0 {
			t.Fatalf("got %+v %v", c, err)
		}
	})

	t.Run("reads repos", func(t *testing.T) {
		p := filepath.Join(dir, "c.json")
		if err := os.WriteFile(p, []byte(`{"repos":["/a","/b"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := Load(p)
		if err != nil || !reflect.DeepEqual(c.Repos, []string{"/a", "/b"}) {
			t.Fatalf("got %+v %v", c, err)
		}
	})

	t.Run("reads forges without secrets", func(t *testing.T) {
		p := filepath.Join(dir, "f.json")
		body := `{"forges":[{"host":"bb.corp.com","type":"bitbucket-server","tokenEnv":"BB_TOKEN","baseUrl":"https://bb.corp.com/git"}]}`
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := Load(p)
		want := Forge{Host: "bb.corp.com", Type: "bitbucket-server", TokenEnv: "BB_TOKEN", BaseURL: "https://bb.corp.com/git"}
		if err != nil || len(c.Forges) != 1 || c.Forges[0] != want {
			t.Fatalf("got %+v %v", c, err)
		}
	})

	t.Run("invalid json is an error", func(t *testing.T) {
		p := filepath.Join(dir, "bad.json")
		if err := os.WriteFile(p, []byte(`{nope`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err == nil {
			t.Fatal("expected error")
		}
	})
}
