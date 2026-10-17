# Design

## Context

The filters in `internal/killmail` are pure: `Filters.Allow(k, class)` decides from the killmail and
from functions the relay wires in, the way `Linked` reads the topology graph. A kill's payload gives
only `solar_system_id`, with no security status. The relay already asks ESI's
`GET /universe/systems/{id}/` for every posted kill's system name. That answer also carries
`security_status`, which `internal/universe` reads past today and caches the name alone.

EVE's solar system IDs are partitioned by kind: 30000000–30999999 are known space (Pochven and
Zarzakh included), 31000000–31999999 are wormhole space (Thera, the shattered and Drifter systems
included), and 32000000 and up are Abyssal pockets, which never appear on a map.

## Goals / Non-Goals

**Goals:**
- Relay k-space kills by security class, chosen with one setting.
- Ask ESI nothing more than today for a relay that wants all k-space or none of it.
- Never lose a kill because a lookup failed.

**Non-Goals:**
- Filtering by region, constellation or a list of systems.
- Separate classes for Pochven or Zarzakh. They count as null-sec, which their -1.0 security makes
  them in game.
- Showing the security class on the card.

## Decisions

### The setting is a set of classes, and empty means none

`config` parses `CHAINSNIFFER_KSPACE` into a set of `high`, `low` and `null`, trimmed, lower-cased and
deduplicated. `none`, unset and empty all give the empty set, which drops every k-space kill.
`none` beside a class, or any other word, is an error naming the variable, exit 2. The summary logs
the classes in a fixed order, or `none`. Keeping `none` as a word, rather than only "unset", lets an
operator write the choice down explicitly in a compose file.

### K-space is decided by ID, the class by security status

`killmail` gains `SecurityClass` (`high`, `low`, `null`), `IsKSpace(systemID)` (the 30000000 range),
and `ClassOf(security float64)`: high at 0.45 and up, low above 0.0, null at 0.0 and below. The
0.45 edge is where the game's display rounding turns a system into 0.5, and anything above 0.0
shows as at least 0.1. `Filters` gains `KSpace` (the configured set) and
`Security func(systemID int64) (float64, bool)`.

`Allow` runs the new check after `Linked` and before the payload-only filters, in this order: a
system that is not k-space (or unknown, ID 0) passes; an empty set drops it; all three classes pass
it; otherwise `Security` is asked, a miss passes it, and a hit passes it only when its class is in
the set. The drop reason is `FilterKSpaceSecurity = "kspace_security"`. Deciding k-space by ID means
the two common settings, none and all, never need a lookup.

### One ESI call serves the name and the security status

`universe.Resolver` caches `{name, security}` per system instead of the name alone, from the same
`GET /universe/systems/{id}/`, and gains `Security(ctx, id) (float64, bool)` beside `Name`. Whichever
is asked first fetches; the other is answered from the cache, so a posted k-space kill still costs
one request. `Security` returns false on a failed lookup and caches nothing, like `Name`. Security
status changes only with rare CCP rebalances, and the cache lives as long as the process, as names
do now.

### The relay binds the lookup to the kill's context

`Allow` has no context, and `Linked` needs none. The relay declares `Securities` (`Security(ctx, id)
(float64, bool)`, the resolver) and a `WithSecurities` option. For each kill it copies its filters
and sets `Security` to a closure over the kill's context that calls the resolver and logs a warning
naming the system on a miss. `cmd/chainsniffer` passes the resolver it already builds. Without a
`Securities`, `Security` stays nil and an unresolved class passes, which keeps the relay's tests that
do not care about security unchanged.

- *Alternative:* resolve security in `relay.handle` before `Allow` and pass it in. Rejected: it
  changes `Allow`'s signature for every filter and every test, to serve one.

## Risks / Trade-offs

- [The default changes] → an upgraded relay stops posting k-space kills. The README's upgrade note
  and the PR say so, and `CHAINSNIFFER_KSPACE=high,low,null` restores the old behaviour.
- [ESI is down while some but not all classes are configured] → kills in an unwanted class can post
  for the length of the outage, each with a warning. The user chose this over losing wanted kills.
- [A k-space kill now waits for a lookup before filtering] → only for a system not yet cached, and
  only with a partial set; the same request then names the system, so a posted kill pays nothing
  extra and a dropped one costs one cached request per system per process.
