// Package forge reads pull requests from code hosts (GitHub via the gh CLI,
// Bitbucket Cloud and Server over REST). Providers return a common PR shape;
// nothing outside this package knows host-specific formats.
package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"workstation/internal/config"
)

const (
	StateOpen     = "open"
	StateMerged   = "merged"
	StateDeclined = "declined"

	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	ReviewRequired         = "review_required"

	ChecksSuccess = "success"
	ChecksFailure = "failure"
	ChecksPending = "pending"

	maxPages = 3
)

// PR is a pull request, normalised across hosts.
type PR struct {
	Number           int       `json:"number"`
	Title            string    `json:"title"`
	URL              string    `json:"url"`
	Source           string    `json:"source"` // head branch
	Dest             string    `json:"dest"`   // base branch
	State            string    `json:"state"`  // open | merged | declined
	Draft            bool      `json:"draft"`
	Approvals        int       `json:"approvals"`
	ChangesRequested int       `json:"changesRequested"`
	Review           string    `json:"review,omitempty"` // approved | changes_requested | review_required | ""
	Checks           string    `json:"checks,omitempty"` // success | failure | pending | "" (unknown)
	UpdatedAt        time.Time `json:"updatedAt"`
}

type Provider interface {
	PullRequests(ctx context.Context) ([]PR, error)
}

// ErrNoProvider means the remote is not a host we know how to query. It is
// expected for plain git remotes and is not worth a warning.
var ErrNoProvider = errors.New("no pull request provider for this remote")

// RunFunc runs a command in dir and returns stdout.
type RunFunc func(dir string, args ...string) ([]byte, error)

// Deps are the side-effecting pieces, injectable for tests.
type Deps struct {
	Forges []config.Forge
	Getenv func(string) string
	GH     RunFunc // runs `gh <args>`
	HTTP   *http.Client
}

// DefaultDeps wires the real environment.
func DefaultDeps(forges []config.Forge) Deps {
	return Deps{Forges: forges, Getenv: os.Getenv, GH: runGH, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

func runGH(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

var scpLike = regexp.MustCompile(`^[\w.-]+@([^:/\s]+):(.+)$`)

// ParseRemote extracts host and "owner/repo" (or "PROJECT/repo") from a git
// remote URL in scp, https or ssh form.
func ParseRemote(raw string) (host, path string, ok bool) {
	raw = strings.TrimSpace(raw)
	if m := scpLike.FindStringSubmatch(raw); m != nil && !strings.Contains(raw, "://") {
		host, path = m[1], m[2]
	} else {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return "", "", false
		}
		host, path = u.Hostname(), strings.TrimPrefix(u.Path, "/")
	}
	path = strings.TrimPrefix(strings.TrimSuffix(path, ".git"), "scm/") // Bitbucket Server http clone path
	if len(strings.Split(path, "/")) < 2 {
		return "", "", false
	}
	return host, path, true
}

// Detect picks the provider for a repository from its remote URL.
func Detect(dir, remoteURL string, d Deps) (Provider, error) {
	host, path, ok := ParseRemote(remoteURL)
	if !ok {
		return nil, ErrNoProvider
	}
	if strings.EqualFold(host, "github.com") {
		return &GitHub{Dir: dir, Run: d.GH}, nil
	}

	cfg, configured := findForge(d.Forges, host)
	if !configured && strings.EqualFold(host, "bitbucket.org") {
		cfg, configured = config.Forge{Host: host, Type: "bitbucket-cloud"}, true
	}
	if !configured {
		return nil, ErrNoProvider
	}

	owner, repo, _ := strings.Cut(path, "/")
	switch cfg.Type {
	case "bitbucket-cloud":
		return newCloud(cfg, owner, repo, d)
	case "bitbucket-server":
		return newServer(cfg, host, owner, repo, d)
	}
	return nil, fmt.Errorf("forge %q: unknown type %q", host, cfg.Type)
}

func findForge(forges []config.Forge, host string) (config.Forge, bool) {
	for _, f := range forges {
		if strings.EqualFold(f.Host, host) {
			return f, true
		}
	}
	return config.Forge{}, false
}

func newCloud(cfg config.Forge, ws, repo string, d Deps) (Provider, error) {
	userEnv, tokenEnv := orDefault(cfg.UserEnv, "BITBUCKET_USER"), orDefault(cfg.TokenEnv, "BITBUCKET_TOKEN")
	token := d.Getenv(tokenEnv)
	if token == "" {
		return nil, fmt.Errorf("bitbucket cloud: set %s (and %s for the account email)", tokenEnv, userEnv)
	}
	return &BitbucketCloud{
		Base: "https://api.bitbucket.org", Workspace: ws, Repo: repo,
		User: d.Getenv(userEnv), Token: token, Client: d.HTTP,
	}, nil
}

func newServer(cfg config.Forge, host, project, repo string, d Deps) (Provider, error) {
	if cfg.TokenEnv == "" {
		return nil, fmt.Errorf("bitbucket server %s: tokenEnv is not configured", host)
	}
	token := d.Getenv(cfg.TokenEnv)
	if token == "" {
		return nil, fmt.Errorf("bitbucket server %s: set %s", host, cfg.TokenEnv)
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://" + host
	}
	return &BitbucketServer{Base: base, Project: project, Repo: repo, Token: token, Client: d.HTTP}, nil
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// reviewFrom derives the overall review state shared by the Bitbucket providers.
func reviewFrom(approvals, changes, reviewers int) string {
	switch {
	case changes > 0:
		return ReviewChangesRequested
	case approvals > 0:
		return ReviewApproved
	case reviewers > 0:
		return ReviewRequired
	}
	return ""
}

// getJSON GETs url and decodes the JSON body into out.
func getJSON(ctx context.Context, c *http.Client, url string, auth func(*http.Request), out any) error {
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	auth(req)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("%s: %s %s", url, resp.Status, strings.TrimSpace(string(snippet)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
