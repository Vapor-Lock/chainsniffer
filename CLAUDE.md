# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:1105d646 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/core-concepts/sync-concepts.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->


## Build & Test

`nix flake check` is the gate before merging. It runs everything offline in the Nix sandbox:
`go test ./...` (through `buildGoModule`'s check phase), `go vet ./...`, `golangci-lint run ./...`
and a build of the container image.

```bash
nix flake check    # the merge gate: build + tests, vet, lint, image
nix build          # the binary at ./result/bin/chainsniffer
nix build .#image  # the OCI image; docker load < result
go test ./...      # fast loop in the devshell
go test ./internal/relay -run TestHandler -v
gofmt -l .         # golangci-lint enforces gofmt; this is the quick check
```

- **Nix only sees files git tracks:** `git add` a new file before `nix build` or `nix flake check`,
  or the build fails on a missing file.
- **`vendorHash` is pinned** in `flake.nix`. Any change to `go.mod` or `go.sum` invalidates it and
  `nix build` fails with a hash mismatch. To update: set the value to `pkgs.lib.fakeHash`, run
  `nix build`, and copy the `got:` hash back in (`nix build 2>&1 | grep got:`).
- **Tests never touch the network.** Everything external is faked with `httptest` or an interface
  the package defines itself. Keep it that way — a test that dials out fails in the sandbox.

## Architecture Overview

chainsniffer is a single Go binary that relays killmails from one Wanderer map's SSE stream to one
Discord channel. See `README.md` for operation and `openspec/changes/archive/2026-10-14-add-killmail-relay-bot/design.md`
for the Wanderer wire format and the reasoning behind the decisions below.

```
cmd/chainsniffer    wiring, signals, exit codes (0 clean, 1 operator failure, 2 bad config)
internal/config     env + _FILE parsing, validation, redacted summary
internal/sse        generic SSE reader (framing only, io.Reader -> Event)
internal/wanderer   map stream client: requests, backoff, staleness watchdog, fatal statuses
internal/killmail   payload decoding, the Killmail type, classification, filters
internal/discord    embed rendering, poster (rate limits, retries), channel/permission check
internal/universe   ESI system-name resolver with an in-memory cache
internal/state      state.json: last event ID + bounded set of posted killmail IDs
internal/topology   map graph from the /systems snapshot + topology events; systems linked to home
internal/relay      the pipeline: event -> killmails -> filter/dedupe -> post -> persist cursor
```

Data flow: `wanderer` holds one SSE connection and hands each envelope to `relay`, which decodes
its killmails, drops the ones that are duplicates or fail the filters, posts the rest through
`discord`, and only then advances the cursor in `state` and saves. A crash mid-envelope replays it;
the dedupe set makes that at-least-once processing with at-most-once posting.

## Conventions & Patterns

- **Packages depend only on interfaces they declare themselves** (`Poster`, `Resolver`,
  `Store`). `relay` composes the concrete types; everything else is unit-tested with fakes.
  Keep new dependencies pointing inward this way.
- **One instance per map.** There is no coordination between processes; two would double-post.
- **Secrets live in `config.Secret`,** whose `String` and `LogValue` return `[redacted]`. Call
  `Reveal()` only where the value goes out on the wire.
- **Fatal versus retryable is a type, not a string.** `wanderer.FatalError` and the Discord
  poster's classification decide whether the process exits 1 or backs off and retries.
- **Logging is `log/slog`** on the logger threaded down from `main` via each package's
  `WithLogger` option. No package logs on its own.

<!-- BEGIN DEVFLAKE (managed by devflake; devflake-upgrade refreshes it, edit outside this block) -->
## Development environment (devflake)

The devshell comes from the `devflake` flake input. `nix develop` (or direnv) provides the
project's toolchain plus `bd` (beads), `openspec`, `claude`, `herdr` and `herdr-agents`, and gives
the project a workspace in the herdr session (attach with `herdr`).

- Workflow: an OpenSpec change (`openspec/changes/<change>/`) becomes a beads epic with a
  dependency graph of session-sized children (the `openspec-to-epic` skill), which agents work
  through in parallel (`herdr-agents spawn N --preset bd-epic --var epic=<id> --loop`).
- devflake moves with `nix run github:slwst/devflake#upgrade [REF]`, not a hand edit of the
  `devflake` input in `flake.nix`: it also refreshes this block and the devflake skills.
- `nix flake check` is the gate before merging: it runs the tests and linters offline in the Nix
  sandbox. `nix build` builds the project.
- Nix only sees files git tracks: `git add` new files before `nix build` or `nix flake check`.
- Agents in `.worktrees/<name>` claim and release beads with `herdr-agents claim|release`,
  deliver work with `herdr-agents merge`, and never commit `.beads/`.
- Beads is published, not private scratch. Bead text and `bd remember` memories share one
  database that `bd dolt push` replicates to the project's remote, often the code remote and
  possibly public; no flag withholds memories from a push, and a push ships history, so editing
  or deleting a row later does not unpublish it. Keep credentials, tokens, customer data and
  private hostnames out of beads — reference where a value lives, never the value.
- Publish beads the first time with `bd dolt push`, which creates `refs/dolt/data` on the remote;
  `bd sync` fails until it exists. Use `bd sync` thereafter: it pulls, checks for conflicts and
  pushes with retry, so it copes with another replica having moved the remote.
- `bd update --notes` **replaces** a bead's notes; use `--append-notes` to add to them.
- The herdr session is shared with every other devflake project open on the host, each in its own
  workspace, and herdr scopes nothing within a session. Panes outside this repository's workspaces
  are not yours: check `worktree.repo_root` in `herdr workspace list` before acting on a pane.
- The herdr tooling belongs to devflake. Project-specific agent presets go in
  `.herdr/prompts/<name>.md` (they shadow built-in presets of the same name), and notes for
  the captain tab in `.herdr/captain.md`.
<!-- END DEVFLAKE -->
