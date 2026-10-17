# Design

## Context

`state.json` holds the stream cursor and the IDs of posted kills, not their contents, and
Wanderer's backfill covers only ten minutes, so nothing the relay keeps today can produce a kill on
demand. The replay has to bring its own copy of one.

## Goals / Non-Goals

**Goals:**
- Post a real, previously posted kill to a test channel from any build, with that build's
  decoding, classification, enrichment and rendering.
- Leave a running relay sharing the state directory undisturbed.

**Non-Goals:**
- Replaying more than the last kill, or a chosen kill.
- Replaying into the relay's own channel as part of normal operation.

## Decisions

### Save the payload, not the decoded kill

`last_kill.json` holds `{"killmail_id": <id>, "payload": <map_kill payload>}`: the event payload the
kill arrived in, as Wanderer sent it, and which of its killmails was posted. A later build decodes
it with its own `killmail.Extract`, so a decoder change shows in the replay too. Keeping the raw
entry on `killmail.Killmail` was the alternative; it makes the type incomparable for its tests and
reaches into the decoder for a testing aid.

The save happens right after Discord accepts the post, next to `MarkPosted`. A failed write is
logged at warn and swallowed: the file serves replays only. It is its own file, like `names.json`,
so `state.json` stays small and is never at risk from it.

### Replay is a mode of the binary, chosen by an environment variable

Everything else is configured from the environment, and so is this:
`CHAINSNIFFER_REPLAY_CHANNEL_ID` set means replay and exit. The rest of the configuration is
required and read as usual, since the replay needs the Discord token, the Wanderer map for the
chain alias, and the friendly IDs and card style for the card.

`Relay.Replay(ctx, id, payload)` runs the kill through the same classification, enrichment, name
and alias resolution and rendering as `handle`, shared through one `card` helper, but not the
dedupe, the filters, `MarkPosted`, the last-kill save or the cursor. `cmd/chainsniffer` builds a
poster on the replay channel and runs its startup check, loads the topology once for the alias (a
failure only costs the alias), and exits 0 on a post and 1 otherwise.

## Risks / Trade-offs

- [The replay channel is the relay's channel] → the kill is posted there a second time. That is
  what was asked for, so it is not refused.
- [One more file write per post] → one small atomic write beside the `state.json` save. Negligible
  next to the Discord post.
