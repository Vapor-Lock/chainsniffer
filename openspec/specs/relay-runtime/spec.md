# relay-runtime Specification

## Purpose

Defines how the chainsniffer process is configured, how it persists its relay state across restarts,
how it logs and shuts down, and the container image it is delivered in.

## Requirements

### Requirement: Connection configuration
The relay SHALL read its connection settings from the required environment variables
`CHAINSNIFFER_WANDERER_URL`, `CHAINSNIFFER_WANDERER_MAP` (map ID or slug),
`CHAINSNIFFER_WANDERER_TOKEN`, `CHAINSNIFFER_DISCORD_TOKEN` and `CHAINSNIFFER_DISCORD_CHANNEL_ID`.

#### Scenario: Minimal configuration
- **WHEN** only the required variables are set
- **THEN** the relay starts using the defaults for everything else

#### Scenario: Missing required variable
- **WHEN** `CHAINSNIFFER_DISCORD_CHANNEL_ID` is not set
- **THEN** the relay exits non-zero before connecting anywhere and names every missing or invalid variable

#### Scenario: Invalid value
- **WHEN** `CHAINSNIFFER_MIN_VALUE` is `ten` or `CHAINSNIFFER_FRIENDLY_CORP_IDS` contains a non-integer
- **THEN** the relay exits non-zero and names the invalid variable

### Requirement: Filter configuration
The relay SHALL read its filter settings from optional environment variables:
`CHAINSNIFFER_FRIENDLY_CORP_IDS` and `CHAINSNIFFER_FRIENDLY_ALLIANCE_IDS` (comma-separated, default
empty), `CHAINSNIFFER_FRIENDLY_ONLY` (default `false`), `CHAINSNIFFER_EXCLUDE_NPC` (default `true`),
`CHAINSNIFFER_MIN_VALUE` in ISK (default `0`) and `CHAINSNIFFER_MAX_KILL_AGE` as a Go duration
(default `1h`).

#### Scenario: Filter defaults
- **WHEN** none of the filter variables are set
- **THEN** NPC kills are excluded, there is no minimum value, kills older than one hour are dropped and every non-NPC kill is posted

#### Scenario: Friendly IDs with spaces
- **WHEN** `CHAINSNIFFER_FRIENDLY_CORP_IDS` is `98000001, 98000002`
- **THEN** both corporation IDs are treated as friendly

### Requirement: Runtime settings
The relay SHALL read optional runtime settings: `CHAINSNIFFER_STATE_DIR` (default
`/var/lib/chainsniffer`), `CHAINSNIFFER_LOG_LEVEL` (`debug`, `info`, `warn`, `error`; default `info`)
and `CHAINSNIFFER_LOG_FORMAT` (`text` or `json`; default `text`).

#### Scenario: Custom state directory
- **WHEN** `CHAINSNIFFER_STATE_DIR` is `/data`
- **THEN** the relay keeps its state file in `/data`

### Requirement: Secrets from files
For each secret (`CHAINSNIFFER_WANDERER_TOKEN`, `CHAINSNIFFER_DISCORD_TOKEN`) the relay SHALL also
accept a `<NAME>_FILE` variable naming a file that holds the value, with surrounding whitespace
trimmed. Setting both the variable and its `_FILE` variant SHALL be rejected.

#### Scenario: Docker or Kubernetes secret
- **WHEN** `CHAINSNIFFER_DISCORD_TOKEN_FILE=/run/secrets/discord` points to a file containing the token and a trailing newline
- **THEN** the relay uses the token without the newline

#### Scenario: Both forms set
- **WHEN** both `CHAINSNIFFER_WANDERER_TOKEN` and `CHAINSNIFFER_WANDERER_TOKEN_FILE` are set
- **THEN** the relay exits non-zero with a configuration error

### Requirement: Secrets are never logged
The relay SHALL NOT write the Wanderer token or the Discord token to logs, including in configuration
dumps and error messages.

#### Scenario: Startup configuration summary
- **WHEN** the relay logs its effective configuration at startup
- **THEN** the tokens are shown as redacted

### Requirement: Persistent relay state
The relay SHALL keep its state in a JSON file `state.json` inside the state directory, holding the
last processed SSE event ID and the IDs of the most recently posted killmails (at least the last
1000). The state directory SHALL be created if it does not exist.

#### Scenario: State survives restart
- **WHEN** the relay posts killmail `123` after event `01JB`, is stopped and started again
- **THEN** it resumes from `01JB` and treats `123` as already posted

#### Scenario: Bounded size
- **WHEN** more than the retained number of killmails have been posted
- **THEN** the oldest posted IDs are dropped from the state so the file stays bounded

### Requirement: Durable state writes
The relay SHALL record a killmail as posted only after Discord accepted it, and SHALL write the state
file atomically (write a temporary file, then rename) after each processed event, so a crash never
leaves a partially written state file.

#### Scenario: Crash during write
- **WHEN** the process is killed while writing state
- **THEN** on the next start `state.json` holds either the previous or the new state, never a truncated file

### Requirement: Unreadable state
If `state.json` exists but cannot be parsed, the relay SHALL log a warning, move it aside as
`state.json.corrupt`, and start with empty state. If the state directory is not writable, the relay
SHALL exit non-zero at startup.

#### Scenario: Corrupt state file
- **WHEN** `state.json` contains invalid JSON
- **THEN** the relay renames it to `state.json.corrupt`, logs a warning and starts with live events only

#### Scenario: Read-only state directory
- **WHEN** the state directory cannot be written
- **THEN** the relay exits non-zero at startup naming the directory

### Requirement: Structured logging
The relay SHALL log to standard error at the configured level and format, including at info level:
startup, a successful Wanderer subscription, each reconnect with its reason, and each posted killmail
ID with its classification.

#### Scenario: JSON logs
- **WHEN** `CHAINSNIFFER_LOG_FORMAT=json`
- **THEN** every log line is one JSON object

### Requirement: Graceful shutdown
On SIGINT or SIGTERM the relay SHALL stop reading new events, finish the Discord post in progress (for
at most 10 seconds), persist its state, and exit with status 0.

#### Scenario: Container stop
- **WHEN** the container runtime sends SIGTERM while the relay is idle
- **THEN** the relay saves its state and exits 0 within a few seconds

### Requirement: Container image
The project SHALL build an OCI container image of the relay that runs the `chainsniffer` binary as its
entrypoint as a non-root user, includes CA certificates for TLS, declares `/var/lib/chainsniffer` as
the state directory, and needs no other files at runtime.

#### Scenario: Run the image
- **WHEN** the image is run with the required environment variables and a volume mounted at `/var/lib/chainsniffer`
- **THEN** the relay connects to Wanderer and Discord over TLS and persists its state to the volume

#### Scenario: Non-root
- **WHEN** the image's configured user is inspected
- **THEN** it is not root

### Requirement: Topology configuration
The relay SHALL read two optional settings: `CHAINSNIFFER_HOME_SYSTEM_ID`, the EVE solar system ID
of the home system (unset by default), and `CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL`, a Go duration
(default `15m`). The relay SHALL load and follow the map's topology whether or not the home system
is set. The home system SHALL only enable the linked-system filter.

#### Scenario: Home system set
- **WHEN** `CHAINSNIFFER_HOME_SYSTEM_ID` is `31000727`
- **THEN** the relay filters kills to systems linked to solar system 31000727

#### Scenario: Home system unset
- **WHEN** `CHAINSNIFFER_HOME_SYSTEM_ID` is not set
- **THEN** the relay still loads topology snapshots on connect and at the resync interval and follows the topology events, but does not filter by linked system

#### Scenario: Invalid home system
- **WHEN** `CHAINSNIFFER_HOME_SYSTEM_ID` is `J142845` or `0`
- **THEN** the relay exits with status 2 and names `CHAINSNIFFER_HOME_SYSTEM_ID`

#### Scenario: Invalid resync interval
- **WHEN** `CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL` is `soon`, `0` or negative
- **THEN** the relay exits with status 2 and names `CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL`

#### Scenario: Startup summary
- **WHEN** the relay starts
- **THEN** the startup configuration log line includes the resync interval, and the home system ID when one is configured

### Requirement: Card style configuration
The relay SHALL read the optional setting `CHAINSNIFFER_CARD_STYLE`, case-insensitively, as `block`,
`fields`, `strip` or `components`, defaulting to `components`, and SHALL post every kill card in that
style. Any other value SHALL be a configuration error that exits with status 2 and names the
variable. The startup configuration summary SHALL include the card style.

#### Scenario: Default style
- **WHEN** `CHAINSNIFFER_CARD_STYLE` is unset
- **THEN** kill cards are posted in the `components` style and the startup summary says `components`

#### Scenario: Old style chosen
- **WHEN** `CHAINSNIFFER_CARD_STYLE` is `Fields`
- **THEN** kill cards are posted in the `fields` style

#### Scenario: Component cards chosen
- **WHEN** `CHAINSNIFFER_CARD_STYLE` is `components`
- **THEN** kill cards are posted as component messages

#### Scenario: Unknown style
- **WHEN** `CHAINSNIFFER_CARD_STYLE` is `table`
- **THEN** the relay exits with status 2 and names `CHAINSNIFFER_CARD_STYLE`, before connecting anywhere

### Requirement: Persistent name cache
The relay SHALL keep every corporation and alliance name and ticker, and every character name, it
resolves in a JSON file `names.json` in the state directory, and SHALL NOT look up an ID found there again, including after
a restart. Entries SHALL never expire. A missing `names.json` SHALL start an empty cache; an
unreadable one SHALL be moved aside as `names.json.corrupt` with a warning and replaced by an empty
cache. Neither SHALL affect `state.json`.

#### Scenario: Names survive restart
- **WHEN** the relay resolves corporation `98735003`, is stopped and started again, and a later kill names corporation `98735003` without its name
- **THEN** the embed shows the corporation's name and ticker and no lookup is made for it

#### Scenario: Corrupt name cache
- **WHEN** `names.json` contains invalid JSON at startup
- **THEN** the relay renames it to `names.json.corrupt`, logs a warning, resumes from the event ID in `state.json`, and resolves names afresh

#### Scenario: Failed lookups are not stored
- **WHEN** ESI cannot be reached while resolving alliance `99014747`
- **THEN** nothing is written for it to `names.json`, and the next kill naming it tries again

### Requirement: Last posted kill
After Discord accepts a kill, the relay SHALL save that kill's ID and the `map_kill` payload it
arrived in to `last_kill.json` in the state directory, replacing the kill saved before. A kill that
is filtered, a duplicate, or not accepted by Discord SHALL NOT be saved. A failed save SHALL be
logged and SHALL NOT affect the post or `state.json`.

#### Scenario: Posted kill saved
- **WHEN** an event carries killmails `1` and `2`, and both are posted
- **THEN** `last_kill.json` holds killmail `2` and the event's payload

#### Scenario: Filtered kill not saved
- **WHEN** killmail `3` is dropped as an NPC kill after killmail `2` was posted
- **THEN** `last_kill.json` still holds killmail `2`

### Requirement: Replay the last kill
When `CHAINSNIFFER_REPLAY_CHANNEL_ID` is set to a Discord channel ID, the process SHALL check the bot
can post to that channel, post the kill saved in `last_kill.json` there in the configured card
style, classified with the configured friendly IDs and named as a live kill is, and exit with
status 0. It SHALL NOT open the event stream, post to `CHAINSNIFFER_DISCORD_CHANNEL_ID`, apply the
filters or the duplicate check, or change `state.json`. A value that is not a channel ID SHALL be a
configuration error, exiting with status 2.

#### Scenario: Replay to a test channel
- **WHEN** a relay has posted killmail `128412345`, and the process is then started with `CHAINSNIFFER_REPLAY_CHANNEL_ID` set to a test channel
- **THEN** the test channel receives killmail `128412345`'s card, nothing is posted to the relay's channel, the stream is not opened, `state.json` is unchanged, and the process exits 0

#### Scenario: Nothing saved
- **WHEN** the process is started with `CHAINSNIFFER_REPLAY_CHANNEL_ID` set and no `last_kill.json` in the state directory
- **THEN** it logs that there is no kill to replay, posts nothing, and exits 1

#### Scenario: Not a channel ID
- **WHEN** `CHAINSNIFFER_REPLAY_CHANNEL_ID` is `#test`
- **THEN** the process exits with status 2 and names `CHAINSNIFFER_REPLAY_CHANNEL_ID`
