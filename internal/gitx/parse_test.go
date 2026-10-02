package gitx

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseWorktrees(t *testing.T) {
	out := "worktree C:/repo\nHEAD aaa111\nbranch refs/heads/main\n\n" +
		"worktree C:/repo/.claude/worktrees/feat\nHEAD bbb222\nbranch refs/heads/LOY-1-feat\n\n" +
		"worktree C:/wt/detached\nHEAD ccc333\ndetached\n\n" +
		"worktree C:/bare\nbare\n"

	got := ParseWorktrees(out)

	want := []Worktree{
		{Path: filepath.FromSlash("C:/repo"), Head: "aaa111", Branch: "main", IsMain: true},
		{Path: filepath.FromSlash("C:/repo/.claude/worktrees/feat"), Head: "bbb222", Branch: "LOY-1-feat"},
		{Path: filepath.FromSlash("C:/wt/detached"), Head: "ccc333", Detached: true},
		{Path: filepath.FromSlash("C:/bare"), Bare: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseWorktreesEmpty(t *testing.T) {
	if got := ParseWorktrees(""); len(got) != 0 {
		t.Fatalf("expected no worktrees, got %+v", got)
	}
}

func TestParseStatus(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want Status
	}{
		{
			name: "clean with upstream",
			out:  "# branch.oid abc\n# branch.head main\n# branch.upstream origin/main\n# branch.ab +0 -0\n",
			want: Status{Branch: "main", HasUpstream: true},
		},
		{
			name: "ahead and behind",
			out:  "# branch.head feat\n# branch.upstream origin/feat\n# branch.ab +2 -3\n",
			want: Status{Branch: "feat", HasUpstream: true, Ahead: 2, Behind: 3},
		},
		{
			name: "no upstream",
			out:  "# branch.head feat\n",
			want: Status{Branch: "feat"},
		},
		{
			name: "detached head",
			out:  "# branch.head (detached)\n",
			want: Status{Detached: true},
		},
		{
			name: "mixed changes",
			out: "# branch.head feat\n" +
				"1 .M N... 100644 100644 100644 h1 h2 modified.go\n" +
				"1 M. N... 100644 100644 100644 h1 h2 staged.go\n" +
				"1 MM N... 100644 100644 100644 h1 h2 both.go\n" +
				"2 R. N... 100644 100644 100644 h1 h2 R100 new.go\told.go\n" +
				"u UU N... 100644 100644 100644 100644 h1 h2 h3 conflict.go\n" +
				"? untracked.txt\n" +
				"? other dir/file.txt\n" +
				"! ignored.log\n",
			want: Status{Branch: "feat", Staged: 3, Modified: 2, Untracked: 2, Conflicts: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseStatus(tt.out); got != tt.want {
				t.Fatalf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestStatusDirty(t *testing.T) {
	if (Status{}).Dirty() {
		t.Fatal("clean status reported dirty")
	}
	for _, s := range []Status{{Staged: 1}, {Modified: 1}, {Untracked: 1}, {Conflicts: 1}} {
		if !s.Dirty() {
			t.Fatalf("%+v should be dirty", s)
		}
	}
}

func TestParseCommit(t *testing.T) {
	got, ok := ParseCommit("abc1234\x00Fix: thing\x002026-10-02T10:00:00-05:00\n")
	if !ok {
		t.Fatal("expected ok")
	}
	want := Commit{Hash: "abc1234", Subject: "Fix: thing", Date: "2026-10-02T10:00:00-05:00"}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}

	if _, ok := ParseCommit(""); ok {
		t.Fatal("empty output must not parse")
	}
}
