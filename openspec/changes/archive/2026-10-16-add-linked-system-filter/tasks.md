# Tasks

## 1. Configuration (`internal/config`)

- [x] 1.1 Add `CHAINSNIFFER_HOME_SYSTEM_ID` (optional positive `int64`; 0 or absent means unset) and `CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL` (positive Go duration, default `15m`, validated even without a home system). Add both to the redacted startup summary. Verify with table tests for every scenario under `relay-runtime` "Topology configuration": unset, valid, `J142845`, `0`, `soon`, a negative interval, and errors reported together with other invalid variables.
- [x] 1.2 Document both variables in the README configuration tables, with a sentence on how linking is decided. Verify that the README tables list them with the defaults the tests assert.

## 2. Topology graph (`internal/topology`)

- [x] 2.1 Create the package with a `Graph`: a home system, an undirected adjacency keyed by unordered system pair, and a linked set recomputed by BFS from home after every change. Include `Linked(id) bool` (home always true, 0 never) and a mutex so `Linked` is safe from any goroutine. Verify with tests for every scenario under `chain-topology` "Linked systems".
- [x] 2.2 Implement `Apply(eventType string, payload json.RawMessage)` for `connection_added`, `connection_removed` (`solar_system_source_id`/`solar_system_target_id`) and `deleted_system` (`solar_system_id`, dropping all of the system's edges). Decode leniently: a missing or non-integer ID logs a warning and changes nothing, and unknown types are ignored. Log linked/unlinked transitions at debug. Verify with tests for every scenario under `chain-topology` "Topology events", using fixture payloads shaped like the `ExternalEvents.broadcast` calls in Wanderer's `map_server_connections_impl.ex` and `map_server_systems_impl.ex`.
- [x] 2.3 Implement the resync protocol: `BeginResync()` starts journalling the changes `Apply` makes, and `Install(Snapshot)` replaces the graph with the snapshot's, replays the journal, recomputes the linked set, and logs the systems and linked counts at info (plus a warning when home is absent). Verify with tests for "Snapshot replaces stale topology", "Events during a snapshot are not lost", "Home system not on the map" and "Topology logging", and run `go test -race` over a test that applies events concurrently with `Install`.

## 3. Wanderer client (`internal/wanderer`)

- [x] 3.1 Add `WithEventTypes(...string)` and have `request` send them as the comma-separated `events` parameter, keeping `map_kill` as the default. Hand every requested type to the handler instead of only `map_kill`, and update the package doc and the `Event*` comments. Verify with tests for both scenarios of the modified "Authenticated map kill subscription" and a test that a `connection_removed` event reaches the handler.
- [x] 3.2 Add `WithConnectHook(func(context.Context) error)`, called synchronously on the `connected` event with the watchdog paused. A hook error ends the connection and retries with backoff. A hook `*FatalError` ends `Run`. The `connected` event never reaches the handler. Verify with `httptest` tests: the hook runs before the first event is handled, a failing hook reconnects (and backs off) without handling that connection's events, a fatal hook error is returned from `Run`, and the hook runs again on every reconnect.
- [x] 3.3 Add `Client.Snapshot(ctx) (Snapshot, error)`: `GET {base}/api/maps/{map}/systems` with `Authorization: Bearer` and `Accept: application/json`, decoding `data.systems[].solar_system_id` and `data.connections[].solar_system_source`/`solar_system_target`. Classify statuses through `statusError`, with 401, 403 and 404 fatal and the rest retryable, and make the hints fit a REST request too. Verify with `httptest` tests using a response fixture shaped like `MapSystemAPIController.index`: a 200 decode, 401 and 404 returning `*FatalError`, a 403 `Public API is disabled`, a 502 that isn't fatal, and the token absent from every error string.

## 4. Linked-system filter (`internal/killmail`)

- [x] 4.1 Add `Filters.Linked func(int64) bool` and `FilterUnlinkedSystem = "unlinked_system"`, checked first in `Allow`; a nil `Linked` passes everything. Make `Extract` fall back to the payload's top-level `solar_system_id` when an entry has none. Verify with tests for every scenario under `killmail-filtering` "Linked-system filter".

## 5. Relay dispatch (`internal/relay`)

- [x] 5.1 Declare a `Topology` interface (`Apply(eventType string, payload json.RawMessage)`) and a `WithTopology` option. In `Handle`, send `deleted_system`, `connection_added` and `connection_removed` to it and `map_kill` through the existing pipeline. Advance the cursor and save after every event type. Update the package doc. Verify with fake-based tests: a topology event reaches the fake and advances the cursor ("Topology event advances the cursor"), and a topology event with no `Topology` configured is ignored but still advances the cursor.

## 6. Wiring (`cmd/chainsniffer`)

- [x] 6.1 When a home system is set, build a `topology.Graph` and pass the following: `WithEventTypes` with the three topology types, `WithConnectHook` (resync: `BeginResync`, `Snapshot`, `Install`, serialized with the periodic resync), the graph to the relay via `WithTopology`, and `Graph.Linked` to `Filters.Linked`. Start a ticker goroutine at the resync interval that logs a warning when a snapshot fails and keeps the old graph. Without a home system, wire none of this. Verify with `main_test.go` end-to-end tests against an `httptest` Wanderer (stream plus `/systems`) and a fake Discord:
  - a kill in an island is dropped and one in a linked system is posted;
  - a `connection_removed` event mid-stream unlinks a system for the next kill;
  - a 401 from `/systems` exits 1;
  - with no home system, `/systems` is never requested and the stream asks for `map_kill` only.
- [x] 6.2 Add to the README's Limitations: kills are judged by the topology at processing time, and missed topology changes heal within one resync interval. Mention the topology summary line in the operation notes. Verify by reading the rendered sections against the design's Risks.

## 7. Integration

- [x] 7.1 Run `nix flake check` and confirm it passes: tests, vet, golangci-lint and the image build.
- [x] 7.2 Run the built binary against `wanderer.vaporlock.space` map `vla` with `CHAINSNIFFER_HOME_SYSTEM_ID=31000727` and debug logging. Confirm that the snapshot line reports 22 systems or so, with 16 linked (the numbers drift with the live map). Confirm that a kill in an unlinked system (for example Amarr) logs `filter=unlinked_system` rather than being posted.

## Workflow follow-up

- Turn this change into a beads epic with the `openspec-to-epic` skill and let agents work it.
- Archive the change with `/opsx:archive` once the epic is closed and merged.
