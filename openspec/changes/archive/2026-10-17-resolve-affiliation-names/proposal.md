# Proposal

## Why

Live kill cards show no corporation or alliance for the victim or the final blow. Wanderer's
`killmail_update` payload often carries the corporation and alliance IDs but leaves the names and
tickers `null`. A replay of kills 139230273 and 139230286 showed this in all six copies Wanderer
sent of each. The renderer drops what is empty, so the Victim row disappears and the Final blow row
shrinks to the pilot and ship. This happens in either card layout. Older cards showed these lines
only because their payloads happened to carry the names.

## What Changes

- When a kill's victim or final blow has a corporation or alliance ID but no name or ticker, the
  relay looks the name and ticker up from ESI's public `/corporations/{id}/` and `/alliances/{id}/`
  endpoints before rendering the card.
- Only IDs the payload gives are looked up. A missing alliance ID means the pilot had no alliance
  when the kill happened, so the card shows none rather than guessing from the corporation's current
  alliance.
- Corporation and alliance IDs, names and tickers do not change in EVE, so every resolved name and
  ticker is kept for good: in memory, and in a `names.json` file in the state directory, so a
  restart does not ask ESI again. The file is a cache only. A missing or corrupt `names.json`
  starts empty and never affects the cursor or the posted-kill record in `state.json`.
- The same goes for a victim or final-blow character ID without a name, which the live check found
  (kill 139233228): the relay looks the name up from ESI's `/characters/{id}/` and keeps it in
  `names.json` too. The `block` card's Victim row then names the pilot above the corporation and
  alliance, as the Final blow row does.
- Lookups are bounded by a short timeout, like system names. A lookup that fails or times out
  leaves that line out, and the kill is still posted. Failures are not cached.
- Classification and filters keep using only the IDs in the payload. The lookups only add display
  text.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `discord-killmail-posting`: adds an "Affiliation names" requirement. It makes the card show
  corporation and alliance names and tickers, and character names, that the payload identifies
  only by ID, on the same best-effort, bounded-delay terms as "System name"; and a "Victim row names
  the pilot" requirement for the `block` card.
- `relay-runtime`: adds a "Persistent name cache" requirement for `names.json` in the state
  directory.

## Impact

- `internal/discord`: the `block` Victim row gains the pilot's name.
- `internal/universe`: character, corporation and alliance lookups alongside the system-name resolver, with
  the same timeout and logging behaviour, backed by a cache interface the package declares.
- `internal/state`: a `names.json` store that implements that cache, written atomically when a new
  name is learned.
- `internal/relay`: a new interface it declares itself, which fills in missing affiliation names
  before `discord.Render`. Wired in `cmd/chainsniffer`.
- Adds up to four ESI requests for a kill whose names are missing, the first time each ID is seen. No new
  dependencies, configuration or permissions; ESI needs no authentication.
- README: say that names Wanderer leaves out are looked up from ESI.
