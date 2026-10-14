# Design

## Context

The repository is empty apart from the devflake scaffolding. `flake.nix` has an empty
`packages.default`, empty `checks` and an empty devshell `packages`. This change therefore sets up
the Go project and its Nix plumbing as well as the bot itself. See proposal.md for motivation.

The canonical Wanderer documentation is the [Real-Time Events API post on wanderer.ltd](https://wanderer.ltd/news/webhooks)
(2025-06-21). It disagrees with the current server code in three places: the envelope's timestamp
key is `timestamp`, not `ts`; the `?token=` query parameter is not accepted; and its `map_kill`
example (a nested `victim{name,ship}` object) is not what the server sends. This design follows the
code, verified against the Wanderer source (`wanderer-industries/wanderer`,
`lib/wanderer_app_web/controllers/api/events_controller.ex`,
`lib/wanderer_app_web/controllers/plugs/check_sse_disabled.ex`,
`lib/wanderer_app/kills/message_handler.ex`, `lib/wanderer_app/external_events/*`). Test fixtures
use the payload shapes from the code.

SSE is optional on each Wanderer instance. The public wanderer.ltd instance has it disabled and
answers `503 Server-Sent Events are disabled on this server` (verified 2026-10-14), so the bot
targets a self-hosted or other instance with SSE enabled. A map's admin must also enable SSE in the
map's settings, otherwise the server returns 403 `SSE_DISABLED_FOR_MAP`, and Enterprise-mode
servers can return 402 `SUBSCRIPTION_REQUIRED`.

- **Auth:** the stream accepts only `Authorization: Bearer <map public_api_key>`. The `?token=`
  query parameter mentioned in Wanderer's blog post is not accepted by the current controller.
- **Framing:** each event is written as `id: <ULID>\nevent: <type>\ndata: <json>\n\n`. The data is
  the whole envelope, `{"id","type","map_id","timestamp","payload"}`. Keepalives are
  `: keepalive\n\n`, sent every ~30s. The first event on every connection is `event: connected`.
- **Backfill:** `last_event_id` is a query parameter (not the `Last-Event-ID` header). It replays
  from an in-memory relay that keeps only **10 minutes** of events (at most 1000).
- **Kill payloads:** `map_kill` is emitted for two payload shapes:
  - `{"type":"kill_count","solar_system_id","count"}`
  - `{"type":"killmail_update","solar_system_id","killmails":[...],"timestamp"}`, where each
    killmail is a flat map: `killmail_id`, `kill_time`, `solar_system_id`, `zkb`,
    `victim_{char,corp,alliance}_{id,name}`, `victim_{corp,alliance}_ticker`,
    `victim_ship_{type_id,name}`, the same `final_blow_*` fields, `attacker_count`,
    `total_value` and `npc`.

  The payload contains no attacker list and no system name.
- **Limits:** 10 SSE connections per API key, and SSE must be enabled for the map.

## Goals / Non-Goals

**Goals:**
- A single static Go binary with few dependencies, testable without network access, so it fits
  the `nix flake check` sandbox.
- Kills are delivered at least once to the pipeline and posted at most once, given state on disk.
- Packages split by capability, so agents can build them in parallel.

**Non-Goals:**
- High availability or running several replicas. One instance per map; a second instance would
  post duplicates.
- Receiving Discord interactions (slash commands, reading messages). The bot only writes.
- Enriching kills from ESI or zKillboard beyond the system-name lookup.

## Decisions

### Project layout

```
cmd/chainsniffer/main.go       wiring, signals, exit codes
internal/config                env + _FILE parsing, validation, redacted summary
internal/sse                   generic SSE reader (framing only, io.Reader -> Event)
internal/wanderer              stream client: request building, backoff, staleness, fatal errors
internal/killmail              payload decoding, Killmail type, classification, filters
internal/discord               embed rendering, poster (rate limits, retries, fatal errors), channel check
internal/universe              system-name resolver with cache
internal/state                 state.json load/save, bounded posted-ID set
internal/relay                 pipeline: event -> killmails -> filter/dedupe -> post -> persist cursor
```

Every package depends only on interfaces it defines itself, such as `Poster`, `Store` and
`NameResolver`. `relay` composes them, and each package is unit-tested with fakes or
`httptest.Server`.

### Hand-written SSE client instead of a library

`internal/sse` is about 100 lines on `bufio.Reader`. It implements the WHATWG parsing rules:
`data` joining, comments, CR/LF handling, and a stripped leading space.

- **Why:** the Go SSE libraries (r3labs/sse and similar) handle reconnects with the
  `Last-Event-ID` header, while Wanderer needs a `last_event_id` query parameter and custom
  fatal-status handling. Owning the loop is simpler than working around a library.
- **Alternative considered:** r3labs/sse. Rejected for the reason above and for the extra
  dependency.

### Stream client loop

`wanderer.Client.Run(ctx, cursor func() string, handle func(Envelope) error)`:

- Each connection builds the URL with `events=map_kill`, plus `last_event_id=<cursor()>` when a
  cursor is known.
- A watchdog resets a 90s timer on every byte read. When the timer fires it cancels the request
  context, which surfaces as a reconnectable error.
- Backoff starts at 1s, doubles up to a 60s cap, and adds ±20% jitter. It resets after the
  first event or keepalive on a connection.
- 401, 402, 403 and 404 return a typed `FatalError`, as does a 503 whose body is the plain-text
  "SSE disabled" message from `check_sse_disabled.ex` or JSON code `SSE_GLOBALLY_DISABLED`.
  `main` exits 1. All other failures retry, including 429 connection limits.
- `handle` runs synchronously in the read loop. That backpressure is deliberate: while Discord
  is slow, the server-side buffer and backfill absorb the delay, and the cursor only advances
  after `handle` returns.

### Processing order and cursor semantics

For each envelope, `relay` does the following in order:

1. Decode the killmails, skipping `kill_count`.
2. For each killmail in order: dedupe, filter, then post.
3. Record each posted killmail in the state.
4. After the whole envelope is handled, set the cursor to the envelope `id` and save the state
   atomically.

A crash mid-envelope replays the envelope on restart. Its already-posted killmails are deduped,
so the result is at-least-once processing with at-most-once posting.

A killmail that Discord ultimately refuses (retry budget exhausted) is skipped and is not marked
as posted. The cursor still advances, so one undeliverable kill can't wedge the stream. The
error log is the record.

### Dedupe store

The state holds `last_event_id` and `posted`, an ordered list of the last 1000 killmail IDs. It is
backed in memory by a ring plus a set. The state is written with `os.CreateTemp` in the same
directory, followed by `fsync` and `os.Rename`. At most one write happens per envelope, and kill
volume is low, so write amplification doesn't matter.

### Discord: discordgo, REST plus a minimal gateway session

The bot uses `github.com/bwmarrin/discordgo`:

- `ChannelMessageSendEmbed` posts the messages.
- `Channel(id)` does the startup channel check. A follow-up permission check on the channel uses
  `UserChannelPermissions` when the guild state is available.
- The bot also opens a gateway session with zero intents. Discord requires a bot to have
  connected to the gateway before it can create messages, and the session shows the bot as
  online. discordgo handles gateway reconnects.

discordgo's REST client already follows per-route rate-limit buckets and 429 `retry_after`. Our
poster wraps it with:

- a 5-minute retry budget for network errors and 5xx, with exponential backoff;
- classification of `*discordgo.RESTError` codes: 401 is fatal (bad token), and 403 or 404 are
  fatal with a message about permissions.

**Alternative considered:** a channel webhook URL instead of a bot. It is simpler, but the user
asked for a bot, and a bot can be pointed at another channel by changing an ID instead of creating
a new webhook.

### Embed layout

- **Title:** `<victim ship> — <victim name | corp name>`, linked to
  `https://zkillboard.com/kill/<id>/`.
- **Thumbnail:** `https://images.evetech.net/types/<ship_type_id>/render?size=128`.
- **Inline fields:**
  - System
  - Value, formatted as `1.2B`, `412.5M` or `950K` followed by `ISK`
  - Attackers
  - Victim: `[TICKER] Corp` / `<ALLY>`
  - Final blow: `Pilot (Ship) [TICKER]`
- **Color:** loss `0xC0392B`, kill `0x27AE60`, neutral `0x7F8C8D`.
- **Timestamp and footer:** the timestamp is `kill_time`. The footer reads `Loss` or `Kill`, and
  is empty for neutral kills.

Rendering is a pure function `Render(Killmail, Classification, systemName) *discordgo.MessageEmbed`,
so golden tests can cover it.

### System names via ESI

`internal/universe` resolves `solar_system_id` with `GET https://esi.evetech.net/latest/universe/systems/{id}/`.
ESI is public and needs no auth. Lookups use a 3s timeout, and results go in an unbounded
in-memory cache, since a chain only has dozens of systems. On failure the bot uses the ID and
doesn't cache it, so a later kill retries the lookup.

**Alternatives considered:**
- Embedding the Static Data Export: it is large and needs updating when new systems appear.
- Asking Wanderer's map systems API: it needs another endpoint and the same token, and only
  covers systems still on the map.

ESI is the least coupled option.

### Configuration and logging

- **Config:** `os.LookupEnv` plus `strconv`. No config library is needed for about 15 variables.
  Validation collects every error and reports them all at once.
- **Logging:** `log/slog` from the standard library, with a text or JSON handler chosen by
  config. Tokens live in a `Secret` string type whose `LogValue` and `String` methods return
  `[redacted]`.

### Nix build, checks, and image

- **Devshell:** `pkgs.go`, `pkgs.gopls`, `pkgs.golangci-lint`.
- **`packages.default`:** `pkgs.buildGoModule` with a pinned `vendorHash`, `CGO_ENABLED=0`,
  `ldflags = ["-s" "-w"]`, and `subPackages = ["cmd/chainsniffer"]`.
- **`packages.image`:** `pkgs.dockerTools.buildLayeredImage`:
  - `contents = [ pkgs.cacert ]`
  - `config.Entrypoint = [ "${default}/bin/chainsniffer" ]`
  - `config.User = "65532:65532"`
  - `config.Env = [ "SSL_CERT_FILE=…" ]`
  - `config.Volumes."/var/lib/chainsniffer" = {}`
  - A `fakeRootCommands` step creates the state directory, owned by uid 65532.

  The image is loaded with `docker load < result`.
- **`checks`:**
  - `build` builds `packages.default`. `buildGoModule`'s `checkPhase` runs `go test ./...`.
  - `vet` runs `go vet ./...` against the vendored modules.
  - `golangci-lint` uses a minimal config (default linters plus `gofmt`) in `.golangci.yml`.
  - `image` builds the image, so a broken image definition fails the gate.

  Every check runs offline: `buildGoModule` vendors dependencies through its fixed-output
  derivation, and the tests use `httptest`.

**Alternative considered:** gomod2nix. It is more precise per dependency but adds a generated
file and a tool. With only a few dependencies, `vendorHash` is enough, though it has to be
updated whenever `go.sum` changes. Tasks call this out.

## Risks / Trade-offs

- **[Backfill covers only 10 minutes]** If the bot is down longer than that, kills from the gap
  are lost. Wanderer may also resend older kills later as a `killmail_update`.
  → Document it. The max-age filter and dedupe keep resent kills from causing reposts. A longer
  outage is acceptable for a notification feed.
- **[`last_event_id` unknown after a Wanderer restart]** If the relay's ETS is wiped, Wanderer
  returns nothing to backfill and streams live events.
  → Nothing to do. Dedupe covers any overlap.
- **[Classification sees only the final blow]** Wanderer's payload has no attacker list, so a
  kill where friendlies took part but didn't land the final blow is classified `neutral`. With
  friendly-only mode on, it is not posted.
  → State this in the README and spec. A later change could fetch the full killmail from
  zKillboard/ESI if this matters.
- **[Wanderer payload drift]** The kill payload is an internal map, not a versioned schema.
  → Decode leniently (all fields optional except `killmail_id`), keep fixture payloads from the
  real source in tests, and log unknown payload types at debug level.
- **[Duplicate instances]** Two containers with separate state would double-post.
  → Document that one instance runs per map.
- **[vendorHash churn]** Each dependency change breaks `nix build` until the hash is updated.
  → The task list and README describe the update procedure (set `lib.fakeHash`, build, copy the
  hash).

## Migration Plan

This is a new deployment, so there is no migration. To roll it out:

1. Create a Discord application and bot, invite it with View Channel, Send Messages and Embed
   Links on the target channel, and note the token and channel ID.
2. Copy the map's public API key from Wanderer's map settings, and enable SSE for the map.
3. Run `nix build .#image && docker load < result`. Run the container with the env vars and a
   volume at `/var/lib/chainsniffer`.

To roll back, stop the container. Nothing else holds state.

## Open Questions

- Where the image is published (GHCR via CI, or loaded locally). This doesn't affect the code,
  so it can be added later as a CI change.
