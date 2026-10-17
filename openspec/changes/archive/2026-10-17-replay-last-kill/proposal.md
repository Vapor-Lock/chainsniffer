# Proposal

## Why

Checking a new build in Discord means waiting for a kill on the map, which can take an hour on a
quiet evening. The live checks for the card styles spent most of their time waiting. A build
should be able to show what it makes of a known, real kill, on demand, in a test channel.

## What Changes

- The relay saves every kill it posts, with the Wanderer `map_kill` payload it arrived in, to
  `last_kill.json` in the state directory, replacing the one before.
- A new optional setting, `CHAINSNIFFER_REPLAY_CHANNEL_ID`, starts the process in replay mode. It
  loads the saved kill, posts it to that channel, and exits 0. The kill is decoded, classified,
  enriched and rendered by the running build, with the running configuration, so a new card style
  or decoder change shows on a real kill.
- A replay passes the startup channel check against the replay channel, skips the filters and the
  duplicate check, never opens the event stream, never posts to the relay's channel and never
  changes `state.json`. It is safe beside a running relay on the same state directory.
- With no kill saved, or a post that fails, a replay exits 1.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `relay-runtime`: adds "Last posted kill" and "Replay the last kill".

## Impact

- `internal/config`: `CHAINSNIFFER_REPLAY_CHANNEL_ID`, a snowflake, in the startup summary when set.
- `internal/state`: a `last_kill.json` store, separate from `state.json`.
- `internal/relay`: a `LastKill` interface it saves each posted kill to, and `Replay`.
- `cmd/chainsniffer`: the replay mode, and the store wired into the relay.
- `README.md`: the setting and a "Testing a build" section.
