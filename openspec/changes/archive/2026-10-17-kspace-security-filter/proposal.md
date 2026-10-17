# Proposal

## Why

A chain reaches into known space through its high-sec, low-sec and null-sec exits, and the
home-chain filter relays every kill in those systems. A corporation living in wormholes usually
cares about its own space and maybe one kind of k-space neighbour, not the steady traffic of a
high-sec trade hub on a C2 static. There is no way today to say which parts of k-space are worth
a post.

## What Changes

- A new optional setting, `CHAINSNIFFER_KSPACE`, lists the k-space security classes whose kills are
  relayed, as a comma-separated combination of `highsec`, `lowsec` and `nullsec`, for example `lowsec,nullsec`.
  The value `none` relays no k-space kills.
- **BREAKING:** unset (or empty) means `none`. A relay upgraded without setting it stops posting
  k-space kills and posts wormhole kills only. `CHAINSNIFFER_KSPACE=highsec,lowsec,nullsec` restores today's
  behaviour.
- A k-space system's class comes from its security status: high-sec at 0.45 and above (shown as
  0.5 to 1.0 in game), low-sec above 0.0 and below 0.45, null-sec at 0.0 and below. Pochven and
  Zarzakh, at -1.0, count as null-sec.
- Kills in wormhole space, Thera included, are never affected. Whether a system is k-space is
  known from its ID alone, so a relay set to `none`, or to all three classes, never looks a
  security status up.
- When a k-space system's security cannot be resolved within the lookup timeout, the kill is
  posted as if the filter passed it, and a warning is logged. A filter that cannot decide never
  costs a kill.
- A dropped kill reports the reason `kspace_security`, like the other filters. The setting is
  shown in the startup summary, and an unknown word, or `none` combined with a class, is a
  configuration error that exits with status 2.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `killmail-filtering`: adds a "K-space security filter" requirement.
- `relay-runtime`: "Filter configuration" gains `CHAINSNIFFER_KSPACE`, and its defaults scenario
  no longer posts k-space kills.

## Impact

- `internal/config`: parse and validate `CHAINSNIFFER_KSPACE`; include it in the startup summary.
- `internal/killmail`: the security classes, the k-space test by system ID, and the filter in
  `Filters.Allow`, fed by a security lookup function the relay supplies.
- `internal/universe`: the system lookup also returns `security_status`, cached with the name, so
  the existing call serves both and adds no request for a system already named.
- `internal/relay` and `cmd/chainsniffer`: wire the lookup into the filters.
- `README.md`: the setting, the class boundaries, and an upgrade note for the new default.
