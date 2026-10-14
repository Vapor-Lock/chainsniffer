# chainsniffer

A Discord bot that relays in-chain killmails from a [Wanderer](https://wanderer.ltd) map to a
Discord channel as they happen.

chainsniffer holds one Server-Sent Events connection to a single Wanderer map, watches the
`map_kill` events Wanderer emits for every system on that map, classifies each killmail against
your friendly corporations and alliances, and posts the ones that pass your filters to one Discord
channel as an embed. It keeps a small state file, so a restart resumes where it stopped and never
posts the same kill twice.

One process serves one map and one channel.

## What a post looks like

Each killmail becomes an embed:

- **Title:** `<victim ship> — <victim name | corp name>`, linked to the kill on zKillboard.
- **Thumbnail:** a render of the victim's ship.
- **Fields:** System, Value (`1.2B ISK`), Attackers, Victim (`[TICKER] Corp` / `<ALLY>`) and
  Final blow (`Pilot (Ship) [TICKER]`).
- **Color and footer:** red `Loss` for a friendly victim, green `Kill` for a friendly final blow,
  and grey with no footer for a neutral in-chain kill.
- **Timestamp:** the kill time Wanderer reported.

System names are resolved through the public ESI endpoint and cached in memory; a lookup that fails
falls back to the solar system ID.

## Setup

### 1. The Discord bot

1. Create an application at the [Discord Developer Portal](https://discord.com/developers/applications),
   add a bot to it, and copy the **bot token**. This is `CHAINSNIFFER_DISCORD_TOKEN`.
2. Invite the bot to your server and give it these three permissions on the target channel:
   **View Channel**, **Send Messages** and **Embed Links**. Nothing else is needed — the bot only
   writes, and it requests no gateway intents.
3. Turn on Developer Mode in Discord (*Settings → Advanced*), right-click the channel and
   **Copy Channel ID**. This is `CHAINSNIFFER_DISCORD_CHANNEL_ID`.

chainsniffer checks all of this at startup: it verifies the token, resolves the channel, and
reports any of the three permissions it is missing, before it subscribes to the stream. A rejected
token or an unreachable channel exits 1 rather than retrying forever.

### 2. The Wanderer map

chainsniffer authenticates with a map's **public API key**, which you copy from the map's settings
in Wanderer. This is `CHAINSNIFFER_WANDERER_TOKEN`. `CHAINSNIFFER_WANDERER_MAP` is the map's ID or
slug, and `CHAINSNIFFER_WANDERER_URL` is the base URL of the Wanderer instance
(`https://wanderer.example.com`, no path).

**SSE has to be enabled in two places:**

- **On the server.** The real-time events API is optional per Wanderer instance. The public
  **wanderer.ltd instance has it disabled** and answers `503 Server-Sent Events are disabled on
  this server`, so chainsniffer only works against a self-hosted instance (or another instance)
  with SSE turned on.
- **For the map.** A map's admin enables SSE in the map's settings. Without it the server answers
  `403 SSE_DISABLED_FOR_MAP`.

Either refusal, along with a rejected key (401), a missing map (404) and an Enterprise server's
`402 SUBSCRIPTION_REQUIRED`, is treated as fatal: chainsniffer logs what the server said and exits
1 instead of reconnecting. Every other failure — a dropped connection, a 5xx, a 429 from the
10-connections-per-key limit — is retried with exponential backoff.

## Configuration

Everything is read from the environment at startup. Invalid or missing settings are reported
together, so one restart fixes a whole batch of typos, and the process exits **2** without
connecting anywhere.

### Required

| Variable | Description |
| --- | --- |
| `CHAINSNIFFER_WANDERER_URL` | Base URL of the Wanderer instance, e.g. `https://wanderer.example.com`. |
| `CHAINSNIFFER_WANDERER_MAP` | The map's ID or slug. |
| `CHAINSNIFFER_WANDERER_TOKEN` | The map's public API key. |
| `CHAINSNIFFER_DISCORD_TOKEN` | The Discord bot token. |
| `CHAINSNIFFER_DISCORD_CHANNEL_ID` | The channel to post in, as a decimal snowflake. |

### Filters

| Variable | Default | Description |
| --- | --- | --- |
| `CHAINSNIFFER_FRIENDLY_CORP_IDS` | empty | Comma-separated EVE corporation IDs counted as friendly. |
| `CHAINSNIFFER_FRIENDLY_ALLIANCE_IDS` | empty | Comma-separated EVE alliance IDs counted as friendly. |
| `CHAINSNIFFER_FRIENDLY_ONLY` | `false` | Post only kills involving a friendly (drops neutral in-chain kills). Requires at least one friendly ID. |
| `CHAINSNIFFER_EXCLUDE_NPC` | `true` | Drop kills Wanderer flagged as NPC kills. |
| `CHAINSNIFFER_MIN_VALUE` | `0` | Drop kills below this total value, in ISK. |
| `CHAINSNIFFER_MAX_KILL_AGE` | `1h` | Drop kills older than this Go duration, e.g. `90m` or `1h30m`. Keeps a backfill or a resent killmail from posting stale kills. |

With no friendly IDs configured every kill is neutral, which is a fine way to run the bot as a
plain in-chain kill feed. Turning `CHAINSNIFFER_FRIENDLY_ONLY` on without friendly IDs would post
nothing at all, so that combination is rejected at startup.

### Runtime

| Variable | Default | Description |
| --- | --- | --- |
| `CHAINSNIFFER_STATE_DIR` | `/var/lib/chainsniffer` | Directory holding `state.json`. Must be writable. |
| `CHAINSNIFFER_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `CHAINSNIFFER_LOG_FORMAT` | `text` | `text` or `json`. |

### Secrets from files

Both tokens can come from a file instead of the environment, which is the shape Docker and
Kubernetes secrets arrive in. Append `_FILE` to the variable's name and point it at the file:

| Variable | Description |
| --- | --- |
| `CHAINSNIFFER_WANDERER_TOKEN_FILE` | File holding the Wanderer map API key. |
| `CHAINSNIFFER_DISCORD_TOKEN_FILE` | File holding the Discord bot token. |

A trailing newline is trimmed. Setting both forms of the same secret is a configuration error.
Neither token is ever logged: the startup summary and every error report them as `[redacted]`.

## Running

### The container image

```sh
nix build .#image
docker load < result
```

That loads `chainsniffer:0.1.0`: the binary, a CA bundle, and nothing else — no shell and no
package manager. It runs as uid `65532` and declares a volume at `/var/lib/chainsniffer` owned by
that user, so a mounted volume is writable without any further setup.

```sh
docker run -d --name chainsniffer --restart unless-stopped \
  -v chainsniffer-state:/var/lib/chainsniffer \
  -e CHAINSNIFFER_WANDERER_URL=https://wanderer.example.com \
  -e CHAINSNIFFER_WANDERER_MAP=my-chain \
  -e CHAINSNIFFER_WANDERER_TOKEN=... \
  -e CHAINSNIFFER_DISCORD_TOKEN=... \
  -e CHAINSNIFFER_DISCORD_CHANNEL_ID=123456789012345678 \
  -e CHAINSNIFFER_FRIENDLY_CORP_IDS=98000001,98000002 \
  chainsniffer:0.1.0
```

Run it without the state volume and it still works, but a restart re-posts whatever Wanderer
replays. Run it with no environment at all and it exits 2, naming every variable it needs:

```console
$ docker run --rm chainsniffer:0.1.0
chainsniffer: invalid configuration: CHAINSNIFFER_WANDERER_URL is required; ...
```

`SIGTERM` (so, `docker stop`) finishes the post in flight, within ten seconds, saves the state and
exits 0.

### The binary

```sh
nix build     # ./result/bin/chainsniffer
nix run .     # with the variables already in the environment
```

### Exit statuses

| Status | Meaning |
| --- | --- |
| 0 | A clean shutdown on `SIGINT` or `SIGTERM`. |
| 1 | A failure needing an operator: a rejected token, a channel the bot cannot post to, a stream Wanderer refuses, an unusable state directory. |
| 2 | A configuration the relay refuses to start with. Every bad variable is named on stderr. |

## Limitations

- **The backfill window is 10 minutes.** Wanderer replays missed events from an in-memory relay
  holding at most 10 minutes (and 1000 events). An outage longer than that loses the kills in the
  gap. Wanderer may also resend older kills; `CHAINSNIFFER_MAX_KILL_AGE` and the dedupe state keep
  those from being posted again.
- **Classification sees only the final blow.** Wanderer's kill payload has no attacker list, so a
  kill your pilots took part in but didn't land the final blow on is classified neutral — and with
  `CHAINSNIFFER_FRIENDLY_ONLY` on, it isn't posted.
- **Run one instance per map.** Two processes with separate state double-post. There is no
  coordination between instances.
- **The bot only writes.** No slash commands, no reading messages, no map events other than kills.
- **No enrichment.** Posts carry what Wanderer sent, plus the system name from ESI. Nothing is
  fetched from zKillboard or ESI's killmail endpoints.

## Development

The devshell (`nix develop`, or direnv) carries Go, gopls and golangci-lint.

```sh
go build ./...
go test ./...
nix flake check    # the gate: build + tests, go vet, golangci-lint, and the image
nix build          # the binary
nix build .#image  # the OCI image
```

Every test runs offline against `httptest` servers, so `nix flake check` works in the Nix sandbox.
Note that **Nix only sees files git tracks**: `git add` a new file before building or checking.

### Updating `vendorHash`

`packages.default` is a `buildGoModule` with a pinned `vendorHash`, which covers the vendored
dependency tree. Any change to `go.mod` or `go.sum` invalidates it and `nix build` fails with a
hash mismatch. To update it:

1. In `flake.nix`, replace the `vendorHash` value with `pkgs.lib.fakeHash`.
2. Run `nix build`. It fails, reporting the hash it actually got.
3. Copy that `got:` hash into `vendorHash` and build again.

`nix build 2>&1 | grep got:` does the reading in step 2.

## Layout

| Path | Contents |
| --- | --- |
| `cmd/chainsniffer` | Wiring, signal handling, exit statuses. |
| `internal/config` | Environment parsing, `_FILE` secrets, validation, the redacted summary. |
| `internal/sse` | A generic SSE reader: framing only, `io.Reader` to events. |
| `internal/wanderer` | The map stream client: requests, backoff, staleness watchdog, fatal statuses. |
| `internal/killmail` | Payload decoding, the `Killmail` type, classification, filters. |
| `internal/discord` | Embed rendering, the poster (rate limits, retries), the startup check. |
| `internal/universe` | The ESI system-name resolver and its cache. |
| `internal/state` | `state.json` and the bounded set of posted killmail IDs. |
| `internal/relay` | The pipeline: event to killmails to filter, dedupe, post, persist. |

The specs this was built from live in `openspec/changes/add-killmail-relay-bot/`;
`design.md` there documents the Wanderer wire format and the decisions behind the layout.
