// Command workstation serves a local dashboard of Git worktrees and the
// Claude Code sessions that ran in them.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	"workstation/internal/branches"
	"workstation/internal/claude"
	"workstation/internal/config"
	"workstation/internal/forge"
	"workstation/internal/server"
	"workstation/internal/workspace"
	"workstation/web"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	addr := flag.String("addr", "127.0.0.1:7420", "listen address (keep it on loopback)")
	cfgPath := flag.String("config", config.DefaultPath(), "optional config file")
	lan := flag.Bool("lan", false, "listen on all interfaces and accept private-IP hosts (no auth: trusted networks only)")
	flag.Parse()

	if *showVersion {
		fmt.Println("workstation", version)
		return
	}
	if err := run(*addr, *cfgPath, *lan); err != nil {
		fmt.Fprintln(os.Stderr, "workstation:", err)
		os.Exit(1)
	}
}

func run(addr, cfgPath string, lan bool) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	var opts []server.Option
	if lan {
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return err
		}
		addr = net.JoinHostPort("0.0.0.0", port)
		opts = append(opts, server.WithLAN())
	}

	cli := claude.NewCLI(claude.DefaultCLIRoot(), claude.PidAlive)
	provider := claude.Combined{CLI: cli, Desktop: claude.NewDesktop(claude.DesktopRoots())}
	builder := workspace.Builder{Provider: provider, ExtraRepos: cfg.Repos}

	opts = append(opts,
		server.WithPlans(cli),
		server.WithBranches(branches.NewService(forge.DefaultDeps(cfg.Forges))),
	)
	handler := server.New(builder.Build, server.OSLauncher{}, web.Dist(), opts...)
	log.Printf("workstation listening on http://%s", addr)
	return http.ListenAndServe(addr, handler)
}
