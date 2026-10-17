# Spec Delta

## ADDED Requirements

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
