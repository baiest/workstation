package forge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"workstation/internal/config"
)

func TestParseRemote(t *testing.T) {
	tests := []struct {
		url, host, path string
	}{
		{"git@github.com:owner/repo.git", "github.com", "owner/repo"},
		{"https://github.com/owner/repo.git", "github.com", "owner/repo"},
		{"https://github.com/owner/repo", "github.com", "owner/repo"},
		{"https://user@bitbucket.org/ws/repo.git", "bitbucket.org", "ws/repo"},
		{"git@bitbucket.org:ws/repo.git", "bitbucket.org", "ws/repo"},
		{"ssh://git@bb.corp.com:7999/proj/repo.git", "bb.corp.com", "proj/repo"},
		{"https://bb.corp.com/scm/PROJ/repo.git", "bb.corp.com", "PROJ/repo"},
		{"https://bb.corp.com:8443/scm/PROJ/repo.git", "bb.corp.com", "PROJ/repo"},
	}
	for _, tt := range tests {
		host, path, ok := ParseRemote(tt.url)
		if !ok || host != tt.host || path != tt.path {
			t.Errorf("%s -> %q %q ok=%v, want %q %q", tt.url, host, path, ok, tt.host, tt.path)
		}
	}
	for _, bad := range []string{"", "not a url", "https://github.com/onlyone"} {
		if _, _, ok := ParseRemote(bad); ok {
			t.Errorf("%q must not parse", bad)
		}
	}
}

const ghJSON = `[
 {"number":114,"title":"Fix scanner","state":"MERGED","headRefName":"LOY-96","baseRefName":"main","isDraft":false,
  "reviewDecision":"","mergedAt":"2026-10-02T17:00:19Z","updatedAt":"2026-10-02T17:00:19Z","url":"https://github.com/o/r/pull/114",
  "statusCheckRollup":[{"__typename":"StatusContext","state":"SUCCESS"}],"latestReviews":[]},
 {"number":120,"title":"Stacked","state":"OPEN","headRefName":"LOY-69","baseRefName":"LOY-68","isDraft":true,
  "reviewDecision":"CHANGES_REQUESTED","updatedAt":"2026-10-01T10:00:00Z","url":"u120",
  "statusCheckRollup":[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"},{"__typename":"CheckRun","status":"IN_PROGRESS","conclusion":""}],
  "latestReviews":[{"state":"APPROVED"},{"state":"CHANGES_REQUESTED"},{"state":"COMMENTED"}]},
 {"number":121,"title":"Closed","state":"CLOSED","headRefName":"x","baseRefName":"main","isDraft":false,"reviewDecision":"REVIEW_REQUIRED",
  "updatedAt":"2026-09-01T10:00:00Z","url":"u121",
  "statusCheckRollup":[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"FAILURE"},{"__typename":"StatusContext","state":"SUCCESS"}],"latestReviews":[]},
 {"number":122,"title":"Bare","state":"OPEN","headRefName":"y","baseRefName":"main","isDraft":false,"reviewDecision":"APPROVED",
  "updatedAt":"2026-09-01T10:00:00Z","url":"u122","statusCheckRollup":[],"latestReviews":[{"state":"APPROVED"},{"state":"APPROVED"}]}
]`

func TestGitHub(t *testing.T) {
	var calls []string
	p := &GitHub{Dir: "/repo", Run: func(dir string, args ...string) ([]byte, error) {
		if dir != "/repo" {
			t.Errorf("gh must run in the repo dir, got %q", dir)
		}
		calls = append(calls, strings.Join(args, " "))
		return []byte(ghJSON), nil
	}}

	prs, err := p.PullRequests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// checks (statusCheckRollup) are slow on GitHub's side, so only open PRs get them
	if len(calls) != 2 {
		t.Fatalf("expected 2 gh calls, got %v", calls)
	}
	all, open := calls[0], calls[1]
	if !strings.HasPrefix(all, "pr list --state all") || strings.Contains(all, "statusCheckRollup") {
		t.Errorf("first call must list all PRs without checks: %s", all)
	}
	if !strings.HasPrefix(open, "pr list --state open") || !strings.Contains(open, "statusCheckRollup") {
		t.Errorf("second call must fetch checks of open PRs: %s", open)
	}
	if len(prs) != 4 {
		t.Fatalf("got %d prs", len(prs))
	}

	want := []PR{
		{Number: 114, State: StateMerged, Source: "LOY-96", Dest: "main", Checks: ChecksSuccess},
		{Number: 120, State: StateOpen, Draft: true, Source: "LOY-69", Dest: "LOY-68", Approvals: 1, ChangesRequested: 1, Review: ReviewChangesRequested, Checks: ChecksPending},
		{Number: 121, State: StateDeclined, Source: "x", Dest: "main", Review: ReviewRequired, Checks: ChecksFailure},
		{Number: 122, State: StateOpen, Source: "y", Dest: "main", Approvals: 2, Review: ReviewApproved},
	}
	for i, w := range want {
		g := prs[i]
		if g.Number != w.Number || g.State != w.State || g.Draft != w.Draft || g.Source != w.Source || g.Dest != w.Dest ||
			g.Approvals != w.Approvals || g.ChangesRequested != w.ChangesRequested || g.Review != w.Review || g.Checks != w.Checks {
			t.Errorf("pr %d:\n got  %+v\n want %+v", w.Number, g, w)
		}
	}
	if prs[0].URL != "https://github.com/o/r/pull/114" || prs[0].UpdatedAt.IsZero() {
		t.Errorf("url/updatedAt not mapped: %+v", prs[0])
	}
}

func TestGitHubChecksFailureIsNotFatal(t *testing.T) {
	n := 0
	p := &GitHub{Run: func(string, ...string) ([]byte, error) {
		n++
		if n == 2 {
			return nil, errors.New("rate limited")
		}
		return []byte(`[{"number":1,"title":"t","state":"OPEN","headRefName":"a","baseRefName":"main","reviewDecision":"","updatedAt":"2026-10-01T10:00:00Z","url":"u","latestReviews":[]}]`), nil
	}}
	prs, err := p.PullRequests(context.Background())
	if err != nil || len(prs) != 1 || prs[0].Checks != "" {
		t.Fatalf("checks are optional: %+v %v", prs, err)
	}
}

func TestGitHubErrors(t *testing.T) {
	missing := &GitHub{Run: func(string, ...string) ([]byte, error) {
		return nil, errors.New(`exec: "gh": executable file not found`)
	}}
	if _, err := missing.PullRequests(context.Background()); err == nil || !strings.Contains(err.Error(), "gh") {
		t.Fatalf("expected gh error, got %v", err)
	}
	bad := &GitHub{Run: func(string, ...string) ([]byte, error) { return []byte("{nope"), nil }}
	if _, err := bad.PullRequests(context.Background()); err == nil {
		t.Fatal("expected json error")
	}
}

func TestBitbucketCloud(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "me@x.com" || pass != "tok" {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Path != "/2.0/repositories/ws/repo/pullrequests" {
			http.Error(w, "bad path "+r.URL.Path, 404)
			return
		}
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"values":[{"id":3,"title":"Old","state":"DECLINED","source":{"branch":{"name":"c"}},"destination":{"branch":{"name":"main"}},"links":{"html":{"href":"https://bb.test/3"}},"updated_on":"2026-09-01T10:00:00.000000+00:00","participants":[]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"values":[
		 {"id":1,"title":"A","state":"OPEN","draft":false,"source":{"branch":{"name":"a"}},"destination":{"branch":{"name":"main"}},"links":{"html":{"href":"https://bb.test/1"}},"updated_on":"2026-10-01T10:00:00.000000+00:00",
		  "participants":[{"role":"REVIEWER","approved":true,"state":"approved"},{"role":"REVIEWER","approved":false,"state":null},{"role":"PARTICIPANT","approved":false,"state":null}]},
		 {"id":2,"title":"B","state":"OPEN","draft":true,"source":{"branch":{"name":"b"}},"destination":{"branch":{"name":"a"}},"links":{"html":{"href":"https://bb.test/2"}},"updated_on":"2026-10-01T11:00:00.000000+00:00",
		  "participants":[{"role":"REVIEWER","approved":false,"state":"changes_requested"}]}
		],"next":"` + srv.URL + `/2.0/repositories/ws/repo/pullrequests?page=2"}`))
	}))
	defer srv.Close()

	p := &BitbucketCloud{Base: srv.URL, Workspace: "ws", Repo: "repo", User: "me@x.com", Token: "tok", Client: srv.Client()}
	prs, err := p.PullRequests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 3 {
		t.Fatalf("pagination: got %d prs", len(prs))
	}
	a, b, c := prs[0], prs[1], prs[2]
	if a.Number != 1 || a.State != StateOpen || a.Source != "a" || a.Dest != "main" || a.Approvals != 1 || a.Review != ReviewApproved || a.URL != "https://bb.test/1" {
		t.Errorf("A: %+v", a)
	}
	if !b.Draft || b.ChangesRequested != 1 || b.Review != ReviewChangesRequested || b.Dest != "a" {
		t.Errorf("B: %+v", b)
	}
	if c.State != StateDeclined {
		t.Errorf("C: %+v", c)
	}
	if a.Checks != "" {
		t.Errorf("checks are not fetched for bitbucket: %q", a.Checks)
	}

	bad := &BitbucketCloud{Base: srv.URL, Workspace: "ws", Repo: "repo", User: "me@x.com", Token: "wrong", Client: srv.Client()}
	if _, err := bad.PullRequests(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 error, got %v", err)
	}
}

func TestBitbucketCloudMapsMergedAndSuperseded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[
		 {"id":1,"state":"MERGED","source":{"branch":{"name":"a"}},"destination":{"branch":{"name":"main"}},"participants":[]},
		 {"id":2,"state":"SUPERSEDED","source":{"branch":{"name":"b"}},"destination":{"branch":{"name":"main"}},"participants":[]}]}`))
	}))
	defer srv.Close()
	p := &BitbucketCloud{Base: srv.URL, Workspace: "w", Repo: "r", Client: srv.Client()}
	prs, err := p.PullRequests(context.Background())
	if err != nil || prs[0].State != StateMerged || prs[1].State != StateDeclined {
		t.Fatalf("%+v %v", prs, err)
	}
}

func TestBitbucketServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", 401)
			return
		}
		if r.URL.Path != "/rest/api/1.0/projects/PROJ/repos/repo/pull-requests" || r.URL.Query().Get("state") != "ALL" {
			http.Error(w, "bad "+r.URL.String(), 404)
			return
		}
		if r.URL.Query().Get("start") == "2" {
			_, _ = w.Write([]byte(`{"values":[{"id":3,"title":"C","state":"MERGED","fromRef":{"displayId":"c"},"toRef":{"displayId":"main"},"links":{"self":[{"href":"https://bb.test/3"}]},"updatedDate":1790000000000,"reviewers":[]}],"isLastPage":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"values":[
		 {"id":1,"title":"A","state":"OPEN","fromRef":{"displayId":"a"},"toRef":{"displayId":"main"},"links":{"self":[{"href":"https://bb.test/1"}]},"updatedDate":1790000000000,
		  "reviewers":[{"status":"APPROVED","approved":true},{"status":"UNAPPROVED","approved":false}]},
		 {"id":2,"title":"B","state":"DECLINED","fromRef":{"displayId":"b"},"toRef":{"displayId":"a"},"links":{"self":[{"href":"https://bb.test/2"}]},"updatedDate":1790000000000,
		  "reviewers":[{"status":"NEEDS_WORK","approved":false}]}
		],"isLastPage":false,"nextPageStart":2}`))
	}))
	defer srv.Close()

	p := &BitbucketServer{Base: srv.URL, Project: "PROJ", Repo: "repo", Token: "tok", Client: srv.Client()}
	prs, err := p.PullRequests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 3 {
		t.Fatalf("pagination: got %d", len(prs))
	}
	a, b, c := prs[0], prs[1], prs[2]
	if a.State != StateOpen || a.Approvals != 1 || a.Review != ReviewApproved || a.Source != "a" || a.URL != "https://bb.test/1" {
		t.Errorf("A: %+v", a)
	}
	if b.State != StateDeclined || b.ChangesRequested != 1 || b.Review != ReviewChangesRequested || b.Dest != "a" {
		t.Errorf("B: %+v", b)
	}
	if c.State != StateMerged || c.UpdatedAt.IsZero() {
		t.Errorf("C: %+v", c)
	}
}

// PR links are rendered as <a href>: a hostile server must not smuggle in a
// javascript: or data: URL.
func TestOnlyHTTPLinksSurvive(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/o/r/pull/1": "https://github.com/o/r/pull/1",
		"http://bb.local/pull/2":        "http://bb.local/pull/2",
		"javascript:alert(1)":           "",
		" JaVaScRiPt:alert(1)":          "",
		"data:text/html,<script>":       "",
		"//evil.example/x":              "",
		"/relative/path":                "",
		"vbscript:x":                    "",
		"":                              "",
	} {
		if got := safeURL(in); got != want {
			t.Errorf("safeURL(%q) = %q, want %q", in, got, want)
		}
	}

	gh := &GitHub{Run: func(string, ...string) ([]byte, error) {
		return []byte(`[{"number":1,"title":"t","state":"OPEN","headRefName":"a","baseRefName":"main","updatedAt":"2026-10-01T10:00:00Z","url":"javascript:alert(1)","latestReviews":[]}]`), nil
	}}
	if prs, err := gh.PullRequests(context.Background()); err != nil || prs[0].URL != "" {
		t.Errorf("github: %+v %v", prs, err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/2.0/") {
			_, _ = w.Write([]byte(`{"values":[{"id":1,"state":"OPEN","source":{"branch":{"name":"a"}},"destination":{"branch":{"name":"main"}},"links":{"html":{"href":"javascript:alert(1)"}},"participants":[]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"values":[{"id":1,"state":"OPEN","fromRef":{"displayId":"a"},"toRef":{"displayId":"main"},"links":{"self":[{"href":"data:text/html,x"}]},"reviewers":[]}],"isLastPage":true}`))
	}))
	defer srv.Close()
	cloud := &BitbucketCloud{Base: srv.URL, Workspace: "w", Repo: "r", Client: srv.Client()}
	if prs, err := cloud.PullRequests(context.Background()); err != nil || prs[0].URL != "" {
		t.Errorf("cloud: %+v %v", prs, err)
	}
	server := &BitbucketServer{Base: srv.URL, Project: "P", Repo: "r", Token: "t", Client: srv.Client()}
	if prs, err := server.PullRequests(context.Background()); err != nil || prs[0].URL != "" {
		t.Errorf("server: %+v %v", prs, err)
	}
}

func TestResponseBodyIsCapped(t *testing.T) {
	old := maxBody
	maxBody = 1024
	defer func() { maxBody = old }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[],"padding":"` + strings.Repeat("x", 4096) + `"}`))
	}))
	defer srv.Close()

	cloud := &BitbucketCloud{Base: srv.URL, Workspace: "w", Repo: "r", Client: srv.Client()}
	if _, err := cloud.PullRequests(context.Background()); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("an oversized response must be refused, got %v", err)
	}
}

// Credentials must never follow a pagination link to another host.
func TestCloudPaginationStaysOnTheSameHost(t *testing.T) {
	hits := 0
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"values":[]}`))
	}))
	defer evil.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[],"next":"` + evil.URL + `/steal"}`))
	}))
	defer srv.Close()

	cloud := &BitbucketCloud{Base: srv.URL, Workspace: "w", Repo: "r", User: "u", Token: "secret", Client: srv.Client()}
	_, err := cloud.PullRequests(context.Background())
	if err == nil || !strings.Contains(err.Error(), "another host") {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if hits != 0 {
		t.Fatalf("the other host was contacted %d times (credentials would have leaked)", hits)
	}
}

func TestDetectRefusesRiskyForgeConfig(t *testing.T) {
	env := map[string]string{"BB_TOKEN": "s", "AWS_SECRET_ACCESS_KEY": "aws", "GITHUB_TOKEN": "g", "BITBUCKET_TOKEN": "t"}
	base := Deps{Getenv: func(k string) string { return env[k] }, GH: func(string, ...string) ([]byte, error) { return nil, nil }}
	detect := func(f config.Forge, remote string) error {
		d := base
		d.Forges = []config.Forge{f}
		_, err := Detect("/r", remote, d)
		return err
	}
	remote := "https://bb.corp.com/scm/P/r.git"

	if err := detect(config.Forge{Host: "bb.corp.com", Type: "bitbucket-server", TokenEnv: "BB_TOKEN"}, remote); err != nil {
		t.Errorf("sane config must pass: %v", err)
	}
	if err := detect(config.Forge{Host: "bb.corp.com", Type: "bitbucket-server", TokenEnv: "BB_TOKEN", BaseURL: "https://bb.corp.com/git"}, remote); err != nil {
		t.Errorf("https baseUrl on the same host must pass: %v", err)
	}

	risky := map[string]config.Forge{
		"plain http baseUrl":        {Host: "bb.corp.com", Type: "bitbucket-server", TokenEnv: "BB_TOKEN", BaseURL: "http://bb.corp.com"},
		"baseUrl on another host":   {Host: "bb.corp.com", Type: "bitbucket-server", TokenEnv: "BB_TOKEN", BaseURL: "https://evil.example"},
		"baseUrl with credentials":  {Host: "bb.corp.com", Type: "bitbucket-server", TokenEnv: "BB_TOKEN", BaseURL: "https://u:p@bb.corp.com"},
		"unrelated secret as token": {Host: "bb.corp.com", Type: "bitbucket-server", TokenEnv: "AWS_SECRET_ACCESS_KEY"},
		"github token as token":     {Host: "bb.corp.com", Type: "bitbucket-server", TokenEnv: "GITHUB_TOKEN"},
		"lowercase env name":        {Host: "bb.corp.com", Type: "bitbucket-server", TokenEnv: "bb_token"},
	}
	for name, f := range risky {
		if err := detect(f, remote); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}

	cloud := config.Forge{Host: "bitbucket.org", Type: "bitbucket-cloud", UserEnv: "AWS_SECRET_ACCESS_KEY", TokenEnv: "BITBUCKET_TOKEN"}
	if err := detect(cloud, "git@bitbucket.org:w/r.git"); err == nil {
		t.Error("cloud userEnv outside the allowed prefixes must be refused")
	}
}

func TestDetect(t *testing.T) {
	env := map[string]string{"BITBUCKET_USER": "u", "BITBUCKET_TOKEN": "t", "BB_TOKEN": "s"}
	deps := Deps{
		Forges: []config.Forge{
			{Host: "bb.corp.com", Type: "bitbucket-server", TokenEnv: "BB_TOKEN"},
			{Host: "bb.nobase.com", Type: "bitbucket-server", TokenEnv: "MISSING"},
			{Host: "bb.custom.com", Type: "bitbucket-server", TokenEnv: "BB_TOKEN", BaseURL: "https://bb.custom.com/git/"},
		},
		Getenv: func(k string) string { return env[k] },
		GH:     func(string, ...string) ([]byte, error) { return nil, nil },
	}

	if p, err := Detect("/r", "git@github.com:o/r.git", deps); err != nil || p == nil {
		t.Fatalf("github: %v %v", p, err)
	} else if gh, ok := p.(*GitHub); !ok || gh.Dir != "/r" {
		t.Fatalf("github provider: %#v", p)
	}

	p, err := Detect("/r", "git@bitbucket.org:ws/repo.git", deps)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := p.(*BitbucketCloud); !ok || c.Workspace != "ws" || c.Repo != "repo" || c.User != "u" || c.Token != "t" {
		t.Fatalf("cloud provider: %#v", p)
	}

	p, err = Detect("/r", "ssh://git@bb.corp.com:7999/PROJ/repo.git", deps)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := p.(*BitbucketServer); !ok || s.Base != "https://bb.corp.com" || s.Project != "PROJ" || s.Repo != "repo" || s.Token != "s" {
		t.Fatalf("server provider: %#v", p)
	}

	p, err = Detect("/r", "https://bb.custom.com/scm/P/r.git", deps)
	if s, ok := p.(*BitbucketServer); err != nil || !ok || s.Base != "https://bb.custom.com/git" {
		t.Fatalf("custom base: %#v %v", p, err)
	}
}

func TestDetectErrors(t *testing.T) {
	deps := Deps{
		Forges: []config.Forge{{Host: "bb.corp.com", Type: "bitbucket-server", TokenEnv: "BB_TOKEN"}},
		Getenv: func(string) string { return "" },
		GH:     func(string, ...string) ([]byte, error) { return nil, nil },
	}

	if _, err := Detect("/r", "git@gitlab.com:o/r.git", deps); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("unknown host should be ErrNoProvider, got %v", err)
	}
	if _, err := Detect("/r", "garbage", deps); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("unparsable remote should be ErrNoProvider, got %v", err)
	}
	if _, err := Detect("/r", "git@bitbucket.org:ws/repo.git", deps); err == nil || !strings.Contains(err.Error(), "BITBUCKET_TOKEN") {
		t.Fatalf("missing cloud token must name the env var, got %v", err)
	}
	if _, err := Detect("/r", "https://bb.corp.com/scm/P/r.git", deps); err == nil || !strings.Contains(err.Error(), "BB_TOKEN") {
		t.Fatalf("missing server token must name the env var, got %v", err)
	}
}
