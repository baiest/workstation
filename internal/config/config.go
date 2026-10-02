// Package config loads the optional ~/.workstation.json.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"workstation/internal/safeio"
)

const maxConfigBytes = 1 << 20

type Config struct {
	// Repos are listed even when no Claude session ever ran in them.
	Repos []string `json:"repos"`
	// Forges tell the app how to reach pull requests on hosts it cannot infer.
	Forges []Forge `json:"forges"`
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
	return c, nil
}
