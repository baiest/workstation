package forge

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// BitbucketCloud reads PRs from the Bitbucket Cloud REST API v2.
// Auth: Basic (account email + API token) or, without User, a Bearer access token.
// Build/check status is not fetched (it costs one request per PR).
type BitbucketCloud struct {
	Base, Workspace, Repo, User, Token string
	Client                             *http.Client
}

type cloudPage struct {
	Values []struct {
		ID           int                                    `json:"id"`
		Title        string                                 `json:"title"`
		State        string                                 `json:"state"`
		Draft        bool                                   `json:"draft"`
		UpdatedOn    string                                 `json:"updated_on"`
		Source       struct{ Branch struct{ Name string } } `json:"source"`
		Destination  struct{ Branch struct{ Name string } } `json:"destination"`
		Links        struct{ HTML struct{ Href string } }   `json:"links"`
		Participants []struct {
			Role     string `json:"role"`
			Approved bool   `json:"approved"`
			State    string `json:"state"`
		} `json:"participants"`
	} `json:"values"`
	Next string `json:"next"`
}

func (b *BitbucketCloud) auth(r *http.Request) {
	switch {
	case b.User != "":
		r.SetBasicAuth(b.User, b.Token)
	case b.Token != "":
		r.Header.Set("Authorization", "Bearer "+b.Token)
	}
}

func (b *BitbucketCloud) PullRequests(ctx context.Context) ([]PR, error) {
	next := fmt.Sprintf("%s/2.0/repositories/%s/%s/pullrequests?state=OPEN&state=MERGED&state=DECLINED&state=SUPERSEDED&pagelen=50",
		b.Base, url.PathEscape(b.Workspace), url.PathEscape(b.Repo))

	var prs []PR
	for page := 0; next != "" && page < maxPages; page++ {
		var p cloudPage
		if err := getJSON(ctx, b.Client, next, b.auth, &p); err != nil {
			return nil, fmt.Errorf("bitbucket cloud: %w", err)
		}
		for _, v := range p.Values {
			pr := PR{
				Number: v.ID, Title: v.Title, URL: safeURL(v.Links.HTML.Href), Draft: v.Draft,
				Source: v.Source.Branch.Name, Dest: v.Destination.Branch.Name, State: cloudState(v.State),
			}
			pr.UpdatedAt, _ = time.Parse(time.RFC3339, v.UpdatedOn)
			reviewers := 0
			for _, pt := range v.Participants {
				if pt.Role == "REVIEWER" {
					reviewers++
				}
				if pt.Approved {
					pr.Approvals++
				}
				if pt.State == "changes_requested" {
					pr.ChangesRequested++
				}
			}
			pr.Review = reviewFrom(pr.Approvals, pr.ChangesRequested, reviewers)
			prs = append(prs, pr)
		}
		next = p.Next
		if next != "" && !sameOrigin(next, b.Base) {
			// the Authorization header would follow the link: never send it elsewhere
			return nil, fmt.Errorf("bitbucket cloud: refusing pagination link to another host: %s", next)
		}
	}
	return prs, nil
}

func cloudState(s string) string {
	switch s {
	case "MERGED":
		return StateMerged
	case "DECLINED", "SUPERSEDED":
		return StateDeclined
	}
	return StateOpen
}

// BitbucketServer reads PRs from Bitbucket Server / Data Center REST 1.0 with
// an HTTP access token. Build/check status is not fetched.
type BitbucketServer struct {
	Base, Project, Repo, Token string
	Client                     *http.Client
}

type serverPage struct {
	Values []struct {
		ID          int    `json:"id"`
		Title       string `json:"title"`
		State       string `json:"state"`
		Draft       bool   `json:"draft"`
		UpdatedDate int64  `json:"updatedDate"`
		FromRef     struct {
			DisplayID string `json:"displayId"`
		} `json:"fromRef"`
		ToRef struct {
			DisplayID string `json:"displayId"`
		} `json:"toRef"`
		Links struct {
			Self []struct{ Href string } `json:"self"`
		} `json:"links"`
		Reviewers []struct {
			Status string `json:"status"`
		} `json:"reviewers"`
	} `json:"values"`
	IsLastPage    bool `json:"isLastPage"`
	NextPageStart int  `json:"nextPageStart"`
}

func (b *BitbucketServer) PullRequests(ctx context.Context) ([]PR, error) {
	auth := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+b.Token) }

	var prs []PR
	start := 0
	for page := 0; page < maxPages; page++ {
		u := fmt.Sprintf("%s/rest/api/1.0/projects/%s/repos/%s/pull-requests?state=ALL&limit=50&start=%d",
			b.Base, url.PathEscape(b.Project), url.PathEscape(b.Repo), start)
		var p serverPage
		if err := getJSON(ctx, b.Client, u, auth, &p); err != nil {
			return nil, fmt.Errorf("bitbucket server: %w", err)
		}
		for _, v := range p.Values {
			pr := PR{
				Number: v.ID, Title: v.Title, Draft: v.Draft, State: serverState(v.State),
				Source: v.FromRef.DisplayID, Dest: v.ToRef.DisplayID,
			}
			if len(v.Links.Self) > 0 {
				pr.URL = safeURL(v.Links.Self[0].Href)
			}
			if v.UpdatedDate > 0 {
				pr.UpdatedAt = time.UnixMilli(v.UpdatedDate)
			}
			for _, r := range v.Reviewers {
				switch r.Status {
				case "APPROVED":
					pr.Approvals++
				case "NEEDS_WORK":
					pr.ChangesRequested++
				}
			}
			pr.Review = reviewFrom(pr.Approvals, pr.ChangesRequested, len(v.Reviewers))
			prs = append(prs, pr)
		}
		if p.IsLastPage {
			break
		}
		start = p.NextPageStart
	}
	return prs, nil
}

func serverState(s string) string {
	switch s {
	case "MERGED":
		return StateMerged
	case "DECLINED":
		return StateDeclined
	}
	return StateOpen
}
