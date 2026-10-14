# Tasks

## 1. Go project and Nix gate

- [ ] 1.1 Add the Go toolchain (`go`, `gopls`, `golangci-lint`) to the devshell `packages` in `flake.nix`. Verify that `nix develop --command go version` and `nix develop --command golangci-lint --version` succeed.
- [ ] 1.2 Create `go.mod` (module `github.com/Vapor-Lock/chainsniffer`, current Go version), a stub `cmd/chainsniffer/main.go` that only prints its version, and `.golangci.yml` (default linters plus `gofmt`). Verify with `go build ./...` and `golangci-lint run`.
- [ ] 1.3 Define `packages.default` with `buildGoModule` (`CGO_ENABLED=0`, `-s -w`, `subPackages = [ "cmd/chainsniffer" ]`, pinned `vendorHash`). Verify that `nix build` produces `result/bin/chainsniffer`.
- [ ] 1.4 Add `checks.build` (the package, whose checkPhase runs `go test ./...`), `checks.vet` (`go vet ./...`) and `checks.golangci-lint`, all offline. Verify that `nix flake check` passes, and that a deliberately misformatted file makes it fail.

## 2. SSE framing (`internal/sse`)

- [ ] 2.1 Implement the SSE reader (`io.Reader` to events with `id`, `event`, `data`): blank-line dispatch, multi-line `data` joining, comment lines, CRLF/LF/CR line endings, and the leading space after the colon. Verify with table tests covering each rule, plus a fixture copied from Wanderer's `format_sse_event` output (`connected`, `map_kill`, `: keepalive`).
- [ ] 2.2 Have the reader report activity (any bytes, including comments) to a callback so a watchdog can reset on keepalives. Verify with a unit test showing that keepalive-only input fires the callback and yields no events.

## 3. Killmail model and filters (`internal/killmail`)

- [ ] 3.1 Define the envelope and killmail types, and lenient decoding of `map_kill` payloads: extract `killmail_update.killmails`, ignore `kill_count` and unknown types, and skip entries without an integer `killmail_id` with a warning. Verify with tests using fixture JSON shaped like Wanderer's `message_handler.ex` output, including a structure kill with no victim character.
- [ ] 3.2 Implement classification (loss, kill or neutral against friendly corporation and alliance ID sets, with victim precedence). Verify with tests for every scenario under `killmail-filtering` "Kill classification".
- [ ] 3.3 Implement the filter chain: NPC, minimum value, friendly-only and maximum kill age (with an injected clock, and missing `kill_time` kept). Each filter returns the name of what dropped a kill for debug logging. Verify with tests for every scenario under `killmail-filtering` for the NPC, minimum value, friendly-only and maximum age requirements.

## 4. Relay state (`internal/state`)

- [ ] 4.1 Implement `state.json` load and save: `last_event_id` plus an ordered posted-ID list of at most 1000 with O(1) membership, atomic writes (temp file, fsync, rename), creating the directory if missing, moving a corrupt file aside to `state.json.corrupt`, and an error for an unwritable directory. Verify with tests in `t.TempDir()` covering round-trip, the bound and eviction, the corrupt file, and a read-only directory.

## 5. Configuration (`internal/config`)

- [ ] 5.1 Parse all `CHAINSNIFFER_*` variables from `relay-runtime` with their defaults, `_FILE` secret variants (whitespace trimmed, both set is an error), friendly-only without friendly IDs rejected, and every error reported at once. Use a `Secret` type that redacts itself in `slog` and `fmt`. Verify with table tests over env maps, including a test that a logged config summary contains no token.

## 6. Wanderer stream client (`internal/wanderer`)

- [ ] 6.1 Build the request: `{url}/api/maps/{map}/events/stream?events=map_kill[&last_event_id=…]`, a Bearer header and `Accept: text/event-stream`. Verify with an `httptest.Server` test asserting the path, query and headers, with and without a cursor.
- [ ] 6.2 Implement the run loop: decode envelopes, call the handler synchronously, log `connected`, and skip malformed events with a warning. Verify with an `httptest` stream that sends `connected`, two `map_kill` events, a malformed event and a keepalive, and assert that the handler receives exactly the two kills in order.
- [ ] 6.3 Add reconnect with exponential backoff (1s doubling to a 60s cap, ±20% jitter, reset after the first event or keepalive), a 90s stale watchdog, and `last_event_id` on reconnect. Durations must be injectable for tests. Verify with tests showing that 503 and closed streams reconnect with growing delays and the correct cursor, and that a silent server triggers a reconnect.
- [ ] 6.4 Return a typed fatal error for 401, 402, 403 and 404, and for a 503 reporting SSE disabled (the plain-text `Server-Sent Events are disabled on this server` body or JSON code `SSE_GLOBALLY_DISABLED`). Log the status and server message, with an actionable hint for SSE disabled for the map or server. Verify with tests per case, including that 429 connection limits, a plain 503 and a 500 retry instead.

## 7. Discord output (`internal/discord`, `internal/universe`)

- [ ] 7.1 Implement the system-name resolver: ESI `/universe/systems/{id}/`, 3s timeout, success-only cache, fallback to the numeric ID. Verify with `httptest` tests for a hit, a cached second call, a timeout, and a 404.
- [ ] 7.2 Implement `Render(killmail, classification, systemName)` producing the embed: title and zKillboard link, ship render thumbnail, system, value (`412.5M ISK` style), attackers, victim and final-blow fields, color per classification, footer, and `kill_time` timestamp. Optional fields missing must degrade gracefully. Verify with golden tests for a typical kill, a loss, a structure kill, and a killmail with minimal fields.
- [ ] 7.3 Add `github.com/bwmarrin/discordgo` and implement the poster: send the embed, wrap 5xx and network errors in a 5-minute exponential retry budget, rely on discordgo for 429 and `retry_after`, and treat 401, 403 and 404 as typed fatal errors with a permissions hint. Update `vendorHash` in `flake.nix`. Verify with tests against an `httptest` server standing in for the Discord API (discordgo endpoint override) covering success, 502 then success, an exhausted budget, a 429 then success, and a 403 being fatal. `nix flake check` must still pass.
- [ ] 7.4 Implement the startup check (validate the token, the channel exists, and permissions when resolvable) and the zero-intent gateway session open/close. Verify with tests against the fake API showing that an unknown channel and a bad token return fatal errors before any post.

## 8. Relay pipeline and binary

- [ ] 8.1 Implement `internal/relay`. Per envelope: extract killmails, then dedupe against the state, filter (debug-log drops), resolve the system name, render, post, and mark each kill posted in order. Then set the cursor to the envelope ID and save the state. A kill whose retries are exhausted is logged and left unmarked. A fatal post error stops the relay. Verify with tests using fake poster, resolver and store: order is preserved, duplicates within and across envelopes are posted once, the cursor advances only after an envelope completes, and an exhausted kill is not recorded.
- [ ] 8.2 Wire `cmd/chainsniffer/main.go`: load config (exit 2 on config errors), set up `slog` per level and format, log the redacted config, load state, run the Discord startup check, then run the Wanderer client with the relay as handler. On SIGINT or SIGTERM, stop reading, let an in-flight post finish (up to 10s), save state and exit 0. Exit 1 on fatal Wanderer or Discord errors. Verify with an end-to-end test in `cmd/chainsniffer` (or `internal/relay`) that runs against fake Wanderer, Discord and ESI `httptest` servers: two kills arrive, one is filtered, one is posted, and after a restart against the same state dir with a backfilled duplicate nothing is reposted.

## 9. Container image and documentation

- [ ] 9.1 Add `packages.image` (`dockerTools.buildLayeredImage`: binary entrypoint, `cacert` and `SSL_CERT_FILE`, user `65532:65532`, `/var/lib/chainsniffer` volume owned by that user) and `checks.image`. Verify that `nix build .#image` succeeds, that `docker load < result` followed by `docker inspect` shows a non-root user and the volume, and that running the image without env vars exits 2 and names the missing variables.
- [ ] 9.2 Write `README.md`: what the bot does, Discord bot setup (permissions: View Channel, Send Messages, Embed Links), the Wanderer map API key and SSE enablement (server and map; wanderer.ltd has SSE disabled), the env var table, running the image with a state volume, the limitations (10-minute backfill window, final-blow-only classification, one instance per map), and the `vendorHash` update procedure. Fill in the Build & Test and Architecture sections of `CLAUDE.md` and `AGENTS.md`. Verify that the documented `nix build .#image` and `docker run` commands work as written.

## Workflow follow-up

- Run `/opsx:verify` against the merged implementation, then `/opsx:archive` once the epic is closed.
- Optional later change: publish the image to GHCR from CI (see design.md Open Questions).
