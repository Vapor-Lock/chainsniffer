# Tasks

## 1. Security classes and the filter

- [ ] 1.1 In `internal/killmail`, add `SecurityClass` (`highsec`, `lowsec`, `nullsec`), `IsKSpace(systemID)` for the 30000000–30999999 range, and `ClassOf(security)` with high at 0.45 and up, low above 0.0, null at 0.0 and below. Verify with table tests at the edges: 0.45, 0.4499, 0.0001, 0.0, -1.0, and IDs 30000142, 30100000 (Zarzakh), 31000005 (Thera), 31002604, 32000001 and 0.
- [ ] 1.2 Add `KSpace` (the configured set) and `Security func(systemID int64) (float64, bool)` to `killmail.Filters`, and the `kspace_security` check in `Allow` right after `Linked`: non-k-space or unknown passes, an empty set drops, all three pass without calling `Security`, a lookup miss passes, a hit passes only for a configured class. Verify with filter tests for each of these, including that `Security` is never called for a wormhole kill or with an empty or full set, and that the drop reason is `kspace_security`.

## 2. Configuration

- [ ] 2.1 Parse `CHAINSNIFFER_KSPACE` in `internal/config` into the class set: trimmed, case-insensitive, duplicates allowed; `none`, unset and empty give the empty set; an unknown word, or `none` with a class, is an error naming the variable. Show the classes in a fixed order (high, low, null), or `none`, in `LogValue`. Verify with config tests: unset, `none`, `Lowsec, nullsec`, `HIGHSEC,highsec`, `lowsec,wormhole`, `none,highsec`, and the summary for unset and for two classes.
- [ ] 2.2 Document `CHAINSNIFFER_KSPACE` in the README's filter table, with the class boundaries, that wormhole kills are never affected, and that an unresolved system is posted with a warning; add an upgrade note that the default relays no k-space kills and `highsec,lowsec,nullsec` restores the old behaviour. Verify by reading the rendered section.

## 3. Security lookup and wiring

- [ ] 3.1 In `internal/universe`, cache `{name, security}` per system from the existing `GET /universe/systems/{id}/` and add `Security(ctx, id) (float64, bool)` beside `Name`, so whichever is asked first fetches and the other is served from the cache; a failure returns false and caches nothing. Verify with httptest tests: `Security` then `Name` (and the reverse) make one request, a negative security decodes, a failure is retried on the next call, an id of 0 makes no request.
- [ ] 3.2 Declare `Securities` and `WithSecurities` in `internal/relay`; per kill, set the filters' `Security` to a closure over the kill's context that asks the resolver and logs a warning naming the system on a miss. Wire `KSpace` from config and the resolver in `cmd/chainsniffer`. Verify with relay tests using a fake: a high-sec kill dropped under `lowsec,nullsec` with reason `kspace_security`, a low-sec kill posted, a failed lookup posted with a warning, a wormhole kill posted under the empty set with no lookup; and an end-to-end test in `cmd/chainsniffer` where the default drops a k-space kill and `CHAINSNIFFER_KSPACE=highsec,lowsec,nullsec` posts it. `nix flake check` passes.

## 4. Live check

- [ ] 4.1 Run the relay against the map with `CHAINSNIFFER_LOG_LEVEL=debug` and `CHAINSNIFFER_KSPACE` unset, then set to a partial combination, and confirm from the posts and the `kspace_security` debug lines that only kills in the configured classes, and every wormhole kill, reach `#kills-in-chain`.
