# workstation

[![ci](https://github.com/baiest/workstation/actions/workflows/ci.yml/badge.svg)](https://github.com/baiest/workstation/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Mission control for your Git worktrees and Claude Code sessions.**

If you run several Claude Code sessions in parallel, each in its own `git worktree`, it is easy to lose track of
which worktree belongs to which session, what state each one is in, which branches depend on which, and which
have a pull request. `workstation` puts that on one page, served from a single local binary. No cloud, no
accounts, no database.

For every worktree it shows the repository, branch, path, Git state (staged / modified / untracked / conflicts,
ahead / behind), last commit, and the linked Claude session (status, last activity, title, last message).

- **Worktrees** — every worktree of every repo, with its Claude session and PR status.
- **Plan button** — read the plan a Claude session produced, rendered as Markdown.
- **Branch graph** — the active branches of a repo as a tree: which depends on which, with PR status
  (open / draft / merged / declined), approvals, review state and checks. Zoom (buttons, **Ctrl + wheel**, *Fit*) and
  search (branch, worktree, PR title or `#number`; **Enter** jumps to the next match).
- **Tickets tab** — everything of a ticket (`REG-5393`) in one place: its branches, worktree folders, PRs, git state
  and Claude sessions, one row per branch. Answers "which worktree is for which branch and PR".
- **Readable worktree cards** — titled by the PR title (else the branch) with a ticket badge, the folder as its own row,
  ordered by what needs attention (live Claude, uncommitted changes, open PR) with merged ones last, and a
  "merged · removable" hint. Branches with a commit in the last 3 days glow in the graph.
- **Branch cleanup** — one button deletes local branches whose PR was merged long ago, after you review the list
  ([details](#branch-cleanup)).
- **Worktree cleanup** — one button removes worktree folders whose PR merged and that hold nothing else
  ([details](#worktree-cleanup)).
- **Hide projects** you do not care about (**Hide** next to the project name; restore them from "Hidden projects").
  Remembered in the browser; hidden projects are not queried for PRs.
- **UI size** with **A−** / **A+** in the header (also browser zoom).
- **Pull requests** from GitHub, Bitbucket Cloud and Bitbucket Server / Data Center.
- **Actions** — open a worktree in a terminal or Cursor / VS Code, or resume its Claude session.

> **Unofficial.** Not affiliated with or endorsed by Anthropic. It reads local files that Claude Code and Claude
> Desktop write, in formats that are **not documented** and may change; see [Dependencies on Claude internals](#dependencies-on-claude-internals-and-limitations).
> Status shown is only what can be determined reliably, otherwise `unknown`.

## Install

### Option 1: download a release (no toolchain needed)

Grab the archive for your platform from the [Releases](https://github.com/baiest/workstation/releases) page
(macOS Apple Silicon / Intel, Linux, Windows), then:

```sh
tar xzf workstation_*_darwin_arm64.tar.gz
./workstation
```

macOS may refuse an unsigned binary downloaded through a browser. Clear the quarantine flag once:

```sh
xattr -d com.apple.quarantine ./workstation
```

### Option 2: build from source

Requirements: Go 1.25.14 or newer (older 1.25 patches have known standard-library vulnerabilities; Go downloads the
right toolchain automatically), Node 22, pnpm 10, `git`. On macOS: `brew install go node pnpm`.

```sh
git clone https://github.com/baiest/workstation.git
cd workstation
make build          # builds the frontend, embeds it, writes bin/workstation
./bin/workstation   # http://127.0.0.1:7420
```

`make install` copies the binary to `/usr/local/bin` (`PREFIX=~/.local make install` for a user directory).
`go install` is not supported: the web UI is built separately and embedded into the binary, so use `make build`.
Without `make` (Windows), run the commands from the [Makefile](Makefile): `pnpm -C web install`,
`pnpm -C web build`, `go build -o workstation ./cmd/workstation`.

## Run

```sh
workstation                                   # http://127.0.0.1:7420
workstation -addr 127.0.0.1:8000              # another port (loopback addresses only)
workstation -config ~/.workstation.json       # config file (optional)
workstation -version
```

Open the URL and press **Refresh** (or `r`). `/` focuses the filter, `Esc` clears it. Data is recomputed on every
refresh; there is no polling or WebSocket.

### Keep it running on macOS (optional, untested)

A launch agent starts it at login. `launchd` has a minimal `PATH`, so list where `git`, `gh`, `claude` and your editor live:

`~/Library/LaunchAgents/dev.workstation.plist`

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>dev.workstation</string>
  <key>ProgramArguments</key><array><string>/usr/local/bin/workstation</string></array>
  <key>EnvironmentVariables</key><dict>
    <key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>
```

```sh
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.workstation.plist
```

### Not reachable from the network

The server only listens on loopback and **refuses** any other `-addr` (`0.0.0.0`, an empty host, a LAN address). It
has no authentication and its buttons start processes on your machine, so it is deliberately not exposed. To use it
from another device, tunnel to it (for example `ssh -L 7420:127.0.0.1:7420 your-machine`).

### Logs

Operational problems are written to **stderr** (startup, and the real cause of any internal error, which the browser
only sees as "internal error"). Nothing is written to a file: run it in a terminal, or redirect it
(`workstation 2>> workstation.log`). If you start it hidden or as a service, the output is discarded unless you
redirect it.

### Optional config: `~/.workstation.json`

```json
{
  "repos": ["/Users/me/code/some-repo"],
  "forges": [
    { "host": "bitbucket.org", "type": "bitbucket-cloud", "userEnv": "BITBUCKET_USER", "tokenEnv": "BITBUCKET_TOKEN" },
    { "host": "bb.mycorp.com", "type": "bitbucket-server", "tokenEnv": "BB_SERVER_TOKEN", "baseUrl": "https://bb.mycorp.com" }
  ]
}
```

Repositories are discovered automatically from the working directories of Claude sessions. `repos` lists extra
repositories to show even if no Claude session ever ran in them. `forges` is only needed for Bitbucket (see
[Pull requests](#pull-requests)); secrets are **never** stored in this file, only the names of environment variables.

## Where the data comes from

### Git (official CLI, no custom parsing of `.git`)

| Info | Command |
|---|---|
| repos / worktrees / paths / branch / HEAD | `git worktree list --porcelain` (first entry = main worktree) |
| staged / modified / untracked / conflicts, ahead/behind | `git status --porcelain=v2 --branch` |
| last commit | `git log -1 --format=%h%x00%s%x00%cI` |
| which worktree a directory belongs to | `git rev-parse --show-toplevel` |
| branches, tips, merged state, ancestry | `git for-each-ref`, `git branch --no-merged`, `git merge-base --is-ancestor`, `git rev-list` |

Runs with `GIT_OPTIONAL_LOCKS=0` so refreshing never takes `index.lock` from your own git commands.
"Ahead/behind" is only shown when the branch has an upstream.

### Claude Code sessions (undocumented internal files)

All of this lives in `internal/claude` behind the `ClaudeSessionProvider` interface. Nothing else in the app
knows these formats.

**Claude Code CLI** — config dir `~/.claude` (or `$CLAUDE_CONFIG_DIR`):

| File | Used for |
|---|---|
| `projects/<encoded-cwd>/<sessionId>.jsonl` | One transcript per session. Fields read: `cwd`, `gitBranch`, `slug` (head/tail records), `timestamp` (last record = last activity), last `assistant` text block (= last message). Only the first 50 lines and the last 64 KB are read. |
| `sessions/<pid>.json` | One file per *running* `claude` process: `pid`, `sessionId`, `status`, `name`. Used for live status and title. |
| `plans/<slug>.md` | The plan of a session (see [Plans](#plans)). |

The `<encoded-cwd>` directory name is **never decoded** (the encoding is lossy); the real path comes from the
`cwd` field inside the transcript.

**Claude Desktop (Code tab)** — `claude-code-sessions/<account>/<org>/local_<id>.json` under the Desktop data dir:

- Windows (MSIX, verified): `%LOCALAPPDATA%\Packages\Claude_*\LocalCache\Roaming\Claude`; also `%APPDATA%\Claude`
- macOS (**not verified**): `~/Library/Application Support/Claude`

Fields read: `sessionId`, `cliSessionId` (links to the CLI transcript), `cwd`, `originCwd`, `sourceBranch`, `title`,
`lastActivityAt`, `isArchived` (archived sessions are skipped). A Desktop session with a matching CLI transcript is
merged into one session (Desktop title wins); otherwise it appears on its own with unknown status.

### Plans

Every transcript record carries a `slug` field (e.g. `bien-ubicarte-en-workstation-ancient-puffin`); the plan of
that session is `<claude config dir>/plans/<slug>.md`. A session shows the **Plan** button only when that file
exists. The API takes a session id, never a path; the slug must match `[a-z0-9-]+` and the file is capped at 1 MB.
The Markdown is rendered with raw HTML escaped, non-http(s) links dropped and images never loaded.
Desktop-only sessions have no slug, so no plan.

### Branch graph and dependencies

Branches are local branches (`git for-each-ref refs/heads`). A branch is **active** — drawn — when it has a
worktree, or an open PR, or is not merged into the default branch (`git branch --no-merged`) and its PR is not
already merged. The toggle "Show recently merged" adds branches whose PR merged in the last 30 days. At most 40
nodes are drawn; the rest are counted ("N more branches hidden"), worktree and open-PR branches kept first.

The default branch is `origin/HEAD`, else local `main` / `master`.

A branch's parent (the arrow into it) is decided in this order, and the graph says which one applied:

1. **PR base** (solid line): the destination branch of its PR, if that branch is in the graph.
2. **Git history** (dashed line): the closest other graph branch whose tip is an ancestor
   (`git merge-base --is-ancestor`, fewest commits in between). Identical tips are ambiguous, so only the default
   branch may claim them. If nothing qualifies, the parent is the default branch.

Limits of the inference: a branch cut from a commit that its parent has since moved past (rebase, force-push)
no longer has that parent as an ancestor, so it falls back to the default branch.

### Branch cleanup

**Branches tab → "Clean up branches…"** opens a preview; nothing is deleted until you confirm. A local branch is
offered only when **all** of these hold:

- its pull request is **merged** (from GitHub / Bitbucket; with no PR data nothing is offered),
- it was merged at least **N days ago** (default 30, editable),
- the branch **has no commits beyond what the PR merged**: its tip is the PR's head commit, or it is already
  contained in the default branch,
- it is not the default branch, has no worktree checked out, and has no newer open PR with the same name.

Everything else with a merged PR is listed under "Kept" with the reason. You untick what you want to keep and confirm.

Safety, enforced on the server and not just in the page:

- the request is never trusted: eligibility is **recomputed with fresh PR data** at deletion time, and each branch is
  deleted only if it **still points at the commit you were shown** (otherwise "the preview is out of date");
- only **local** branches are deleted (`git branch -D`, which git itself refuses for a checked-out branch).
  Remote branches are never touched;
- afterwards it prints `git branch <name> <sha>` for each deleted branch; the commits stay in git until garbage
  collected, so that restores it.

Limits: only the last 100 GitHub PRs are known, so older branches are not offered; Bitbucket Cloud's payload has no
merge date, so its last-update time is used instead.

### Worktree cleanup

**Worktrees tab → "Clean up worktrees…"** previews the linked worktrees that can go, with a checkbox each; nothing
changes until you confirm. A worktree is offered only when **all** of these hold:

- its branch has a **merged** PR, merged at least **N days ago** (default 7, editable);
- **nothing is uncommitted or untracked** in it, and **no Claude session is running** in it;
- it has **no commits beyond what the PR merged** (HEAD is the PR's head commit, or already inside the default branch);
- it is a linked worktree: never the main one, a detached one, or the default branch's.

Removing **deletes the folder**; the branch is kept (delete it afterwards with "Clean up branches…"). It is enforced on the
server, using a freshly built workspace and fresh PR data at the moment of removal:

- each worktree must still be a candidate and still at the commit you were shown;
- a fresh `git status` must be clean (a file created after the preview stops it);
- git is never asked to `--force`, so it refuses locked worktrees and any with modified or untracked files.

### Pull requests

The Branches tab loads in **two steps** so it never waits for the network: first the graph from Git alone
(`GET /api/branches?prs=0`, no forge call), then the full graph with pull requests, which replaces it while the page
shows "Loading pull requests…" (nodes without a known PR show `PR…` instead of "no PR"). Branches whose PR already
merged (squash merges) only disappear from the graph once the second step arrives. PRs are cached for 60 s per repo;
Refresh bypasses the cache. If the lookup fails, the graph stays as drawn from Git and a warning says why. The forge
is chosen from the `origin` remote URL:

| Host | How | Needs |
|---|---|---|
| `github.com` | `gh pr list` (last 100 PRs, plus a second call for the checks of open PRs) | `gh` installed and logged in |
| `bitbucket.org` | REST v2 `/2.0/repositories/{workspace}/{repo}/pullrequests` | `BITBUCKET_USER` (account email) + `BITBUCKET_TOKEN` (API token) env vars; without a user, the token is sent as a Bearer access token |
| other host in `forges` | Bitbucket Server / DC REST `/rest/api/1.0/projects/{P}/repos/{r}/pull-requests` | the env var named by `tokenEnv` (HTTP access token) |

Credential environment variables must be upper case and start with `BITBUCKET_`, `BB_`, `ATLASSIAN_` or
`WORKSTATION_`, so a tampered config cannot make the app read an unrelated secret. `baseUrl` must be `https` and on the
same host as the forge entry. API responses are capped at 8 MiB and PR links are kept only if they are `http(s)`.

Unknown hosts and repos without `origin` are silently skipped. Reported per PR: state, draft, number, title, link,
approvals, changes requested, review state, checks.

**Bitbucket is not verified against a live instance**: it is implemented from the documented API shapes and tested
against fixtures only. If a field is off on first use, `internal/forge/bitbucket.go` is the only file to adjust.
Bitbucket **checks/builds are not shown** (one extra request per PR); approvals, changes requested and state are.
For Server / DC the clone URL host is assumed to serve the REST API at `https://<host>` unless `baseUrl` is set
(needed with a context path, e.g. `https://host/bitbucket`).

### Session ↔ worktree relationship

Not assumed. A session is linked to a worktree only if its `cwd` is exactly a worktree path from
`git worktree list`, or `git rev-parse --show-toplevel` run in that `cwd` returns one (sessions started in a
subdirectory). A path-prefix match is deliberately **not** used: a session from a deleted
`repo/.claude/worktrees/x` would wrongly attach to the main worktree.

Anything else goes to **Unlinked sessions**, with a reason: directory no longer exists, not a git repo, or not a
listed worktree. Several sessions in one worktree are all kept; the newest is shown, the rest under "+N more".

### Claude status — only what can be determined reliably

| Shown | When |
|---|---|
| Working | a live process file says `status: "busy"` and its pid is alive |
| Idle | a live process file says `status: "idle"` and its pid is alive |
| Stopped | transcript exists, no live process |
| Unknown | live process with any other status value (raw value in tooltip), or a Desktop session with no CLI match |

## Dependencies on Claude internals (and limitations)

- Everything under "Claude Code sessions" above is an undocumented, internal format and may change between
  Claude releases. If it does, only `internal/claude` needs to change.
- Only `busy` was observed in `sessions/<pid>.json`; `idle` is mapped by name but was not observed. Other values are shown as Unknown.
- **"Waiting / needs input" is not implemented**: no verified signal exists for it.
- Liveness is "pid exists". If the OS reuses a dead session's pid for another process it can be reported as live.
- A brand-new session has no transcript until its first message, so it does not appear until then.
- Last message is the last assistant text found within the final 64 KB of the transcript; it can be empty after a long tool output.
- The plan feature relies on the `slug` field in transcripts and the `plans/<slug>.md` layout. A plan file is
  overwritten as the plan is edited, so the button shows its latest content, not a history.
- Developed and tested on Windows; CI also runs the Go tests on Linux and macOS. **macOS paths for Claude Desktop and the
  macOS terminal / resume commands are unit-tested only, never run on a real Mac by the author.**
- Desktop-only sessions with no `cliSessionId` cannot be resumed from here.
- Sessions of deleted worktrees cannot be resumed (`claude --resume` must run in the original directory).
- Only the last 100 GitHub PRs are looked at: a PR older than that shows as "no PR".
- On a big repo the graph from Git appears in about a second and the pull requests a second or two later (the two
  `gh` calls run in parallel); later loads within a minute are served from the cache.

## Actions and security

Open terminal / editor / resume launch local processes, so the server is locked down. See [SECURITY.md](SECURITY.md)
for the threat model and how to report a problem.

- Loopback only: it refuses to start on any other address, and requests whose `Host` is not loopback are rejected
  (DNS rebinding). `Sec-Fetch-Site` must be same-origin or none; if `Origin` is sent it must match `Host`; POSTs must be
  `application/json`.
- Every response carries a CSP (`script-src 'self'`, `frame-ancestors 'none'`), `X-Frame-Options: DENY`, `nosniff`,
  `Referrer-Policy: no-referrer`.
- Actions only accept worktree paths and session ids present in the last computed workspace; session ids must match
  `[A-Za-z0-9_-]+`; the directory must exist, be absolute and contain no control characters.
- Terminal: Windows starts `powershell.exe -NoExit` in a new console with the directory as its **working directory**
  (never through `cmd.exe`, which would re-parse `&`, `%`, `^` in a path); macOS `open -a Terminal` / `osascript` with
  the path quoted. Editor: `cursor`, else `code`, if on PATH (button hidden otherwise); a `.cmd` shim refuses paths
  containing `& | < > ^ % " !`. Resume: a terminal in the session's directory running `claude --resume <id>`.
- git runs with `core.fsmonitor`, hooks and the pager disabled (a repo's own config can name commands), a 30 s timeout
  that kills the whole process tree, and branch names always qualified as `refs/heads/...`. `gh` has a 25 s timeout.
- The only destructive action is branch cleanup (see [Branch cleanup](#branch-cleanup)): local branches only,
  re-verified server-side, with the previewed commit as a compare-and-delete guard.
- Local files are read with size limits (1 MiB for JSON, 1 MiB per transcript line), and a symlink in `plans/` is
  not served as a plan.
- **Accepted risk: no authentication.** Any process or user on the same machine can read the data and press the
  buttons. Use it on a machine and account you trust.

## Development

```sh
make dev                 # backend on :7420
pnpm -C web dev          # frontend with hot reload on :5173, proxies /api to :7420
make test lint
```

```
cmd/workstation/      main: wiring, flags
internal/gitx/        git CLI wrapper + porcelain parsers
internal/claude/      ClaudeSessionProvider: CLI + Desktop readers, merge, pid check, plan files
internal/workspace/   discovery + session<->worktree join
internal/forge/       pull requests: GitHub (gh), Bitbucket Cloud, Bitbucket Server
internal/branches/    branch dependency graph + cached PR lookups
internal/server/      HTTP API (/api/workspace, /plan, /branches, /actions), security guard, launcher
internal/config/      ~/.workstation.json
web/                  Vue 3 + TypeScript + Vite app; web/dist is embedded via go:embed
```

See [CONTRIBUTING.md](CONTRIBUTING.md). Releases are built by GoReleaser when a `v*` tag is pushed
(`git tag v0.1.0 && git push --tags`).

## License

[MIT](LICENSE)
