// Command workstation serves a local dashboard of Git worktrees and the
// Claude Code sessions that ran in them.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"workstation/internal/branches"
	"workstation/internal/claude"
	"workstation/internal/config"
	"workstation/internal/forge"
	"workstation/internal/notes"
	"workstation/internal/server"
	"workstation/internal/workspace"
	"workstation/internal/wtclean"
	"workstation/web"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	addr := flag.String("addr", "127.0.0.1:7420", "listen address; must be a loopback address (the server has no authentication)")
	cfgPath := flag.String("config", config.DefaultPath(), "optional config file")
	cacheDir := flag.String("cache-dir", defaultCacheDir(), "where pull request data is kept between runs (private to you); empty disables")
	statePath := flag.String("notes-file", defaultNotesFile(), "where stars and notes are kept (private to you); empty keeps them in memory only")
	flag.Parse()

	if *showVersion {
		fmt.Println("workstation", version)
		return
	}
	if err := run(*addr, *cfgPath, *cacheDir, *statePath); err != nil {
		fmt.Fprintln(os.Stderr, "workstation:", err)
		os.Exit(1)
	}
}

// defaultCacheDir is <user cache dir>/workstation/prs, or "" if the OS has none.
func defaultCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "workstation", "prs")
}

// defaultNotesFile is <user config dir>/workstation/notes.json, or "" if the OS has none.
func defaultNotesFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "workstation", "notes.json")
}

func run(addr, cfgPath, cacheDir, notesFile string) error {
	if err := server.RequireLoopback(addr); err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	cli := claude.NewCLI(claude.DefaultCLIRoot(), claude.PidAlive)
	provider := claude.Combined{CLI: cli, Desktop: claude.NewDesktop(claude.DesktopRoots())}
	builder := workspace.Builder{Provider: provider, ExtraRepos: cfg.Repos}

	branchSvc := branches.NewService(forge.DefaultDeps(cfg.Forges))
	if cacheDir != "" {
		branchSvc.Store = branches.NewDiskStore(cacheDir)
	}
	handler := server.New(builder.Build, server.OSLauncher{}, web.Dist(),
		server.WithPlans(cli),
		server.WithBranches(branchSvc),
		server.WithCleanup(branchSvc),
		server.WithNotes(notes.New(notesFile)),
		server.WithWorktreeCleanup(&wtclean.Service{PRs: branchSvc.PullRequests}),
	)
	log.Printf("workstation listening on http://%s", addr)
	return server.NewHTTPServer(addr, handler).ListenAndServe()
}
