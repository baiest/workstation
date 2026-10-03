// Command workstation serves a local dashboard of Git worktrees and the
// Claude Code sessions that ran in them.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"workstation/internal/branches"
	"workstation/internal/claude"
	"workstation/internal/config"
	"workstation/internal/forge"
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
	flag.Parse()

	if *showVersion {
		fmt.Println("workstation", version)
		return
	}
	if err := run(*addr, *cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "workstation:", err)
		os.Exit(1)
	}
}

func run(addr, cfgPath string) error {
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
	handler := server.New(builder.Build, server.OSLauncher{}, web.Dist(),
		server.WithPlans(cli),
		server.WithBranches(branchSvc),
		server.WithCleanup(branchSvc),
		server.WithWorktreeCleanup(&wtclean.Service{PRs: branchSvc.PullRequests}),
	)
	log.Printf("workstation listening on http://%s", addr)
	return server.NewHTTPServer(addr, handler).ListenAndServe()
}
