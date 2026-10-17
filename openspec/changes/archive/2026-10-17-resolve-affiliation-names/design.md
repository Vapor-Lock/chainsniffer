# Design

## Context

`internal/universe.Resolver` looks system names up from ESI (`GET /universe/systems/{id}/`). Its
lookups are best effort: a 3-second timeout, an in-memory cache of successes only, a warning on
failure, and a fallback that never blocks the post. `relay.handle` resolves the system name and
the chain alias, then calls `discord.Render`. `killmail.Party` already carries `CorpID`,
`CorpTicker`, `CorpName`, `AllianceID`, `AllianceTicker` and `AllianceName`; the renderer drops
whichever are empty. `internal/state` keeps `state.json` (cursor plus posted ring), rewriting it
atomically after every event, and moves an unparseable file aside as `.corrupt`. See proposal.md
for the payloads that motivate this.

Facts this design rests on: in EVE, a corporation or alliance ID is permanent, and players cannot
rename a corporation or alliance or change its ticker. A corporation's alliance membership does
change often, and the killmail records the alliance at kill time.

## Goals / Non-Goals

**Goals:**
- Fill in missing corporation and alliance names and tickers, with no change to the renderer.
- Ask ESI about each ID once, ever, across restarts.
- Keep the post's worst-case extra delay at about one lookup timeout.

**Non-Goals:**
- Inferring an alliance the payload does not give: no corporation-to-alliance lookups or storage.
- Ship-name lookups. Wanderer has sent those in every payload seen so far.
- Changing classification or filters, which stay on the payload.
- Persisting system names, which already resolve once per process.

## Decisions

### Lookups live in `internal/universe`, behind interfaces each package declares

`universe` gains `Corporation(ctx, id) (Entity, bool)` and `Alliance(ctx, id) (Entity, bool)`, where
`Entity` is `{Name, Ticker string}`. They call ESI's public `GET /corporations/{id}/` and
`GET /alliances/{id}/`, both of which return `name` and `ticker`. They share the resolver's base URL,
HTTP client, timeout, user agent, body limit and logger. A failure is logged at warn and returns
`false`; an id of 0 or less returns `false` without a request.

`universe` declares the cache it needs, over plain values so that another package can implement
it without importing `universe`: `NameCache` with
`Get(kind string, id int64) (name, ticker string, ok bool)` and
`Put(kind string, id int64, name, ticker string)`, where `kind` is `"corporation"` or `"alliance"`.
A `WithNameCache` option sets it. The default is an in-memory map, so tests and an unwired resolver
behave as before. Only successes are `Put`.

`relay` declares its own `Affiliations` interface with the two lookup methods and a
`WithAffiliations` option. Without one, nothing is filled in. `cmd/chainsniffer` passes the
`*universe.Resolver` it already builds, configured with the `names.json` store.

- *Alternative:* `POST /universe/names/`, which resolves many IDs in one request. Rejected: it
  returns names but not tickers, and the cards show tickers.

### `names.json` is its own file, owned by `internal/state`

`internal/state` gains a `Names` store: `OpenNames(dir, opts...)` loads
`<dir>/names.json`, shaped
`{"corporations": {"<id>": {"name", "ticker"}}, "alliances": {...}}`.

- A missing file starts empty. An unparseable one is renamed to `names.json.corrupt`, with a
  warning, and replaced by an empty cache, just as `state.json` is handled.
- `Get` reads from memory. `Put` adds to memory and, only when the entry is new or different,
  rewrites the file with the existing atomic write helper (temporary file, fsync, rename, fsync
  directory).
- A failed write is logged at warn and keeps the entry in memory. The cache is an optimisation, so
  it never fails a post.
- Safe for concurrent use. Writes are serialized under one mutex.

`Names` satisfies `universe.NameCache` structurally, with the same method set; `state` does not
import `universe`.

It is a separate file, not a field of `state.json`, because:
- `state.json` is rewritten on every event, and an ever-growing cache would ride along on each
  write.
- A corrupt cache must never cost the cursor or the posted-kill record.
- A binary from before this change keeps reading `state.json` as it always has.

No size cap is needed. A long-lived chain sees a few thousand corporations, at about 60 bytes
each.

- *Alternative:* a `names` map inside `state.json`. Rejected for the three reasons above.

### Fill in the Killmail in the relay, after classification and filters

`relay.handle` classifies and filters first, then calls an `enrich(ctx, k)` step that completes
`k.Victim` and `k.FinalBlow`, and then renders. For each party:

- If `CorpID > 0` and either `CorpName` or `CorpTicker` is empty, look up the corporation and fill
  only the empty fields.
- If `AllianceID > 0` and either `AllianceName` or `AllianceTicker` is empty, do the same for the
  alliance.
- An `AllianceID` of 0 stays 0. The pilot had no alliance at kill time.

The lookups for one kill (at most two corporations and two alliances) run concurrently, under one
context bounded by the resolver's timeout. A cold kill then costs about one round trip, and a warm
one costs none. Because enrichment never adds IDs and runs after classification, classification
remains a function of the payload alone.

- *Alternative:* enrich during decoding in `internal/killmail`. Rejected: decoding is pure and
  offline.

### Character names too, found in the live check

The live check posted kill 139233228, whose payload carried the victim's `victim_char_id` but no
`victim_char_name`, so the title fell back to the corporation and the card never named the pilot.
Character names are resolved the same way: `universe.Character(ctx, id)` calls ESI's public
`GET /characters/{id}/`, which returns a `name` and no ticker, and returns an `Entity` with an empty
`Ticker`. They are cached as a third kind, `"character"`, in a `characters` table of `names.json`; a
file written before the table existed loads with an empty one. `relay.Affiliations` gains the
method, and enrichment fills an empty `CharName` for the victim and the final blow beside the
corporations and alliances, so a cold kill does at most six lookups in the one round trip. Only CCP
renames characters, which is rare enough to treat like a forced corporation rename.

The same check showed that the `block` card's Victim row carried only the affiliation, leaving the
pilot to the title. It now reads like the Final blow row: the pilot on the first line, then the
corporation and alliance, without the ship that the title already names. A victim without a
character, such as a structure, shows the affiliation alone. The `fields` style keeps its restored
layout, and the `components` style already names the victim in its section.

## Risks / Trade-offs

- [ESI slow or down] → a kill whose names are not cached waits at most about one timeout (3s), then
  posts without them. A warning is logged per failed ID, and failures are not stored, so the next
  kill retries.
- [CCP force-renames an offensive corporation] → the stored name stays until someone deletes that
  entry, or the whole `names.json`; the README says so. This is rare enough to accept.
- [`names.json` write per new ID] → at most six small atomic writes for a kill that introduces new
  IDs, and none once they are known. That is cheap next to the Discord post.
- [ESI error budget] → steady-state traffic is close to zero once the chain's regulars are cached,
  and every request sends the existing descriptive user agent.
