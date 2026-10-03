# Security policy

`workstation` runs on your machine, reads your Claude Code transcripts, plans and git repositories, can start
terminals and editors, and (optionally) calls GitHub / Bitbucket with your credentials. Please report security
problems privately.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting:
<https://github.com/baiest/workstation/security/advisories/new>

Please do not open a public issue for something exploitable. Include the version (`workstation -version`), your OS,
and the steps to reproduce. You can expect an acknowledgement within a few days; this is a personal project, so
there is no formal SLA.

## Supported versions

Only the latest release and `main` receive fixes.

## Threat model

What the app defends against:

- **Other websites** reaching the local server (DNS rebinding, cross-site requests, clickjacking): loopback-only
  `Host`, `Sec-Fetch-Site` and `Origin` checks, JSON-only POSTs, CSP and `frame-ancestors 'none'`.
- **Hostile directory or branch names** (`&`, `%`, quotes, control characters) reaching a shell: terminals are started
  without `cmd.exe`, the directory is the working directory, editor `.cmd` shims refuse metacharacters.
- **Hostile repositories**: git runs with `core.fsmonitor`, hooks and the pager disabled and with a timeout; branch
  names are always qualified as `refs/heads/...`.
- **Hostile API responses**: bounded response size, `http(s)`-only PR links, pagination never leaves the original host,
  credentials only from environment variables with an allowed prefix and only to the configured `https` host.
- **Resource exhaustion**: server and process timeouts, bounded file and line reads, one shared workspace build.
- **The destructive actions, branch and worktree cleanup**: they only delete local branches, or linked worktree
  folders, whose PR is merged and that hold no extra commits (worktrees also need a clean `git status` and no live
  Claude session). Everything is re-checked server-side with fresh data, an item is deleted only if it is still at the
  commit shown in the preview, git is never asked to `--force`, and remotes, the default branch, checked-out branches and
  the main worktree are never touched.

Deliberately **not** defended (accepted risk):

- **No authentication.** Any process or user on the same machine can read the data and use the action buttons.
  Run it only on a machine and account you trust. For this reason it refuses to listen on anything but loopback.
- No TLS (loopback only).

## Verifying a release

Release archives come with `checksums.txt` and a GitHub build-provenance attestation:

```sh
shasum -a 256 -c checksums.txt --ignore-missing
gh attestation verify workstation_<version>_<os>_<arch>.tar.gz --repo baiest/workstation
```
