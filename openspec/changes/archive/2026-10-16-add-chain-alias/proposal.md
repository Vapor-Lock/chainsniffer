# Proposal

## Why

Pilots name the systems in their chain. A Wanderer map gives each wormhole a temporary name such as
`HOTEL` (Wanderer's `temporary_name` field), and that alias is what people say on comms, not
`J134349`. A kill posted as "J134349" makes readers look the system up on the map. A kill posted as
"J134349 (HOTEL)" tells them where in the chain it happened at a glance.

## What Changes

- The embed's **System** field shows the system's temporary name after its name, in parentheses:
  `J134349 (HOTEL)`. A system with no temporary name, or one not on the map, shows the name alone,
  as today.
- The in-memory map model from `add-linked-system-filter` (internal/topology) also records each
  system's temporary name:
  - **From the snapshot.** `GET /api/maps/{map}/systems` already returns `temporary_name` for every
    system, so each connect and periodic snapshot loads all the aliases.
  - **From the stream.** Wanderer emits `system_metadata_changed` for every system update, including
    setting, changing or clearing the temporary name, with `solar_system_id` and `temporary_name`
    in its payload. chainsniffer subscribes to it and updates the alias. A `deleted_system` event
    drops the system's alias, since Wanderer clears the temporary name when it removes a system.
  - `add_system` is still ignored. Its payload has no temporary name, and a name is always set by a
    later action that emits `system_metadata_changed`.
- **Aliases do not depend on a home system.** chainsniffer now always loads and follows the map
  model: a snapshot on every connect and every `CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL`, and the
  topology and metadata events on the stream. `CHAINSNIFFER_HOME_SYSTEM_ID` now only switches the
  linked-system filter on.
  - **BREAKING (behaviour without a home system):** the stream request always names
    `map_kill,deleted_system,connection_added,connection_removed,system_metadata_changed`. The REST
    snapshot is always fetched, so a snapshot failure on connect now delays the stream and a
    rejected token on `/systems` exits 1, as it already does with a home system.
- No new settings.

This change builds on `add-linked-system-filter` (archived as
`openspec/changes/archive/2026-10-16-add-linked-system-filter`), whose topology model and specs it
extends.

## Capabilities

### New Capabilities
- `chain-aliases`: the temporary name of each system on the map, loaded from the topology snapshot
  and kept current from `system_metadata_changed` and `deleted_system` events.

### Modified Capabilities
- `discord-killmail-posting`: the System field shows the kill system's chain alias when it has one.
- `wanderer-event-stream`: the subscription always requests the topology and metadata events.
- `relay-runtime`: the topology is loaded and followed whether or not a home system is set. The home
  system only enables the linked-system filter.

## Impact

- `internal/wanderer/snapshot.go`: the snapshot decodes `temporary_name` for each system.
- `internal/topology`: `Snapshot` carries aliases. `Graph` accepts no home system (0), stores
  aliases, applies `system_metadata_changed`, clears an alias on `deleted_system`, journals alias
  changes during a resync like connection changes, and answers `Alias(id)`. `EventTypes` gains
  `system_metadata_changed`.
- `internal/relay`: dispatches `system_metadata_changed` to the topology and looks up the alias
  for each posted kill through an interface it declares itself.
- `internal/discord/render.go`: `Render` takes the alias and appends it to the System field. Golden
  tests are added for it.
- `cmd/chainsniffer`: always builds the graph, subscribes to the topology events, runs the connect
  hook and the periodic resync. The linked-system filter is wired only with a home system.
- No new dependencies, settings, or Wanderer endpoints; the map token is unchanged.
