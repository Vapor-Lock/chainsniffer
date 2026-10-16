# Design

## Context

`add-linked-system-filter` (archived 2026-10-16) gave
chainsniffer an in-memory map model: `topology.Graph`, filled from `GET /api/maps/{map}/systems` on
every stream connect and every `CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL`, and kept current from
`connection_added`, `connection_removed` and `deleted_system`. It journals the events applied while a
snapshot is in flight and replays them onto the snapshot, so a resync never undoes a newer event. Today
all of this is wired only when `CHAINSNIFFER_HOME_SYSTEM_ID` is set; this change wires it always.

Wanderer facts this design relies on (source at `~/dev/vl/wanderer`):

- **Snapshot.** `MapSystemAPIController.index` serializes each system through
  `APIUtils.map_system_to_json`, which includes `temporary_name` (null when unset).
- **Stream event.** Every system update goes through `SystemsImpl.update_system` →
  `update_map_system_last_activity` (`map_server_systems_impl.ex`), which broadcasts
  `:system_metadata_changed` with `system_id`, `solar_system_id`, `name`, `temporary_name`, `labels`,
  `description`, `status`, `locked` and `position_x/y`. Temporary name changes take this path:
  `update_system_temporary_name` from the UI and the API, and the copy from a linked signature. Status,
  tag, label and the other updates take it too, so most of these events don't change the alias. The
  event is in `MapEventsAPIController`'s allowlist, so `events=` can request it.
- **Not emitted.** `add_system` payloads carry only `solar_system_id` and position. Removing a system
  calls `cleanup_temporary_name!` without an event, but `deleted_system` follows. Adding a system with
  "extra info" (`maybe_update_extra_info`, used when copying systems between maps) sets
  `temporary_name` directly, with no metadata event.

## Goals / Non-Goals

**Goals:**
- An alias that is current when the kill is posted, from the same snapshot and stream the linked-system
  filter already uses, with no new endpoints, settings or connections.
- The resync guarantee covers aliases too: a snapshot never reverts a rename the stream already
  delivered.

**Non-Goals:**
- Editing an already posted embed when an alias is set or changed later.
- Wanderer's `custom_name` (renamed system) and signature temporary names. Only the system's
  `temporary_name` is the chain alias.

## Decisions

### Aliases live in `topology.Graph`
The graph gets an `aliases map[int64]string` under its existing mutex, and an `Alias(id int64) string`
accessor that returns `""` for none. Alias changes become two more journalled ops (`opSetAlias`,
`opClearAlias`, and `opRemoveSystem` also deletes the alias), so `Install` replays them onto a
snapshot exactly as it replays connection changes. `Install` replaces the alias map from the snapshot
before replaying.
- **Why:** an alias store has the same race as the connection graph, a rename landing while a resync
  is in flight. The graph already solves that with its journal and its `resyncMu`. A separate store
  would have to copy that code, or else risk a periodic snapshot reverting a fresh rename for up to
  15 minutes.
- **Alternative:** a separate `internal/aliases` package with its own snapshot and journal. It would
  be cleaner in name only: one snapshot fetch would feed two installers, which would need to share a
  resync lock.

### The topology is always wired; the home system only enables the filter
`cmd/chainsniffer` always builds the graph, subscribes to every topology event type, runs the
snapshot in the connect hook and starts the periodic resync. `CHAINSNIFFER_HOME_SYSTEM_ID` decides
only whether `Graph.Linked` is installed as `Filters.Linked`. `topology.New(0)` builds a graph with
no home system: `Linked` is never consulted, the BFS and the linked-set debug diff are skipped, the
"home system is not on the map" warning is not logged, and the snapshot info line leaves out
`linked_systems`.
- **Why:** aliases are wanted on every map, home system or not (the user's call), and they need the
  same snapshot, events and resync race handling that the topology already has. One wiring path is
  simpler to test than two, where the topology would be built with a home system and aliases alone
  without one.
- **Alternative:** without a home system, subscribe to `system_metadata_changed` and `deleted_system`
  only, and skip the connection events. That saves a little traffic but adds a second subscription
  list and a second set of tests. The connection events are cheap to apply even when nothing reads
  the linked set.
- **Consequence:** a deployment without a home system gains a REST dependency. A snapshot failure on
  connect delays the stream through the usual backoff, and a 401/403/404 from `/systems` exits 1.
  The stream endpoint already uses the same token and map, so a configuration that streams can be
  expected to snapshot too.

### Subscribe to `system_metadata_changed`; keep ignoring `add_system`
`topology.EventTypes` gains `system_metadata_changed`, so the wiring's subscription picks it up
unchanged, and `relay.Handle` dispatches it to the graph like the other topology events. `Apply`
reads `solar_system_id` with the existing lenient `fieldID` and `temporary_name` as a JSON string or
null. Anything else in the payload is ignored. The trimmed value sets the alias; null, missing or
blank clears it. A payload without a usable `solar_system_id` is warned about and skipped, as for
the other topology events.
- **Why:** it is the only event that reports a temporary name, and it carries the whole current
  value rather than a diff, so applying it is idempotent and a status-only update rewrites the same
  alias. The debug log line is emitted only when the alias actually changes, so busy maps don't spam
  it.
- `add_system` would add nothing: its payload has no name. The one path that creates a system with a
  name and emits no metadata event (copying systems between maps) is covered by the next resync.

### The alias goes in the System field: `J134349 (HOTEL)`
`discord.Render` takes the alias as a fourth argument and renders the System field value as
`<systemName> (<alias>)`, or as `systemName` alone when the alias is empty. A system with no alias
therefore renders exactly as today, and the existing golden files don't change. Before rendering, the alias is escaped
for Discord markdown (`*`, `_`, `~`, `` ` ``, `|`, `\`) so that `A_B_` doesn't italicise, and capped
at 100 characters to stay well inside Discord's 1024-character field limit. A field value over the
limit would make Discord reject the whole embed and lose the kill.
- **Why in the System field:** it is compact, reads the way pilots say it, and keeps the embed's
  field count unchanged (the user's choice).
- **Alternative:** a separate `Chain alias` inline field after `System`. It's cleaner to parse, but it
  takes another slot in the inline grid for a few characters.

### The relay looks aliases up through an interface it declares
`relay` gains `type Aliases interface { Alias(id int64) string }` and a `WithAliases` option. Without
one, the alias is always `""`. `handle` looks the alias up after the filters pass, next to
`resolver.Name`, and the "posted killmail" info line gains a `chain_alias` attribute when it is
non-empty. `cmd/chainsniffer` always passes the same `*topology.Graph` to `WithTopology` and
`WithAliases`.
- **Why:** it follows the convention that packages depend only on interfaces they declare, and it
  keeps the relay's tests on fakes. `Topology` stays the narrow `Apply` interface it is.

### The snapshot carries aliases beside the system IDs
`wanderer.Snapshot` gains `TemporaryNames map[int64]string`, filled from each system's
`temporary_name` (only non-empty values). `topology.Snapshot` gains `Aliases map[int64]string`, and
`snapshotFetcher` in `main` copies it across. `Systems []int64` stays as it is, so the existing
callers and tests keep working.

## Risks / Trade-offs

- **[Kill before alias]** A system is linked and gets a kill before anyone names it. → The embed
  simply has no alias field. That's correct at that moment, and later kills get the name.
- **[Missed metadata event]** As with topology, the stream can lose an event around a reconnect.
  → Every reconnect snapshot reloads all aliases, and the periodic resync bounds a stale alias to
  `CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL`.
- **[More stream traffic]** `system_metadata_changed` fires for every status, tag, label and position
  update, not just temporary names, and each one advances the cursor and writes `state.json`. → On a
  map with a few hundred systems and human-paced edits this is a handful of events a minute, the same
  order as the connection events already subscribed to.
- **[Payload shape drift]** The event payload is an internal Elixir map, not a versioned schema. → It
  is decoded leniently like the other topology events, and the snapshot heals any drift on the next
  resync.

## Migration Plan

No configuration change. After deployment, the first connect subscribes to the extra event types and
its snapshot loads the aliases. Deployments without a home system now also fetch `/systems`, so their
token must be allowed to read it. It is the same map token, and the stream needs the same access. The cursor format is unchanged. A rollback to the previous binary
resumes from the saved cursor with the old subscription.
