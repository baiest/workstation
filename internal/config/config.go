// Package config loads the optional ~/.workstation.json.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"workstation/internal/safeio"
)

const maxConfigBytes = 1 << 20

type Config struct {
	// Repos are listed even when no Claude session ever ran in them.
	Repos []string `json:"repos"`
	// Forges tell the app how to reach pull requests on hosts it cannot infer.
	Forges []Forge `json:"forges"`
	// TicketLink turns a ticket key into a link to wherever you track your work
	// (Jira, GitHub Issues, Trello, Taiga...): "https://tracker.example.com/browse/{key}".
	// Workstation never calls that address; it only shows the link.
	TicketLink string `json:"ticketLink,omitempty"`
	// TicketPattern says what a ticket key looks like in a branch name: a regular
	// expression with one capture group (the key). Default: Jira style, REG-123.
	TicketPattern string `json:"ticketPattern,omitempty"`
}

// Forge describes a code host. Secrets are never stored here: UserEnv and
// TokenEnv only name the environment variables that hold them.
type Forge struct {
	Host     string `json:"host"`
	Type     string `json:"type"` // "bitbucket-cloud" | "bitbucket-server"
	UserEnv  string `json:"userEnv,omitempty"`
	TokenEnv string `json:"tokenEnv,omitempty"`
	BaseURL  string `json:"baseUrl,omitempty"` // server only; default https://<host>
}

// DefaultPath is ~/.workstation.json.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".workstation.json")
}

// Load reads path; a missing file is not an error.
func Load(path string) (Config, error) {
	data, err := safeio.ReadFile(path, maxConfigBytes)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// validate checks what can be checked without the rest of the app. The ticket
// pattern is compiled where it is used (tickets.NewKeyer).
func (c Config) validate() error {
	if c.TicketLink == "" {
		return nil
	}
	if !strings.Contains(c.TicketLink, "{key}") {
		return errors.New(`ticketLink must contain {key}, e.g. "https://tracker.example.com/browse/{key}"`)
	}
	u, err := url.Parse(strings.ReplaceAll(c.TicketLink, "{key}", "KEY-1"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("ticketLink must be an http(s) address")
	}
	return nil
}
