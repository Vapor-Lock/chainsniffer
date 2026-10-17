# Tasks

## 1. Implementation

- [x] 1.1 `CHAINSNIFFER_REPLAY_CHANNEL_ID` in `internal/config`: optional snowflake, in the startup summary when set.
- [x] 1.2 `last_kill.json` store in `internal/state` (`OpenLastKill`, `Save`, `Load`), atomic writes, failed save logged, missing file an `fs.ErrNotExist` error, `state.json` untouched.
- [x] 1.3 `relay.LastKill` and `WithLastKill`: save each posted kill with its payload; `Relay.Replay` past dedupe, filters, store and cursor.
- [x] 1.4 Replay mode in `cmd/chainsniffer`: startup check on the replay channel, topology loaded once for the alias, exit 0 or 1; end-to-end tests for a replay and for nothing saved.
- [x] 1.5 README: the setting, "Testing a build", exit status 0.

## 2. Live check

- [ ] 2.1 Run a build with `CHAINSNIFFER_REPLAY_CHANNEL_ID` set to a test channel against a state directory a relay has posted from, and confirm the card appears there and nowhere else.
