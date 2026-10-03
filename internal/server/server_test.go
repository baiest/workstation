package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"workstation/internal/branches"
	"workstation/internal/claude"
	"workstation/internal/workspace"
	"workstation/internal/wtclean"
)

type fakeLauncher struct {
	calls  []string
	err    error
	editor string
}

func (f *fakeLauncher) Terminal(dir string) error {
	f.calls = append(f.calls, "terminal:"+dir)
	return f.err
}
func (f *fakeLauncher) Editor(dir string) error {
	f.calls = append(f.calls, "editor:"+dir)
	return f.err
}
func (f *fakeLauncher) EditorName() string { return f.editor }
func (f *fakeLauncher) Desktop() error {
	f.calls = append(f.calls, "desktop")
	return f.err
}
func (f *fakeLauncher) Resume(dir, id string) error {
	f.calls = append(f.calls, "resume:"+dir+"|"+id)
	return f.err
}

func fixtureWorkspace(t *testing.T) (workspace.Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	return workspace.Workspace{
		Repos: []workspace.Repo{{Name: "r", Path: "/r", Worktrees: []workspace.Worktree{{
			Name: "wt", Path: dir, Branch: "feat",
			Sessions: []claude.Session{
				{ID: "sess-1", Cwd: dir, Resumable: true, Slug: "plan-one", HasPlan: true},
				{ID: "local_x", Cwd: dir, Resumable: false},
			},
		}}}},
		Unlinked: []workspace.Unlinked{
			{Session: claude.Session{ID: "orphan", Cwd: dir, Resumable: true, Slug: "plan-orphan", HasPlan: true}, Reason: "x"},
			{Session: claude.Session{ID: "gone", Cwd: dir + "-missing", Resumable: true}, Reason: "x"},
		},
	}, dir
}

func newTestServer(t *testing.T) (http.Handler, *fakeLauncher, string, *int) {
	t.Helper()
	ws, dir := fixtureWorkspace(t)
	builds := 0
	l := &fakeLauncher{editor: "cursor"}
	h := New(func() (workspace.Workspace, error) { builds++; return ws, nil }, l,
		fstest.MapFS{"index.html": {Data: []byte("<html>ok</html>")}})
	return h, l, dir, &builds
}

func do(h http.Handler, method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = "127.0.0.1:7420"
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestWorkspaceEndpoint(t *testing.T) {
	h, _, _, _ := newTestServer(t)
	rec := do(h, http.MethodGet, "/api/workspace", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Repos        []map[string]any `json:"repos"`
		Capabilities struct {
			Editor string `json:"editor"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Repos) != 1 || got.Capabilities.Editor != "cursor" {
		t.Fatalf("unexpected body: %s", rec.Body)
	}
}

func TestWorkspaceEndpointBuildError(t *testing.T) {
	h := New(func() (workspace.Workspace, error) { return workspace.Workspace{}, errors.New("boom") }, &fakeLauncher{}, fstest.MapFS{})
	if rec := do(h, http.MethodGet, "/api/workspace", "", nil); rec.Code != 500 {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestHostAndOriginChecks(t *testing.T) {
	h, l, dir, _ := newTestServer(t)
	body := `{"path":` + jsonStr(dir) + `}`

	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("foreign Host must be 403 (DNS rebinding), got %d", rec.Code)
	}

	if rec := do(h, http.MethodPost, "/api/actions/terminal", body, map[string]string{"Origin": "http://evil.example.com"}); rec.Code != 403 {
		t.Fatalf("foreign Origin must be 403, got %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/actions/terminal", body, map[string]string{"Content-Type": "text/plain"}); rec.Code != 415 {
		t.Fatalf("non-JSON must be 415, got %d", rec.Code)
	}
	if len(l.calls) != 0 {
		t.Fatalf("nothing should have launched: %v", l.calls)
	}
	if rec := do(h, http.MethodPost, "/api/actions/terminal", body, map[string]string{"Origin": "http://127.0.0.1:7420"}); rec.Code != 204 {
		t.Fatalf("same-origin must pass, got %d", rec.Code)
	}
}

// Only loopback Hosts are served: the Host header is client-controlled, so
// accepting private IPs would not be an access control, only an invitation.
func TestOnlyLoopbackHostsAreServed(t *testing.T) {
	h, _, _, _ := newTestServer(t)

	get := func(host string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, host := range []string{"127.0.0.1:7420", "localhost:7420", "[::1]:7420", "127.0.0.1"} {
		if get(host) != 200 {
			t.Errorf("%s must be served", host)
		}
	}
	for _, host := range []string{"192.168.1.20:7420", "10.0.0.5:7420", "172.16.3.4:7420", "8.8.8.8:7420", "evil.example.com", "localhost.evil.com:7420", ""} {
		if get(host) != 403 {
			t.Errorf("%q must be rejected", host)
		}
	}
}

// A burst of refreshes must not multiply the (git-heavy) workspace build.
// Internal errors (paths, git stderr, file names) are logged, not sent to the
// browser. Only validation messages written for the user are returned.
func TestInternalErrorsAreNotLeaked(t *testing.T) {
	secret := `C:\Users\victim\secret-repo: fatal: unsafe detail`

	h := New(func() (workspace.Workspace, error) { return workspace.Workspace{}, errors.New(secret) }, &fakeLauncher{}, fstest.MapFS{})
	rec := do(h, http.MethodGet, "/api/workspace", "", nil)
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "secret-repo") || !strings.Contains(rec.Body.String(), "internal error") {
		t.Errorf("workspace: %d %q", rec.Code, rec.Body)
	}

	h2, dir := newServerWith(t, WithPlans(fakePlans{err: errors.New(secret)}), WithBranches(&fakeBranches{err: errors.New(secret)}))
	if rec := do(h2, http.MethodGet, "/api/plan?session=sess-1", "", nil); rec.Code != 500 || strings.Contains(rec.Body.String(), "secret-repo") {
		t.Errorf("plan: %d %q", rec.Code, rec.Body)
	}
	if rec := do(h2, http.MethodGet, "/api/branches?repo=/r", "", nil); rec.Code != 500 || strings.Contains(rec.Body.String(), "secret-repo") {
		t.Errorf("branches: %d %q", rec.Code, rec.Body)
	}

	fl := &fakeLauncher{err: errors.New(secret)}
	ws, d := fixtureWorkspace(t)
	h3 := New(func() (workspace.Workspace, error) { return ws, nil }, fl, fstest.MapFS{})
	if rec := do(h3, http.MethodPost, "/api/actions/terminal", `{"path":`+jsonStr(d)+`}`, nil); rec.Code != 500 || strings.Contains(rec.Body.String(), "secret-repo") {
		t.Errorf("action: %d %q", rec.Code, rec.Body)
	}
	_ = dir

	// validation messages meant for the user do get through
	fl.err = badRequest("not a directory: /x")
	if rec := do(h3, http.MethodPost, "/api/actions/terminal", `{"path":`+jsonStr(d)+`}`, nil); rec.Code != 400 || !strings.Contains(rec.Body.String(), "not a directory") {
		t.Errorf("validation error: %d %q", rec.Code, rec.Body)
	}
}

func TestMissingDefaultBranchIsExplained(t *testing.T) {
	h, _ := newServerWith(t, WithBranches(&fakeBranches{err: fmt.Errorf("wrapped: %w", branches.ErrNoDefaultBranch)}))
	rec := do(h, http.MethodGet, "/api/branches?repo=/r", "", nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "default branch") {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
}

func TestConcurrentWorkspaceRequestsShareOneBuild(t *testing.T) {
	ws, _ := fixtureWorkspace(t)
	var builds int32
	started := make(chan struct{}, 16)
	release := make(chan struct{})
	h := New(func() (workspace.Workspace, error) {
		atomic.AddInt32(&builds, 1)
		started <- struct{}{}
		<-release
		return ws, nil
	}, &fakeLauncher{}, fstest.MapFS{})

	const n = 8
	codes := make(chan int, n)
	for i := 0; i < n; i++ {
		go func() { codes <- do(h, http.MethodGet, "/api/workspace", "", nil).Code }()
	}
	<-started                          // first build is running
	time.Sleep(150 * time.Millisecond) // let the others arrive and queue behind it
	close(release)
	for i := 0; i < n; i++ {
		if c := <-codes; c != 200 {
			t.Errorf("request %d: %d", i, c)
		}
	}
	if got := atomic.LoadInt32(&builds); got != 1 {
		t.Fatalf("%d concurrent requests triggered %d builds, want 1", n, got)
	}

	// once the build is over, the next request builds again (Refresh must be fresh)
	if c := do(h, http.MethodGet, "/api/workspace", "", nil).Code; c != 200 {
		t.Fatalf("follow-up request: %d", c)
	}
	if got := atomic.LoadInt32(&builds); got != 2 {
		t.Fatalf("a request after the build finished must rebuild, builds=%d", got)
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	h, _, _, _ := newTestServer(t)

	cases := map[string]*httptest.ResponseRecorder{
		"static":    do(h, http.MethodGet, "/", "", nil),
		"api":       do(h, http.MethodGet, "/api/workspace", "", nil),
		"not found": do(h, http.MethodGet, "/api/plan?session=x", "", nil),
		"forbidden": do(h, http.MethodGet, "/", "", map[string]string{"Origin": "http://evil.example"}),
	}
	for name, rec := range cases {
		hd := rec.Header()
		if hd.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: X-Frame-Options = %q", name, hd.Get("X-Frame-Options"))
		}
		if hd.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", name, hd.Get("X-Content-Type-Options"))
		}
		if hd.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: Referrer-Policy = %q", name, hd.Get("Referrer-Policy"))
		}
		if hd.Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Errorf("%s: Cross-Origin-Resource-Policy = %q", name, hd.Get("Cross-Origin-Resource-Policy"))
		}
		csp := hd.Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'", "base-uri 'none'", "form-action 'none'", "connect-src 'self'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP lacks %q: %s", name, want, csp)
			}
		}
		if strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
			t.Errorf("%s: scripts must stay strict: %s", name, csp)
		}
	}
}

func TestRejectsCrossSiteFetchMetadata(t *testing.T) {
	h, l, dir, _ := newTestServer(t)
	body := `{"path":` + jsonStr(dir) + `}`

	for _, site := range []string{"cross-site", "same-site"} {
		if rec := do(h, http.MethodGet, "/api/workspace", "", map[string]string{"Sec-Fetch-Site": site}); rec.Code != 403 {
			t.Errorf("GET with Sec-Fetch-Site=%s must be 403, got %d", site, rec.Code)
		}
		if rec := do(h, http.MethodPost, "/api/actions/terminal", body, map[string]string{"Sec-Fetch-Site": site}); rec.Code != 403 {
			t.Errorf("POST with Sec-Fetch-Site=%s must be 403, got %d", site, rec.Code)
		}
	}
	if len(l.calls) != 0 {
		t.Fatalf("nothing may launch: %v", l.calls)
	}
	for _, site := range []string{"same-origin", "none", ""} {
		hdr := map[string]string{}
		if site != "" {
			hdr["Sec-Fetch-Site"] = site
		}
		if rec := do(h, http.MethodGet, "/api/workspace", "", hdr); rec.Code != 200 {
			t.Errorf("Sec-Fetch-Site=%q must pass, got %d", site, rec.Code)
		}
	}
}

func TestHTTPServerHasTimeouts(t *testing.T) {
	h, _, _, _ := newTestServer(t)
	s := NewHTTPServer("127.0.0.1:7420", h)

	if s.Addr != "127.0.0.1:7420" || s.Handler == nil {
		t.Fatalf("addr/handler not set: %+v", s)
	}
	if s.ReadHeaderTimeout <= 0 || s.ReadTimeout <= 0 || s.WriteTimeout <= 0 || s.IdleTimeout <= 0 {
		t.Errorf("all timeouts must be set (slow-header connections would pile up): %+v", s)
	}
	if s.ReadHeaderTimeout > s.ReadTimeout {
		t.Error("header timeout must not exceed the read timeout")
	}
	if s.MaxHeaderBytes <= 0 || s.MaxHeaderBytes > 1<<20 {
		t.Errorf("MaxHeaderBytes = %d, want a small explicit limit", s.MaxHeaderBytes)
	}
}

func TestRequireLoopback(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:7420", "localhost:8080", "[::1]:7420", "127.0.0.2:1"} {
		if err := RequireLoopback(ok); err != nil {
			t.Errorf("%s must be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"0.0.0.0:7420", ":7420", "[::]:7420", "192.168.1.9:7420", "example.com:80", "7420", "127.0.0.1", ""} {
		if err := RequireLoopback(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestActionsOnlyForKnownWorktrees(t *testing.T) {
	h, l, dir, builds := newTestServer(t)

	if rec := do(h, http.MethodPost, "/api/actions/terminal", `{"path":`+jsonStr(dir)+`}`, nil); rec.Code != 204 {
		t.Fatalf("known path: %d %s", rec.Code, rec.Body)
	}
	if *builds != 1 {
		t.Fatalf("action without prior GET should build once, built %d", *builds)
	}
	if rec := do(h, http.MethodPost, "/api/actions/editor", `{"path":`+jsonStr(dir)+`}`, nil); rec.Code != 204 {
		t.Fatalf("editor: %d", rec.Code)
	}
	if *builds != 1 {
		t.Fatalf("snapshot should be reused, built %d", *builds)
	}
	if rec := do(h, http.MethodPost, "/api/actions/terminal", `{"path":"C:\\Windows"}`, nil); rec.Code != 400 {
		t.Fatalf("unknown path must be 400, got %d", rec.Code)
	}
	want := []string{"terminal:" + dir, "editor:" + dir}
	if strings.Join(l.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", l.calls, want)
	}
}

func TestResumeAction(t *testing.T) {
	h, l, dir, _ := newTestServer(t)

	for _, c := range []struct {
		id   string
		code int
	}{{"sess-1", 204}, {"orphan", 204}, {"local_x", 400}, {"gone", 400}, {"nope", 400}} {
		if rec := do(h, http.MethodPost, "/api/actions/resume", `{"sessionId":"`+c.id+`"}`, nil); rec.Code != c.code {
			t.Errorf("resume %s: got %d want %d (%s)", c.id, rec.Code, c.code, rec.Body)
		}
	}
	want := "resume:" + dir + "|sess-1,resume:" + dir + "|orphan"
	if strings.Join(l.calls, ",") != want {
		t.Fatalf("calls = %v", l.calls)
	}
}

func TestResumeInDesktop(t *testing.T) {
	ws, dir := fixtureWorkspace(t)
	// one session that also exists in Claude Desktop, one that only exists in the CLI
	ws.Repos[0].Worktrees[0].Sessions[0].DesktopID = "local_abc"
	l := &fakeLauncher{}
	h := New(func() (workspace.Workspace, error) { return ws, nil }, l, fstest.MapFS{})

	if rec := do(h, http.MethodPost, "/api/actions/resume", `{"sessionId":"sess-1","mode":"desktop"}`, nil); rec.Code != 204 {
		t.Fatalf("a Desktop session must open in Desktop: %d %s", rec.Code, rec.Body)
	}
	if len(l.calls) != 1 || l.calls[0] != "desktop" {
		t.Fatalf("Desktop mode opens the app, not a terminal: %v", l.calls)
	}

	if rec := do(h, http.MethodPost, "/api/actions/resume", `{"sessionId":"orphan","mode":"desktop"}`, nil); rec.Code != 400 {
		t.Errorf("a CLI-only session cannot open in Desktop: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/actions/resume", `{"sessionId":"sess-1","mode":"bogus"}`, nil); rec.Code != 400 {
		t.Errorf("an unknown mode must be 400, got %d", rec.Code)
	}
	if len(l.calls) != 1 {
		t.Fatalf("refused requests must not launch anything: %v", l.calls)
	}

	// the default and the explicit CLI mode keep resuming in a terminal
	for _, body := range []string{`{"sessionId":"sess-1"}`, `{"sessionId":"sess-1","mode":"cli"}`} {
		l.calls = nil
		if rec := do(h, http.MethodPost, "/api/actions/resume", body, nil); rec.Code != 204 {
			t.Fatalf("%s: %d", body, rec.Code)
		}
		if len(l.calls) != 1 || l.calls[0] != "resume:"+dir+"|sess-1" {
			t.Errorf("%s: %v", body, l.calls)
		}
	}
}

func TestActionErrors(t *testing.T) {
	h, l, dir, _ := newTestServer(t)
	if rec := do(h, http.MethodPost, "/api/actions/bogus", `{"path":`+jsonStr(dir)+`}`, nil); rec.Code != 404 {
		t.Fatalf("unknown action: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/actions/terminal", `{bad`, nil); rec.Code != 400 {
		t.Fatalf("bad json: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/actions/terminal", "", nil); rec.Code != 405 {
		t.Fatalf("GET action: %d", rec.Code)
	}
	l.err = errors.New("no terminal")
	if rec := do(h, http.MethodPost, "/api/actions/terminal", `{"path":`+jsonStr(dir)+`}`, nil); rec.Code != 500 {
		t.Fatalf("launcher failure: %d", rec.Code)
	}
}

type fakePlans struct {
	plans map[string]claude.Plan
	err   error
}

func (f fakePlans) ReadPlan(slug string) (claude.Plan, error) {
	if f.err != nil {
		return claude.Plan{}, f.err
	}
	p, ok := f.plans[slug]
	if !ok {
		return claude.Plan{}, os.ErrNotExist
	}
	return p, nil
}

type fakeBranches struct {
	repo          string
	wts           map[string]branches.Worktree
	merged, fresh bool
	fast          int // calls to GraphWithoutPRs
	full          int // calls to Graph
	err           error
}

func (f *fakeBranches) Graph(repo string, wts map[string]branches.Worktree, merged, fresh bool) (branches.Response, error) {
	f.repo, f.wts, f.merged, f.fresh = repo, wts, merged, fresh
	f.full++
	return branches.Response{Graph: branches.Graph{Default: "main", Nodes: []branches.Node{{Branch: "main", IsDefault: true}}}, Warnings: []string{}}, f.err
}

func (f *fakeBranches) GraphWithoutPRs(repo string, wts map[string]branches.Worktree, merged bool) (branches.Response, error) {
	f.repo, f.wts, f.merged = repo, wts, merged
	f.fast++
	return branches.Response{Graph: branches.Graph{Default: "main", Nodes: []branches.Node{{Branch: "main", IsDefault: true}}}, Warnings: []string{}, PRsPending: true}, f.err
}

func newServerWith(t *testing.T, opts ...Option) (http.Handler, string) {
	t.Helper()
	ws, dir := fixtureWorkspace(t)
	h := New(func() (workspace.Workspace, error) { return ws, nil }, &fakeLauncher{}, fstest.MapFS{}, opts...)
	return h, dir
}

func TestPlanEndpoint(t *testing.T) {
	plans := fakePlans{plans: map[string]claude.Plan{
		"plan-one":    {Slug: "plan-one", Markdown: "# One", Path: "/p/plan-one.md"},
		"plan-orphan": {Slug: "plan-orphan", Markdown: "# Orphan"},
	}}
	h, _ := newServerWith(t, WithPlans(plans))

	for id, want := range map[string]string{"sess-1": "# One", "orphan": "# Orphan"} {
		rec := do(h, http.MethodGet, "/api/plan?session="+id, "", nil)
		var got claude.Plan
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Markdown != want {
			t.Errorf("%s: %d %s", id, rec.Code, rec.Body)
		}
	}
	for _, id := range []string{"local_x", "nope", ""} {
		if rec := do(h, http.MethodGet, "/api/plan?session="+id, "", nil); rec.Code != 404 {
			t.Errorf("session %q without plan must be 404, got %d", id, rec.Code)
		}
	}

	gone, _ := newServerWith(t, WithPlans(fakePlans{}))
	if rec := do(gone, http.MethodGet, "/api/plan?session=sess-1", "", nil); rec.Code != 404 {
		t.Errorf("deleted plan file must be 404, got %d", rec.Code)
	}
	broken, _ := newServerWith(t, WithPlans(fakePlans{err: errors.New("disk on fire")}))
	if rec := do(broken, http.MethodGet, "/api/plan?session=sess-1", "", nil); rec.Code != 500 {
		t.Errorf("read failure must be 500, got %d", rec.Code)
	}
	none, _ := newServerWith(t)
	if rec := do(none, http.MethodGet, "/api/plan?session=sess-1", "", nil); rec.Code != 404 {
		t.Errorf("no plan reader configured must be 404, got %d", rec.Code)
	}
}

// prs=0 asks for the graph from git alone, so the page can paint before the
// forge answers.
func TestBranchesEndpointCanSkipPullRequests(t *testing.T) {
	fb := &fakeBranches{}
	h, dir := newServerWith(t, WithBranches(fb))

	rec := do(h, http.MethodGet, "/api/branches?repo=/r&prs=0&merged=1", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"prsPending":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if fb.fast != 1 || fb.full != 0 || !fb.merged {
		t.Fatalf("prs=0 must use the git-only path: %+v", fb)
	}
	if w, ok := fb.wts["feat"]; !ok || w.Path != dir {
		t.Errorf("worktrees: %+v", fb.wts)
	}

	if rec := do(h, http.MethodGet, "/api/branches?repo=/r", "", nil); rec.Code != 200 || fb.full != 1 || fb.fast != 1 {
		t.Fatalf("the default stays the full graph: %d %+v", rec.Code, fb)
	}
	if rec := do(h, http.MethodGet, "/api/branches?repo=/etc&prs=0", "", nil); rec.Code != 400 {
		t.Errorf("repo outside the snapshot must be 400 here too, got %d", rec.Code)
	}
}

func TestBranchesEndpoint(t *testing.T) {
	fb := &fakeBranches{}
	h, dir := newServerWith(t, WithBranches(fb))

	rec := do(h, http.MethodGet, "/api/branches?repo=/r&merged=1&refresh=1", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"default":"main"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if fb.repo != "/r" || !fb.merged || !fb.fresh {
		t.Errorf("flags not passed: %+v", fb)
	}
	if w, ok := fb.wts["feat"]; !ok || w.Name != "wt" || w.Path != dir {
		t.Errorf("worktrees by branch not passed: %+v", fb.wts)
	}

	if rec := do(h, http.MethodGet, "/api/branches?repo=/r", "", nil); rec.Code != 200 || fb.merged || fb.fresh {
		t.Errorf("flags must default to false: %d %+v", rec.Code, fb)
	}
	if rec := do(h, http.MethodGet, "/api/branches?repo=/etc", "", nil); rec.Code != 400 {
		t.Errorf("repo outside the snapshot must be 400, got %d", rec.Code)
	}

	fb.err = errors.New("no default branch")
	if rec := do(h, http.MethodGet, "/api/branches?repo=/r", "", nil); rec.Code != 500 {
		t.Errorf("service error must be 500, got %d", rec.Code)
	}
	none, _ := newServerWith(t)
	if rec := do(none, http.MethodGet, "/api/branches?repo=/r", "", nil); rec.Code != 404 {
		t.Errorf("no branch service configured must be 404, got %d", rec.Code)
	}
}

type fakeCleanup struct {
	repo           string
	wts            map[string]branches.Worktree
	days           int
	refresh        bool
	req            []branches.DeleteRequest
	allowLocalOnly bool
	deletes        int
	err            error
}

func (f *fakeCleanup) CleanupPreview(repo string, wts map[string]branches.Worktree, days int, refresh bool) (branches.CleanupResponse, error) {
	f.repo, f.wts, f.days, f.refresh = repo, wts, days, refresh
	return branches.CleanupResponse{
		CleanupPreview: branches.CleanupPreview{Days: 30, Candidates: []branches.Candidate{{Branch: "old", SHA: "abc", PRNumber: 3}}},
		Warnings:       []string{},
	}, f.err
}

func (f *fakeCleanup) CleanupDelete(repo string, wts map[string]branches.Worktree, days int, allowLocalOnly bool, req []branches.DeleteRequest) ([]branches.DeleteResult, error) {
	f.repo, f.wts, f.days, f.req, f.allowLocalOnly = repo, wts, days, req, allowLocalOnly
	f.deletes++
	var out []branches.DeleteResult
	for _, r := range req {
		out = append(out, branches.DeleteResult{Branch: r.Branch, SHA: r.SHA, Deleted: true})
	}
	return out, f.err
}

func TestCleanupPreviewEndpoint(t *testing.T) {
	fc := &fakeCleanup{}
	h, dir := newServerWith(t, WithCleanup(fc))

	rec := do(h, http.MethodGet, "/api/cleanup?repo=/r&days=45&refresh=1", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"branch":"old"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if fc.repo != "/r" || fc.days != 45 || !fc.refresh {
		t.Errorf("args: %+v", fc)
	}
	if w, ok := fc.wts["feat"]; !ok || w.Path != dir {
		t.Errorf("worktrees (never deleted) must be passed: %+v", fc.wts)
	}

	if rec := do(h, http.MethodGet, "/api/cleanup?repo=/r&days=abc", "", nil); rec.Code != 400 {
		t.Errorf("a non-numeric days must be 400, got %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/cleanup?repo=/etc", "", nil); rec.Code != 400 {
		t.Errorf("repo outside the snapshot must be 400, got %d", rec.Code)
	}
	none, _ := newServerWith(t)
	if rec := do(none, http.MethodGet, "/api/cleanup?repo=/r", "", nil); rec.Code != 404 {
		t.Errorf("no cleanup service must be 404, got %d", rec.Code)
	}
}

func TestCleanupPreviewErrors(t *testing.T) {
	h, _ := newServerWith(t, WithCleanup(&fakeCleanup{err: fmt.Errorf("x: %w", branches.ErrNoDefaultBranch)}))
	if rec := do(h, http.MethodGet, "/api/cleanup?repo=/r", "", nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), "default branch") {
		t.Errorf("%d %q", rec.Code, rec.Body)
	}
	h2, _ := newServerWith(t, WithCleanup(&fakeCleanup{err: errors.New(`C:\secret-repo: boom`)}))
	if rec := do(h2, http.MethodGet, "/api/cleanup?repo=/r", "", nil); rec.Code != 500 || strings.Contains(rec.Body.String(), "secret-repo") {
		t.Errorf("%d %q", rec.Code, rec.Body)
	}
}

func TestCleanupDeleteEndpoint(t *testing.T) {
	fc := &fakeCleanup{}
	h, _ := newServerWith(t, WithCleanup(fc))
	body := `{"repo":"/r","days":30,"branches":[{"branch":"old","sha":"abc"},{"branch":"older","sha":"def"}]}`

	rec := do(h, http.MethodPost, "/api/cleanup", body, nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got struct {
		Results []branches.DeleteResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Results) != 2 || !got.Results[0].Deleted {
		t.Fatalf("results: %s %v", rec.Body, err)
	}
	if fc.repo != "/r" || fc.days != 30 || len(fc.req) != 2 || fc.req[0].SHA != "abc" {
		t.Errorf("service args: %+v", fc)
	}

	// the explicit yes for branches that exist only here travels with the request, and defaults to no
	if fc.allowLocalOnly {
		t.Error("allowLocalOnly must default to false")
	}
	yes := `{"repo":"/r","days":30,"allowLocalOnly":true,"branches":[{"branch":"old","sha":"abc"}]}`
	if rec := do(h, http.MethodPost, "/api/cleanup", yes, nil); rec.Code != 200 || !fc.allowLocalOnly {
		t.Errorf("allowLocalOnly must be passed through: %d %+v", rec.Code, fc)
	}

	// refused before reaching the service
	bad := map[string]string{
		"unknown repo": `{"repo":"/etc","branches":[{"branch":"x","sha":"y"}]}`,
		"bad json":     `{nope`,
		"nothing sent": `{"repo":"/r","branches":[]}`,
		"too many":     `{"repo":"/r","branches":[` + strings.TrimSuffix(strings.Repeat(`{"branch":"b","sha":"s"},`, 201), ",") + `]}`,
		"empty branch": `{"repo":"/r","branches":[{"branch":"","sha":"y"}]}`,
		"dash branch":  `{"repo":"/r","branches":[{"branch":"-D","sha":"y"}]}`,
	}
	before := fc.deletes
	for name, b := range bad {
		if rec := do(h, http.MethodPost, "/api/cleanup", b, nil); rec.Code != 400 {
			t.Errorf("%s: want 400, got %d %s", name, rec.Code, rec.Body)
		}
	}
	if fc.deletes != before {
		t.Fatalf("a refused request must not reach the service")
	}

	if rec := do(h, http.MethodPost, "/api/cleanup", body, map[string]string{"Content-Type": "text/plain"}); rec.Code != 415 {
		t.Errorf("non-JSON must be 415, got %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/cleanup", body, map[string]string{"Sec-Fetch-Site": "cross-site"}); rec.Code != 403 {
		t.Errorf("cross-site must be 403, got %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/cleanup", body, map[string]string{"Origin": "http://evil.example"}); rec.Code != 403 {
		t.Errorf("foreign origin must be 403, got %d", rec.Code)
	}
}

type fakeWT struct {
	repo    string
	days    int
	req     []wtclean.RemoveRequest
	removes int
	err     error
}

func (f *fakeWT) Preview(repo workspace.Repo, days int) (wtclean.Response, error) {
	f.repo, f.days = repo.Path, days
	return wtclean.Response{
		Preview:  wtclean.Preview{Days: 7, Candidates: []wtclean.Candidate{{Path: "/p/done", Name: "done", Branch: "done", SHA: "abc", PRNumber: 3}}},
		Warnings: []string{},
	}, f.err
}

func (f *fakeWT) Remove(repo workspace.Repo, days int, req []wtclean.RemoveRequest) ([]wtclean.RemoveResult, error) {
	f.repo, f.days, f.req = repo.Path, days, req
	f.removes++
	var out []wtclean.RemoveResult
	for _, r := range req {
		out = append(out, wtclean.RemoveResult{Path: r.Path, SHA: r.SHA, Removed: true})
	}
	return out, f.err
}

// Removing folders must rest on a freshly built workspace, not the snapshot the
// page loaded minutes ago.
func TestWorktreeCleanupUsesAFreshWorkspace(t *testing.T) {
	ws, _ := fixtureWorkspace(t)
	builds := 0
	fw := &fakeWT{}
	h := New(func() (workspace.Workspace, error) { builds++; return ws, nil }, &fakeLauncher{}, fstest.MapFS{}, WithWorktreeCleanup(fw))

	rec := do(h, http.MethodGet, "/api/worktree-cleanup?repo=/r&days=14", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"done"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if fw.repo != "/r" || fw.days != 14 || builds != 1 {
		t.Fatalf("preview: repo=%q days=%d builds=%d", fw.repo, fw.days, builds)
	}

	body := `{"repo":"/r","days":14,"worktrees":[{"path":"/p/done","sha":"abc"}]}`
	if rec := do(h, http.MethodPost, "/api/worktree-cleanup", body, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"removed":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if builds != 2 || fw.removes != 1 || len(fw.req) != 1 || fw.req[0].Path != "/p/done" || fw.req[0].SHA != "abc" {
		t.Fatalf("remove must rebuild the workspace first: builds=%d %+v", builds, fw)
	}
}

func TestWorktreeCleanupValidation(t *testing.T) {
	fw := &fakeWT{}
	h, _ := newServerWith(t, WithWorktreeCleanup(fw))

	for name, body := range map[string]string{
		"unknown repo": `{"repo":"/etc","worktrees":[{"path":"/p","sha":"s"}]}`,
		"bad json":     `{nope`,
		"nothing sent": `{"repo":"/r","worktrees":[]}`,
		"too many":     `{"repo":"/r","worktrees":[` + strings.TrimSuffix(strings.Repeat(`{"path":"/p","sha":"s"},`, 101), ",") + `]}`,
		"empty path":   `{"repo":"/r","worktrees":[{"path":"","sha":"s"}]}`,
		"missing sha":  `{"repo":"/r","worktrees":[{"path":"/p","sha":""}]}`,
	} {
		if rec := do(h, http.MethodPost, "/api/worktree-cleanup", body, nil); rec.Code != 400 {
			t.Errorf("%s: want 400, got %d %s", name, rec.Code, rec.Body)
		}
	}
	if fw.removes != 0 {
		t.Fatal("a refused request must not reach the service")
	}
	good := `{"repo":"/r","worktrees":[{"path":"/p","sha":"s"}]}`
	if rec := do(h, http.MethodPost, "/api/worktree-cleanup", good, map[string]string{"Content-Type": "text/plain"}); rec.Code != 415 {
		t.Errorf("non-JSON must be 415, got %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/worktree-cleanup", good, map[string]string{"Sec-Fetch-Site": "cross-site"}); rec.Code != 403 {
		t.Errorf("cross-site must be 403, got %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/worktree-cleanup?repo=/etc", "", nil); rec.Code != 400 {
		t.Errorf("preview of an unknown repo must be 400, got %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/worktree-cleanup?repo=/r&days=abc", "", nil); rec.Code != 400 {
		t.Errorf("a non-numeric days must be 400, got %d", rec.Code)
	}
	none, _ := newServerWith(t)
	if rec := do(none, http.MethodGet, "/api/worktree-cleanup?repo=/r", "", nil); rec.Code != 404 {
		t.Errorf("no service configured must be 404, got %d", rec.Code)
	}
}

func TestWorktreeCleanupErrors(t *testing.T) {
	h, _ := newServerWith(t, WithWorktreeCleanup(&fakeWT{err: fmt.Errorf("x: %w", branches.ErrNoDefaultBranch)}))
	if rec := do(h, http.MethodGet, "/api/worktree-cleanup?repo=/r", "", nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), "default branch") {
		t.Errorf("%d %q", rec.Code, rec.Body)
	}
	h2, _ := newServerWith(t, WithWorktreeCleanup(&fakeWT{err: errors.New(`C:\secret-repo: boom`)}))
	if rec := do(h2, http.MethodGet, "/api/worktree-cleanup?repo=/r", "", nil); rec.Code != 500 || strings.Contains(rec.Body.String(), "secret-repo") {
		t.Errorf("%d %q", rec.Code, rec.Body)
	}
}

func TestStaticFiles(t *testing.T) {
	h, _, _, _ := newTestServer(t)
	if rec := do(h, http.MethodGet, "/", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("index: %d %s", rec.Code, rec.Body)
	}

	empty := New(nil, &fakeLauncher{}, fstest.MapFS{})
	rec := do(empty, http.MethodGet, "/", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "pnpm") {
		t.Fatalf("missing frontend should explain how to build it: %d %s", rec.Code, rec.Body)
	}
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
