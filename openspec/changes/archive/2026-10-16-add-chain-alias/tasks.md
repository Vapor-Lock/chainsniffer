# Tasks

## 1. Snapshot aliases (`internal/wanderer`)

- [x] 1.1 Decode each system's `temporary_name` in `Client.Snapshot` into a new `Snapshot.TemporaryNames map[int64]string`. Keep only non-empty values after trimming whitespace, and leave `Systems` unchanged. Verify by extending the `MapSystemAPIController.index` response fixture with one named system, one with `"temporary_name": null` and one with `"  "`: the decode test asserts that only the named system is in the map, and the existing snapshot tests still pass.

## 2. Aliases in the graph (`internal/topology`)

- [x] 2.0 Let `topology.New(0)` build a graph with no home system: no BFS and no linked-set debug diff, no "home system is not on the map" warning, and no `linked_systems` in the snapshot info line. Connection ops still apply to the adjacency. Verify with tests that a home-less graph applies all three topology events and installs snapshots without the warning, and that the existing home-system tests are unchanged.
- [x] 2.1 Add `Snapshot.Aliases`, an alias map in `Graph`, and `Alias(id int64) string`. `Install` replaces every alias from the snapshot and reports the alias count in its "loaded map topology" info line. Verify with tests for `chain-aliases` "Aliases from the snapshot" (named system, stale alias cleared, blank name) and "Alias logging" (the alias count in the snapshot line).
- [x] 2.2 Handle `system_metadata_changed` in `Apply`. Decode `solar_system_id` with `fieldID` and `temporary_name` as a string or null: the trimmed value sets the alias, and null, missing or blank clears it. A payload without a usable ID logs a warning and changes nothing. Make `deleted_system` also clear the alias, and add the event to `EventTypes`. Record alias changes as journalled ops so `Install` replays them, and log at debug only when an alias actually changes. Verify with tests for every scenario under `chain-aliases` "Alias changes from the stream" and "Alias changes during a snapshot are not lost", using a `system_metadata_changed.json` fixture shaped like the payload broadcast from `update_map_system_last_activity` in Wanderer's `map_server_systems_impl.ex`. Then extend the existing concurrent `go test -race` test with metadata events.

## 3. Embed field (`internal/discord`)

- [x] 3.1 Give `Render` an `alias string` argument and render the System field as `<systemName> (<alias>)`, or as `systemName` alone when the alias is empty. Escape the alias for Discord markdown (`\`, `*`, `_`, `~`, `` ` ``, `|`) and cap it at 100 characters. Update the doc comment and the callers. Verify with:
  - a new golden test for a kill in J134349 with alias `HOTEL` (`J134349 (HOTEL)`);
  - a test for an unresolved name (`31001234 (HOTEL)`);
  - a test that `A_B_` renders escaped;
  - a test that a 300-character alias is capped;
  - the existing goldens (empty alias) staying byte-for-byte unchanged.

## 4. Relay lookup and dispatch (`internal/relay`)

- [x] 4.1 Add the `system_metadata_changed` constant and dispatch it to the `Topology`, as the other topology events are dispatched. Declare `Aliases interface { Alias(id int64) string }` with a `WithAliases` option. In `handle`, look the alias up after the filters pass, pass it to `Render`, and add `chain_alias` to the "posted killmail" log line when it is non-empty. Update the package doc. Verify with fake-based tests:
  - a `system_metadata_changed` event reaches the fake topology and advances the cursor (`wanderer-event-stream` "Metadata events advance the cursor");
  - a kill whose system has an alias in the fake posts an embed whose System field reads `<name> (<alias>)`;
  - with no `Aliases` configured, the System field shows the name alone.

## 5. Wiring and docs (`cmd/chainsniffer`, README)

- [x] 5.1 Always wire the topology. Build the graph (`topology.New(cfg.HomeSystemID)`, 0 when unset) and always pass `WithEventTypes`, `WithConnectHook` and the periodic resync. Pass the graph to the relay via `WithTopology` and `WithAliases`. Install `Graph.Linked` as `Filters.Linked` only when a home system is set. In `snapshotFetcher`, copy `TemporaryNames` into `topology.Snapshot.Aliases`. Make the startup summary always include the resync interval. Verify with `main_test.go` end-to-end tests against the `httptest` Wanderer and the fake Discord:
  - with and without a home system, the stream request carries `events=map_kill,deleted_system,connection_added,connection_removed,system_metadata_changed` and `/systems` is fetched on connect;
  - a kill in a system the `/systems` fixture names `HOTEL` posts `<name> (HOTEL)` in the System field, both with and without a home system;
  - after a mid-stream `system_metadata_changed` renames it `INDIA`, the next kill shows `INDIA`;
  - without a home system, a kill in an island system is still posted (no linked filter);
  - a 401 from `/systems` exits 1 without a home system too.

  Update the existing test that asserted no `/systems` request without a home system.
- [x] 5.2 Document in the README:
  - the chain alias in the System field;
  - that the topology is now always loaded and the home system only enables the filter;
  - the REST dependency without a home system;
  - under Limitations, that an alias set after a kill is posted doesn't edit that post, and that a missed rename heals within one resync interval.

  Update the `CHAINSNIFFER_HOME_SYSTEM_ID` and `CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL` descriptions to match. Verify by reading the rendered sections against the design's Risks.

## 6. Integration

- [x] 6.1 Run `nix flake check` and confirm that the tests, vet, golangci-lint and the image build all pass.
- [x] 6.2 Run the built binary against `wanderer.vaporlock.space` map `vla` with debug logging, once with the home system set and once without. Confirm that the snapshot line reports a non-zero alias count, and that renaming a system's temporary name on the map logs the alias change at debug. If a kill lands in a named system, confirm that its post shows the alias.

## Workflow follow-up

- Turn this change into a beads epic with the `openspec-to-epic` skill and let agents work it. 6.2 is for the captain and user, not an agent.
- Archive the change with `/opsx:archive` once the epic is closed and merged.
