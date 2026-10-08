# Telling Claude about workstation

Paste this into your global `~/.claude/CLAUDE.md` (or a project's `CLAUDE.md`). It teaches every session
to start from what was decided about its ticket, to park side findings instead of chasing them, and to leave a
handoff behind. Workstation must be running; the commands talk to it on `127.0.0.1:7420` (override with
`-addr host:port` or `WORKSTATION_ADDR`).

````markdown
## Workstation (ticket tracking across my Claude sessions)

I run several sessions in parallel, one ticket per worktree. A tool called `workstation` keeps what I decide
about each ticket. From inside the worktree you work in:

- **At the start**, run `workstation ticket`. It prints this ticket's state, the next step someone wrote,
  and the discoveries already noted. Start from the next step. If it says `blocked`, tell me instead of working.
- **When you notice something that is not part of this ticket** (another bug, a refactor, a missing test elsewhere,
  a doubt about the design): do NOT fix it and do NOT widen the scope. Run
  `workstation discover "one line saying what it is and where"` and carry on with the ticket.
  I decide later whether it is dropped, folded in, or becomes its own ticket.
- **Before you stop or hand over**, run `workstation next "what to do first next time, in one or two lines"`
  (what is done, what is left, any risk). Do not rely on this chat being resumable.

If a command says workstation is not running, skip it and tell me once; do not retry in a loop.
If you cannot run commands, write the discovery as a line of its own that starts with `DISCOVERY:` in your
reply; workstation reads those lines from the transcript.
````

## What each command does

| Command | Effect |
|---|---|
| `workstation ticket` | Prints the ticket of the current worktree: state, branch, blocked reason, next step, open discoveries. Changes nothing. |
| `workstation discover "text"` | Adds a discovery (one line, 300 characters at most) to that ticket. Always lands "open": you triage it in the board (drop / in scope / new ticket, which is created *parked*). |
| `workstation next "text"` | Replaces that ticket's next step (1000 characters at most). |

Lines in a chat that start with `DISCOVERY:` are picked up from the transcript too (only from the last 64 KB of
it, so they are best written as they happen). They land as open discoveries, once; a dropped one never comes back.

The ticket is decided by the server, from the branch of the worktree: a tracker key in the branch name
(`feature/REG-123-x` is `REG-123`), otherwise the branch itself.
