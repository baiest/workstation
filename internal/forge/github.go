package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// GitHub reads PRs through the gh CLI, which already holds the user's auth and
// infers the repository from the remote of Dir.
type GitHub struct {
	Dir string
	Run RunFunc
}

// statusCheckRollup is slow to resolve on GitHub's side (seconds for 100 PRs),
// so it is requested separately and only for open PRs.
const (
	ghFields      = "number,title,state,headRefName,baseRefName,isDraft,reviewDecision,updatedAt,mergedAt,headRefOid,url,latestReviews"
	ghCheckFields = "number,statusCheckRollup"
)

type ghPR struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	State          string `json:"state"`
	HeadRefName    string `json:"headRefName"`
	BaseRefName    string `json:"baseRefName"`
	IsDraft        bool   `json:"isDraft"`
	ReviewDecision string `json:"reviewDecision"`
	UpdatedAt      string `json:"updatedAt"`
	MergedAt       string `json:"mergedAt"`
	HeadRefOid     string `json:"headRefOid"`
	URL            string `json:"url"`
	Checks         []struct {
		Typename   string `json:"__typename"`
		State      string `json:"state"`      // StatusContext
		Status     string `json:"status"`     // CheckRun
		Conclusion string `json:"conclusion"` // CheckRun
	} `json:"statusCheckRollup"`
	LatestReviews []struct {
		State string `json:"state"`
	} `json:"latestReviews"`
}

func (g *GitHub) PullRequests(context.Context) ([]PR, error) {
	out, err := g.Run(g.Dir, "pr", "list", "--state", "all", "--limit", "100", "--json", ghFields)
	if err != nil {
		return nil, fmt.Errorf("github (needs the gh CLI, logged in): %w", err)
	}
	var raw []ghPR
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("github: unexpected gh output: %w", err)
	}
	checks := g.openChecks()

	prs := make([]PR, 0, len(raw))
	for _, r := range raw {
		pr := PR{
			Number: r.Number, Title: r.Title, URL: safeURL(r.URL), Source: r.HeadRefName, Dest: r.BaseRefName,
			State: ghState(r.State), Draft: r.IsDraft, Review: strings.ToLower(r.ReviewDecision),
			Checks: checks[r.Number],
		}
		pr.UpdatedAt, _ = time.Parse(time.RFC3339, r.UpdatedAt)
		pr.HeadSHA = r.HeadRefOid
		if pr.State == StateMerged {
			pr.MergedAt, _ = time.Parse(time.RFC3339, r.MergedAt)
		}
		for _, rv := range r.LatestReviews {
			switch rv.State {
			case "APPROVED":
				pr.Approvals++
			case "CHANGES_REQUESTED":
				pr.ChangesRequested++
			}
		}
		prs = append(prs, pr)
	}
	return prs, nil
}

// openChecks returns the folded check status of open PRs by number. Checks are
// a nice-to-have: any failure here just leaves them unknown.
func (g *GitHub) openChecks() map[int]string {
	out, err := g.Run(g.Dir, "pr", "list", "--state", "open", "--limit", "100", "--json", ghCheckFields)
	if err != nil {
		return nil
	}
	var raw []ghPR
	if json.Unmarshal(out, &raw) != nil {
		return nil
	}
	checks := make(map[int]string, len(raw))
	for _, r := range raw {
		checks[r.Number] = ghChecks(r)
	}
	return checks
}

func ghState(s string) string {
	switch s {
	case "MERGED":
		return StateMerged
	case "CLOSED":
		return StateDeclined
	}
	return StateOpen
}

// ghChecks folds the check rollup: any failure wins, then pending, then success.
func ghChecks(r ghPR) string {
	var failed, pending, ok bool
	for _, c := range r.Checks {
		switch strings.ToUpper(firstNonEmpty(c.Conclusion, c.State, c.Status)) {
		case "FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
			failed = true
		case "PENDING", "EXPECTED", "IN_PROGRESS", "QUEUED", "WAITING", "REQUESTED":
			pending = true
		case "SUCCESS", "NEUTRAL", "SKIPPED":
			ok = true
		}
	}
	switch {
	case failed:
		return ChecksFailure
	case pending:
		return ChecksPending
	case ok:
		return ChecksSuccess
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
