# Changelog

All notable changes to chainsniffer are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before 1.0, a minor release may change
defaults: read its **Upgrading** section before moving to it.

## [0.2.1] - 2026-10-18

The first release published as an image, `ghcr.io/vapor-lock/chainsniffer:0.2.1`. It carries
everything listed for 0.2.0, which was tagged but never published.

### Upgrading

- **`components` is the default card style.** Kills are now posted as a native Discord components
  card. Set `CHAINSNIFFER_CARD_STYLE=block` to keep the aligned monospace cards of 0.1.0.
- **The `strip` card style needs the Attach Files permission** in the relay's channel. The startup
  check stops the relay with a clear error when `strip` is configured and the permission is missing.
- **`CHAINSNIFFER_KSPACE` defaults to `none`,** so an upgraded relay no longer posts kills in known
  space. Set `CHAINSNIFFER_KSPACE=highsec,lowsec,nullsec` to keep the earlier behaviour.
- The state directory now also holds `names.json`, a name cache, and `last_kill.json`, the last
  posted kill. Both are written beside `state.json`; no action is needed if the directory is
  writable, as it already had to be.

### Added

- `CHAINSNIFFER_CARD_STYLE` chooses the kill card layout: `components` (the default), `strip` (the
  components card with a rendered portrait strip of the victim and the final blow), `block` or
  `fields`. Unknown values exit with status 2.
- Corporation, alliance and character names and tickers missing from Wanderer's payload are looked
  up from ESI and kept in `names.json`, so a restart does not ask again. A failed lookup leaves the
  line out and the kill is still posted.
- Character portraits and corporation and alliance logos from the EVE image server, cached in memory.
- `CHAINSNIFFER_KSPACE` relays kills in known space by security class: any of `highsec`, `lowsec`
  and `nullsec`, or `none`. Wormhole space is never affected. A dropped kill logs
  `filter=kspace_security`.
- Every posted kill is saved to `last_kill.json`. `CHAINSNIFFER_REPLAY_CHANNEL_ID` starts a replay
  that posts it to a test channel with the running build and configuration, then exits.

### Changed

- The kill card body is an aligned monospace block in the `block` style, and every card is padded to
  the same width. The older inline-field layout is still available as `fields`.
- The Victim row names the pilot above the corporation and alliance.

### Fixed

- Kill cards no longer lose the victim's and final blow's corporation and alliance when Wanderer
  sends their IDs without names.
- The arm64 image builds: the strip image's golden test tolerates the one-level antialiasing
  differences arm64 produces when rendering text, and CI now runs on arm64 as well as amd64.

## [0.2.0] - 2026-10-17

Tagged as `v0.2.0`, but never published: its release failed on the arm64 build. Its changes ship
in 0.2.1, listed above.

## [0.1.0] - 2026-10-16

Tagged as `v0.1.0`, but never published as an image. Build it from the tag with
`nix build .#image` if you need it.

### Added

- Relay of killmails from one Wanderer map's SSE stream to one Discord channel, with a persisted
  cursor and a dedupe set for at-most-once posting.
- Classification of each kill against the friendly corporation and alliance IDs, shown in the
  card colour.
- `CHAINSNIFFER_HOME_SYSTEM_ID`: relay only kills in systems linked to the home system on the map.
- Chain aliases: the System line shows each wormhole's temporary name from the map.
- A container image built with Nix, running as a non-root user with a state volume.

[0.2.1]: https://github.com/Vapor-Lock/chainsniffer/compare/v0.1.0...v0.2.1
[0.2.0]: https://github.com/Vapor-Lock/chainsniffer/releases/tag/v0.2.0
[0.1.0]: https://github.com/Vapor-Lock/chainsniffer/releases/tag/v0.1.0
