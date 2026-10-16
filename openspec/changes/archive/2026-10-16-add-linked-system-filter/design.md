# Design

## Context

See proposal.md for the motivation and the specs for the requirements. This section records what
Wanderer actually does, read from its source (`~/dev/vl/wanderer` at `05295cc5`) and confirmed
against `wanderer.vaporlock.space`, map `vla`.

- **Snapshot endpoint.** `GET /api/maps/{map}/systems` (`MapSystemAPIController.index`) answers
  `{"data": {"systems": [...], "connections": [...]}}`. Systems carry `solar_system_id`; connections
  carry `solar_system_source`, `solar_system_target` and `type` (0 wormhole, 1 stargate). Only
  visible systems are listed. The route uses the `:api` and `:api_map` pipelines, so it accepts the
  same map API key as the stream. Its content negotiation wants `Accept: application/json`, and it
  answers 403 `Public API is disabled` under the same switch that also guards the stream. On `vla`
  the endpoint lists 22 systems and 17 connections, and 16 of the systems are linked to J142845.
- **Topology events.** In the default `legacy` stream format, a payload is the plain map handed to
  `ExternalEvents.broadcast`:
  - `connection_added`: `{connection_id, solar_system_source_id, solar_system_target_id, type, …}`
  - `connection_removed`: `{connection_id, solar_system_source_id, solar_system_target_id}`
  - `deleted_system`: `{solar_system_id, name: null, …}`

  The REST and event field names differ (`solar_system_source` versus `solar_system_source_id`).
- **Emit paths.**
  - Every connection is created in one place (`ConnectionsImpl.maybe_add_connection`, reached from
    jumps, the UI and the API), and that function emits `connection_added`.
  - Every connection is removed through `maybe_remove_connection`, reached from the UI, the API and
    the EOL auto-expiry in `cleanup_connections`, and that function emits `connection_removed`.
  - The exception is a system deletion, including the automatic expiry of idle systems.
    `SystemsImpl.delete_systems` emits `deleted_system` and then removes the system's connections
    in `remove_system_connections` *without* emitting `connection_removed`.
  - Map import and duplication write the database directly and emit nothing.
- **Stream ordering.** `EventsController` sends `connected`, then the backfill, and only then
  subscribes to the live PubSub topic. An event broadcast in that gap reaches neither the backfill
  nor the live stream.
- **chainsniffer today.**
  - `wanderer.Client.Run` requests `events=map_kill`, logs `connected` and calls the handler for
    `map_kill` only.
  - `relay.Handle` decodes kills, filters them, posts them and then advances the cursor.
  - `killmail.Filters` is a plain struct of filter settings.

## Goals / Non-Goals

**Goals:**
- A linked set that is right after every handled event and that heals any drift within one resync
  interval.
- No kill is filtered against a topology that hasn't been loaded yet.
- Fit the existing seams: the relay composes, and other packages depend only on interfaces they
  declare.

**Non-Goals:**
- More than one home system, or a home given by name rather than ID.
- Distance limits ("only within N jumps of home") or filtering on connection properties (EOL, mass,
  ship size). The graph would make these easy later, but none is asked for now.
- Judging a replayed kill by the topology at kill time. The filter reads the topology as it is when
  the kill is processed (see the killmail-filtering spec).
- Persisting the topology. A snapshot on every connection makes a persisted copy redundant.

## Decisions

### Recompute reachability with a BFS on every change
`internal/topology` keeps an undirected adjacency map plus the linked set. Every change (an event or
a snapshot) re-runs a breadth-first search from home and swaps the result in.
- **Why:** a map holds tens to a few hundred systems, so a BFS costs microseconds. Removing an edge
  incrementally means working out whether the far side still has another path home, which is the
  same search with more code.
- **Alternative:** incremental union-find. It can't delete edges, so it would need rebuilding on
  every `connection_removed` anyway.

### Key connections by their unordered system pair, not by connection ID
- **Why:** both the snapshot and the events carry the two system IDs. Wanderer looks connections up
  by pair (`Map.find_connection(map_id, a, b)`) and holds at most one per pair. Keying by pair also
  makes direction irrelevant, as the spec requires.
- **Alternative:** `connection_id`. The cascade deletes don't report connection IDs, so this would
  still need a pair index to handle `deleted_system`.

### Ignore `add_system`; make `deleted_system` drop the system's edges
A new system arrives with no connections, so reachability can't change until a `connection_added`
follows. `deleted_system` must remove every edge touching the system itself, because Wanderer's
cascade emits no `connection_removed` for them.

### Snapshot through a connect hook on `wanderer.Client`
`wanderer.Client` gains `WithConnectHook(func(ctx) error)`. The client calls it synchronously when
the `connected` event arrives, with the staleness watchdog paused, before it reads the next event.
- An error from the hook ends the connection like any other connection failure, so the backoff
  applies.
- A `*FatalError` from the hook ends `Run`.
- The `connected` event still never reaches the handler, so its ID never moves the cursor. It is
  not one of Wanderer's relay events, so a `last_event_id` pointing at it would replay nothing
  useful.
- **Why:** the hook gives the snapshot-before-first-event ordering for free on every reconnect. A
  snapshot that fails leaves every event of that connection unprocessed and the cursor where it was,
  so Wanderer replays the kills on the next connection. Nothing is filtered against an unloaded
  topology.
- **Alternative:** one snapshot in `main` before `Run`. It covers only startup, and a reconnect after
  a long drop would filter against a stale graph.
- **Alternative:** deliver `connected` to the relay handler. That mixes a non-relay event into the
  cursor logic.

### Fetch the snapshot in `internal/wanderer`; decode payloads in `internal/topology`
`wanderer.Client.Snapshot(ctx)` builds the authenticated `GET …/systems` request with
`Accept: application/json`, classifies the status and returns the decoded system IDs and connection
pairs. `statusError` is reused, so 401, 403 and 404 come back as `*FatalError`. The hints need a
wording that fits REST as well as the stream. `internal/topology` decodes the three event payloads,
just as `internal/killmail` owns the kill payloads.
- **Why:** `internal/wanderer` already owns the base URL, the token, the HTTP client and the status
  classification. `internal/topology` stays free of HTTP and is tested with in-memory events.

### Survive events that arrive while a snapshot is in flight
A periodic resync runs on its own goroutine (a ticker in `main`), while events are applied on the
stream goroutine. Fetching a snapshot and simply replacing the graph would undo any event applied
while the request was in flight. `topology.Graph` therefore records each change applied between
`BeginResync()` and `Install(snapshot)`, and `Install` replays them on top of the snapshot before it
swaps in the new linked set.
- Every change is an absolute set operation (add pair, remove pair, remove system), so replaying an
  event the snapshot already reflects changes nothing. The result converges whichever side of the
  snapshot the event really happened on.
- A mutex guards the graph, and a separate mutex serializes resyncs, so a connect-hook snapshot and
  a periodic one never overlap.
- **Alternative:** run the periodic resync on the stream goroutine. On a quiet map only keepalives
  arrive and they never reach the handler, so the resync would have to be wired into the SSE
  reader.

### `Linked` as a predicate on `killmail.Filters`
`killmail.Filters` gains `Linked func(systemID int64) bool`; nil disables the filter. The relay
wires in `topology.Graph.Linked`.
- The new check runs first in `Allow`. On a map with islands it's the most common reason to drop a
  kill, so the logs name it first.
- `killmail.Extract` fills `SolarSystemID` from the payload's top-level `solar_system_id` when an
  entry lacks one. A kill whose system is still unknown has ID 0, which is never linked.
- **Why:** this keeps `internal/killmail` free of any topology import, consistent with the "depend on
  interfaces you declare" convention.

### Relay dispatch
`relay.Handle` switches on the envelope type. `map_kill` goes through the existing pipeline. The
three topology types go to a `Topology` interface that the relay declares (`Apply(eventType string,
payload json.RawMessage)`). Every type then advances the cursor and saves, so a restart doesn't
replay topology events it has already applied. A replayed event would be harmless anyway, but it
would re-trigger debug logging.

### Configuration
- `CHAINSNIFFER_HOME_SYSTEM_ID` is an `int64` and must be positive when set; 0 means unset.
- `CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL` is a Go duration, must be positive, and defaults to `15m`.
  It is validated even without a home system, so a typo doesn't hide until the feature is switched
  on.
- `main` builds the subscription with `wanderer.WithEventTypes(...)` and only adds the topology
  types, the connect hook, the ticker and the filter predicate when a home system is set.

## Risks / Trade-offs

- **[Wanderer's subscribe gap]** An event broadcast between `connected`/backfill and the PubSub
  subscription is lost. → The connect snapshot is taken while that gap is open, so it already
  reflects anything broadcast before it. Only an event in the few milliseconds between the snapshot
  and the subscription can still slip through, and the periodic resync bounds that drift to one
  interval.
- **[Events Wanderer never emits]** Map import or duplication, and direct database edits, change
  connections silently. → The same resync covers them.
- **[REST outage while the stream is up]** On connect, a failed snapshot blocks the connection.
  Wanderer's backfill holds 10 minutes and 1000 events, so a longer API outage loses kills, just as a
  stream outage does today. → Backoff and warning logs. A failed periodic resync keeps the old graph
  instead of blocking.
- **[Home system typo]** A valid-looking but wrong ID passes only kills in that one system. → Each
  snapshot logs a warning when the home system isn't on the map, and the info line reports the
  linked count, so "1 linked system" stands out.
- **[Topology at processing time]** A kill replayed after its system was cut off is dropped, and one
  in a system linked after the kill is posted. → An accepted trade-off, documented in the spec.
- **[Payload drift]** Event payloads are internal Elixir maps, not a versioned schema. → Decode
  leniently and skip malformed events with a warning. Keep fixtures copied from the source files
  listed in Context.

## Migration Plan

The change is opt-in: deploy the new image unchanged, then set `CHAINSNIFFER_HOME_SYSTEM_ID` and
restart. To roll back, unset the variable or run the previous image.

The state file format doesn't change. The cursor may now point at a topology event, which is still a
Wanderer ULID, so an older binary resumes from it correctly with `events=map_kill`.
