# Proposal

## Why

Our wormhole chain is mapped in Wanderer, which already tracks killmails in every system on the map
and exposes them over a real-time Server-Sent Events (SSE) API. Nobody watches the map all the time,
so kills in the chain (hostiles dying, our pilots dying, fleets showing up) go unnoticed until
someone opens the map. chainsniffer relays those kills into a Discord channel as they happen.

This is the project's first change: the repository has no code yet, so the change also sets up the
Go toolchain, the build and the `nix flake check` gate that agents rely on.

## What Changes

- New Go service `chainsniffer` that holds one SSE connection to a single Wanderer map
  (`GET /api/maps/{map}/events/stream?events=map_kill`, Bearer map API token). It reconnects with
  backoff and resumes with `last_event_id` so it doesn't miss kills.
- Parses `map_kill` events, ignores `kill_count` payloads, and pulls individual killmails out of
  `killmail_update` payloads.
- Configurable filters: exclude NPC kills, a minimum ISK value, and lists of friendly
  corporation/alliance IDs. With these, each kill is classified as a friendly **loss**, a friendly
  **kill**, or a **neutral** in-chain kill, and the operator can restrict posting to kills involving
  friendlies.
- Posts each kill that passes the filters to one Discord channel as a rich embed, using a Discord
  bot token. The embed has the victim, ship, final blow, system, ISK value, attacker count, a
  zKillboard link, and a color for loss, kill, or neutral. Posting respects Discord rate limits.
- Keeps a small JSON state file (last SSE event ID plus recently posted killmail IDs) so a restart
  resumes from where it stopped and never reposts a kill.
- Configured entirely through environment variables, with secrets optionally read from files.
- Nix build: `packages.default` (the Go binary), an OCI image built with `dockerTools`, and
  `nix flake check` running `go test`, `go vet` and a linter. The devshell gains the Go toolchain.

## Capabilities

### New Capabilities

- `wanderer-event-stream`: connecting to Wanderer's SSE API for one map, authentication, SSE
  framing, keepalives, reconnect with backoff, and resuming with `last_event_id`.
- `killmail-filtering`: turning `map_kill` events into individual killmails, classifying them
  (loss, kill, or neutral) against friendly corporations and alliances, and the configurable
  NPC, minimum-value and friendly-only filters.
- `discord-killmail-posting`: formatting a killmail as a Discord embed and delivering it to the
  configured channel with a bot token, including rate-limit handling and retries.
- `relay-runtime`: process configuration (env vars and secret files), the persisted relay state
  (last event ID, posted-killmail dedupe), logging, graceful shutdown, and the container image
  the service ships in.

### Modified Capabilities

None. The project has no existing specs.

## Impact

- **New code:** a Go module (`go.mod`, `cmd/chainsniffer`, `internal/...`). This is the first
  application code in the repository.
- **flake.nix:** Go toolchain in the devshell, `packages.default` (`buildGoModule`), an OCI image
  output, and `checks` (tests, vet, lint). This turns on the agents' merge gate.
- **Dependencies:** a Discord client library (discordgo). The SSE client is hand-written on
  `net/http`, so it needs no extra dependency.
- **External systems:**
  - The Wanderer instance: needs a map API token, and SSE must be enabled both on the server and
    for the map. It allows 10 SSE connections per API key. The public wanderer.ltd instance has SSE
    disabled, so the bot targets a self-hosted or other SSE-enabled instance.
  - A Discord application/bot: needs a token, and the bot must be invited with the
    `Send Messages` and `Embed Links` permissions on the target channel.
- **Out of scope:** multiple maps or channels per process, slash commands or other interactive
  bot features, non-kill map events, and fetching full killmails from ESI or zKillboard. The bot
  posts only what Wanderer sends.
