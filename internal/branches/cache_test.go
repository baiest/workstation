package branches

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"workstation/internal/forge"
)

var errBoom = errors.New("boom")

func open(n int, branch string) forge.PR {
	return forge.PR{Number: n, Source: branch, Dest: "main", State: forge.StateOpen}
}

// within runs fn and fails if it does not return in time: the point of the
// stale-while-revalidate cache is that the page never waits for the forge.
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s blocked on the forge", what)
	}
}

func TestExpiredCacheIsServedAtOnceAndRefreshedInTheBackground(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()
	fp := &fakeProvider{prs: []forge.PR{open(5, "feat-a")}}
	s := newService(fp, nil, &now)

	if _, err := s.Graph(repo, nil, false, false); err != nil || fp.n() != 1 {
		t.Fatalf("cold start fetches once: calls=%d err=%v", fp.n(), err)
	}

	now = now.Add(10 * time.Minute) // long expired
	gate := make(chan struct{})
	fp.set([]forge.PR{open(6, "feat-b")}, gate) // the next fetch hangs until we say so

	var stale Response
	within(t, 10*time.Second, "an expired-cache request", func() { stale, _ = s.Graph(repo, nil, false, false) })

	if a := nodeMap(stale.Graph)["feat-a"]; a.PR == nil || a.PR.Number != 5 {
		t.Errorf("the old PRs are shown while the new ones load: %+v", a)
	}
	if !stale.PRsStale || stale.PRsFetchedAt.IsZero() {
		t.Errorf("the response must say the data is old and from when: stale=%v at=%v", stale.PRsStale, stale.PRsFetchedAt)
	}

	close(gate)
	s.Wait()
	fresh, err := s.Graph(repo, nil, false, false)
	if err != nil || fresh.PRsStale || fp.n() != 2 {
		t.Fatalf("after the refresh the data is current: stale=%v calls=%d err=%v", fresh.PRsStale, fp.n(), err)
	}
	if b := nodeMap(fresh.Graph)["feat-b"]; b.PR == nil || b.PR.Number != 6 {
		t.Errorf("the new PRs are in: %+v", b)
	}
}

func TestOnlyOneBackgroundRefreshAtATime(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()
	fp := &fakeProvider{prs: []forge.PR{open(5, "feat-a")}}
	s := newService(fp, nil, &now)
	if _, err := s.Graph(repo, nil, false, false); err != nil {
		t.Fatal(err)
	}

	now = now.Add(10 * time.Minute)
	gate := make(chan struct{})
	fp.set(fp.prs, gate)
	for i := 0; i < 8; i++ { // a burst of page loads while the cache is expired
		within(t, 10*time.Second, "request", func() { _, _ = s.Graph(repo, nil, false, false) })
	}
	close(gate)
	s.Wait()
	if fp.n() != 2 {
		t.Fatalf("8 requests on an expired cache caused %d fetches, want 1 (plus the first)", fp.n()-1)
	}
}

// Two requests arriving together on a cold cache (the page loading a repo while a
// cleanup preview asks for it) must share one fetch.
func TestConcurrentColdRequestsShareOneFetch(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()
	gate := make(chan struct{})
	started := make(chan struct{}, 16)
	fp := &fakeProvider{prs: []forge.PR{open(5, "feat-a")}, gate: gate, started: started}
	s := newService(fp, nil, &now)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.Graph(repo, nil, false, false) }()
	}
	<-started                          // the first fetch is running
	time.Sleep(150 * time.Millisecond) // the others arrive and queue behind it
	close(gate)
	wg.Wait()
	if fp.n() != 1 {
		t.Fatalf("8 concurrent cold requests caused %d fetches", fp.n())
	}
}

func TestRefreshStillForcesAFetch(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()
	fp := &fakeProvider{prs: []forge.PR{open(5, "feat-a")}}
	s := newService(fp, nil, &now)
	_, _ = s.Graph(repo, nil, false, false)
	if _, err := s.Graph(repo, nil, false, true); err != nil || fp.n() != 2 {
		t.Fatalf("an explicit refresh fetches even when the cache is fresh: calls=%d err=%v", fp.n(), err)
	}
}

func TestCacheLastsFiveMinutesByDefault(t *testing.T) {
	t.Parallel()
	now := time.Now()
	s := NewService(forge.Deps{})
	s.Now = func() time.Time { return now }
	if s.TTL != 5*time.Minute {
		t.Fatalf("default TTL = %v, want 5m", s.TTL)
	}
}

// A failed lookup is remembered briefly, so a broken login does not hammer the
// forge on every request, but it is retried soon.
func TestFailuresAreCachedShortly(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()
	fp := &fakeProvider{err: errBoom}
	s := newService(fp, nil, &now)
	s.TTL = 5 * time.Minute

	_, _ = s.Graph(repo, nil, false, false)
	_, _ = s.Graph(repo, nil, false, false)
	if fp.n() != 1 {
		t.Fatalf("a failure is not retried on every request: calls=%d", fp.n())
	}
	now = now.Add(time.Minute) // beyond the failure window, well within the normal TTL
	_, _ = s.Graph(repo, nil, false, false)
	s.Wait()
	if fp.n() != 2 {
		t.Fatalf("a failure is retried after a short while: calls=%d", fp.n())
	}
}

func TestPersistedCacheSurvivesARestart(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()
	store := NewDiskStore(t.TempDir())

	fp1 := &fakeProvider{prs: []forge.PR{open(5, "feat-a")}}
	s1 := newService(fp1, nil, &now)
	s1.Store = store
	if _, err := s1.Graph(repo, nil, false, false); err != nil || fp1.n() != 1 {
		t.Fatalf("first run: calls=%d err=%v", fp1.n(), err)
	}

	// "restart": a new service, same store, a provider that must not be needed
	fp2 := &fakeProvider{prs: []forge.PR{open(99, "other")}}
	s2 := newService(fp2, nil, &now)
	s2.Store = store

	r, err := s2.GraphWithoutPRs(repo, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if a := nodeMap(r.Graph)["feat-a"]; a.PR == nil || a.PR.Number != 5 || r.PRsPending {
		t.Errorf("the first paint already has the PRs from disk: %+v pending=%v", a, r.PRsPending)
	}
	if full, err := s2.Graph(repo, nil, false, false); err != nil || fp2.n() != 0 {
		t.Fatalf("a fresh persisted cache needs no fetch: calls=%d err=%v %+v", fp2.n(), err, full.PRsStale)
	}
}

func TestPersistedCacheKeepsItsAge(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()
	store := NewDiskStore(t.TempDir())
	fp1 := &fakeProvider{prs: []forge.PR{open(5, "feat-a")}}
	s1 := newService(fp1, nil, &now)
	s1.Store = store
	_, _ = s1.Graph(repo, nil, false, false)

	now = now.Add(time.Hour) // restarted an hour later
	fp2 := &fakeProvider{prs: []forge.PR{open(6, "feat-a")}}
	s2 := newService(fp2, nil, &now)
	s2.Store = store

	r, err := s2.Graph(repo, nil, false, false)
	if err != nil || !r.PRsStale {
		t.Fatalf("an hour-old file is shown at once but marked old: stale=%v err=%v", r.PRsStale, err)
	}
	s2.Wait()
	if fp2.n() != 1 {
		t.Fatalf("and refreshed in the background: calls=%d", fp2.n())
	}
}

func TestFailedLookupsAreNotPersisted(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()
	dir := t.TempDir()
	s := newService(&fakeProvider{err: errBoom}, nil, &now)
	s.Store = NewDiskStore(dir)
	_, _ = s.Graph(repo, nil, false, false)

	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Fatalf("an error must not overwrite good data on disk: %v", files)
	}
}

func TestStoreIgnoresCorruptOrOversizedFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := NewDiskStore(dir)

	if _, ok := store.Load("/some/repo"); ok {
		t.Error("an empty store has nothing")
	}
	if err := store.Save("/some/repo", Snapshot{FetchedAt: time.Now(), PRs: []forge.PR{open(1, "a")}}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) != 1 {
		t.Fatalf("one file per repo: %v", files)
	}

	if err := os.WriteFile(files[0], []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Load("/some/repo"); ok {
		t.Error("a corrupt file must be ignored")
	}
	if err := os.WriteFile(files[0], make([]byte, maxCacheFile+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Load("/some/repo"); ok {
		t.Error("an oversized file must be ignored")
	}
}

func TestStoreFilesAreOnlyReadableByTheUser(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	dir := filepath.Join(t.TempDir(), "nested", "cache")
	store := NewDiskStore(dir)
	if err := store.Save("/r", Snapshot{FetchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("the directory holds PR titles: mode %v, want 0700 (%v)", st.Mode().Perm(), err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if fi, err := os.Stat(files[0]); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v, want 0600 (%v)", fi.Mode().Perm(), err)
	}
}

func TestStoreKeysDoNotCollide(t *testing.T) {
	t.Parallel()
	store := NewDiskStore(t.TempDir())
	_ = store.Save("/a/repo", Snapshot{FetchedAt: time.Now(), PRs: []forge.PR{open(1, "a")}})
	_ = store.Save("/b/repo", Snapshot{FetchedAt: time.Now(), PRs: []forge.PR{open(2, "b")}})
	a, _ := store.Load("/a/repo")
	b, _ := store.Load("/b/repo")
	if len(a.PRs) != 1 || a.PRs[0].Number != 1 || len(b.PRs) != 1 || b.PRs[0].Number != 2 {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
}
