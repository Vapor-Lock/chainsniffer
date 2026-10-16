# Proposal

## Why

Wanderer emits a `map_kill` event for every system on the map, whether or not that system is still
connected to the home system. Unlinked islands — old chains nobody has cleaned up, trade hubs and
staging systems pinned for reference — are on the map too, so chainsniffer relays kills there and the
channel fills with noise from space the corporation is not connected to. Only kills in systems
reachable from home through the map's connections are worth posting.

## What Changes

- New optional setting `CHAINSNIFFER_HOME_SYSTEM_ID`: the EVE solar system ID of the home system.
  While it is unset, nothing in this change applies and chainsniffer behaves exactly as today.
- With a home system configured, chainsniffer keeps an in-memory model of the map's topology:
  - **Built from a snapshot.** At startup it fetches the map's systems and connections from
    Wanderer's REST API (`GET /api/maps/{map}/systems`, which returns both) and walks the
    connections out from the home system to find every system linked to it.
  - **Maintained from the stream.** It also subscribes to `deleted_system`, `connection_added` and
    `connection_removed` events on the same SSE connection that carries the kills, and recomputes
    the linked set whenever the topology changes. A connection removed mid-chain unlinks everything
    behind it, and a new connection back to home links an island again. `add_system` is not needed:
    a newly added system has no connections, so it cannot change what is linked until a
    `connection_added` event follows.
  - **Resynchronised.** It takes a fresh snapshot on every stream (re)connection and periodically
    (`CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL`, default `15m`). Wanderer's stream can miss events
    around a reconnect, so this is a safety net.
- New filter `unlinked_system`: a killmail whose solar system is not in the linked set is dropped and
  logged like every other filtered kill. The home system itself always counts as linked.
- The stream request changes from `events=map_kill` to also name the three topology event types
  when a home system is configured. The cursor advances over topology events as it does over kills, so a
  restart resumes after them.
- A home system ID that is not on the map is not an error. It is logged as a warning, and only
  kills in the home system itself pass until the system is added.

## Capabilities

### New Capabilities
- `chain-topology`: the in-memory model of the map's systems and connections, built from a REST
  snapshot, maintained from SSE topology events, periodically resynchronised, and the set of
  systems linked to the home system derived from it.

### Modified Capabilities
- `wanderer-event-stream`: the subscription requests topology events alongside `map_kill` when a home
  system is configured, and hands them to the relay. The cursor covers them too.
- `killmail-filtering`: adds the linked-system filter and its `unlinked_system` drop reason.
- `relay-runtime`: adds `CHAINSNIFFER_HOME_SYSTEM_ID` and `CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL` to
  the configuration, and the topology snapshot to the startup sequence. A snapshot that Wanderer
  refuses for a reason no retry can fix exits 1.

## Impact

- **Code:** `internal/config` (two settings), `internal/wanderer` (snapshot request, event types on
  the stream), a new `internal/topology` package (graph, reachability, event decoding),
  `internal/killmail` (the filter), `internal/relay` (dispatching topology events), and
  `cmd/chainsniffer` (wiring, startup snapshot, resync ticker).
- **Wanderer API:** chainsniffer starts calling `GET /api/maps/{map}/systems` with the same map API
  key it already uses for the stream. The endpoint sits behind the same switch
  (`WANDERER_PUBLIC_API_DISABLED`) as the stream itself, so no instance offers one without the
  other.
- **Behaviour for existing deployments:** none until `CHAINSNIFFER_HOME_SYSTEM_ID` is set.
- **Dependencies:** none new.
- **Docs:** README configuration tables and limitations.
