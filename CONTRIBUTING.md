# Contributing

Thanks for taking a look. This is a small personal tool, so the bar is simple: keep it small and keep it honest.

## Setup

Go >= 1.25, Node 22, pnpm 10, `git`. Optional: the `gh` CLI (GitHub PRs).

```sh
make deps      # frontend dependencies
make test      # Go + frontend tests
make lint      # gofmt, go vet, vue-tsc
make dev       # backend; run `pnpm -C web dev` next to it for hot reload
```

On Windows without `make`, run the underlying commands from the `Makefile` directly.

## Ground rules

- **Tests first.** New behaviour starts with a failing test (table tests and temp-repo integration tests are the
  house style). The existing suites show how.
- **Claude's local files are an undocumented format.** Everything that reads them lives in `internal/claude` behind
  `ClaudeSessionProvider`. Do not leak those formats elsewhere, and do not invent a status you cannot determine
  reliably: `unknown` is a valid answer.
- **Use the git CLI.** Parse its porcelain output; do not read `.git` internals.
- **Never put secrets in the config file.** It names environment variables (with an allowed prefix), nothing more.
- **Be careful with anything that launches a process.** Actions only accept values present in the last computed
  workspace; keep it that way (see `internal/server`). Paths and branch names come from git and transcripts, so
  treat them as hostile: never build a shell command line from them (Windows: no `cmd.exe`).
- **Do not expose the server.** It has no authentication and refuses non-loopback addresses on purpose; do not add a
  "listen on the network" option.
- **Run git and gh through `gitx` / `forge.runCommand`** so they get the hardening flags and timeouts.
- Security reports: see [SECURITY.md](SECURITY.md). `make test lint` plus `govulncheck ./...` should pass.
- Bitbucket support is built from documented API shapes and fixtures. If you have a real instance, fixes to
  `internal/forge/bitbucket.go` with a recorded fixture are very welcome.

## Pull requests

Keep them focused, say what changed and why, and make sure `make test lint` passes. CI also runs the Go tests on
Linux, macOS and Windows.
