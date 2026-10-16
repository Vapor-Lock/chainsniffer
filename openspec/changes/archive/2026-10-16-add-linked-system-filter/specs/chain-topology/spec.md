# Spec Delta

## Purpose

Keeps an up-to-date picture of which solar systems on the Wanderer map are linked to the configured
home system, so kills can be filtered to the chain that is actually connected to home.

## ADDED Requirements

### Requirement: Linked systems
With a home system configured, the relay SHALL treat a solar system as linked when the map's
connections form a path from the home system to it. Connections count in both directions and
whatever their type (wormhole or stargate). The home system SHALL always count as linked, even when
it has no connections or is missing from the map.

#### Scenario: Two jumps out from home
- **WHEN** the map has connections home–A and A–B
- **THEN** home, A and B are linked

#### Scenario: Connection recorded in the other direction
- **WHEN** the map has a connection whose source is A and whose target is the home system
- **THEN** A is linked

#### Scenario: Island not connected to home
- **WHEN** the map has a connection C–D and neither C nor D has a path to home
- **THEN** C and D are not linked

#### Scenario: Home without connections
- **WHEN** the home system has no connections
- **THEN** only the home system is linked

### Requirement: Topology snapshot
The relay SHALL load the map's systems and connections from
`GET {base_url}/api/maps/{map}/systems`, which returns both. It SHALL authenticate with the map API
token as an `Authorization: Bearer <token>` header, and SHALL replace its whole topology with the
snapshot's.

#### Scenario: Snapshot request
- **WHEN** the relay loads a snapshot for base URL `https://wanderer.example`, map `my-chain` and token `T`
- **THEN** it requests `https://wanderer.example/api/maps/my-chain/systems` with header `Authorization: Bearer T`

#### Scenario: Snapshot replaces stale topology
- **WHEN** the relay's topology has a connection A–B and a new snapshot does not contain it
- **THEN** after the snapshot the connection A–B is gone and the linked systems are recomputed

### Requirement: Snapshot on every connection
The relay SHALL load a topology snapshot each time the event stream (re)connects. It SHALL do so
before handling any event that connection delivers.

#### Scenario: First connection
- **WHEN** the relay starts and the stream connects
- **THEN** a snapshot is loaded before the first kill on that connection is filtered

#### Scenario: Reconnect after a drop
- **WHEN** the stream drops and reconnects
- **THEN** a fresh snapshot is loaded before the next event is handled

### Requirement: Failed snapshot on connect
A snapshot that fails on connect with a network error, HTTP 429 or HTTP 5xx SHALL be treated as a
failed connection. The relay SHALL close the stream and reconnect using the stream's backoff rules,
without handling any event from that connection.

#### Scenario: Wanderer API briefly unavailable
- **WHEN** the stream connects but the snapshot request returns HTTP 502
- **THEN** the relay closes the stream, waits per the backoff rules and reconnects, and no kill from that connection is filtered or posted

### Requirement: Fatal snapshot errors
The relay SHALL treat HTTP 401, 403 or 404 from the snapshot endpoint as a configuration error. It
SHALL log the status and the server's message and exit with status 1 instead of retrying.

#### Scenario: Token rejected by the REST API
- **WHEN** the snapshot request responds 401
- **THEN** the relay logs that the Wanderer token was rejected and exits 1

### Requirement: Topology events
The relay SHALL update its topology from stream events. `connection_added` adds the connection
between its `solar_system_source_id` and `solar_system_target_id`. `connection_removed` removes the
connection between them. `deleted_system` removes the `solar_system_id` system and every connection
touching it.

#### Scenario: New connection links an island
- **WHEN** C–D is an unlinked island and a `connection_added` event connects A, a linked system, to C
- **THEN** C and D become linked

#### Scenario: Removed connection unlinks everything behind it
- **WHEN** the map is home–A–B–C and a `connection_removed` event removes A–B
- **THEN** B and C are no longer linked, and home and A still are

#### Scenario: Deleted system takes its connections with it
- **WHEN** the map is home–A–B and a `deleted_system` event removes A, with no `connection_removed` events for A's connections
- **THEN** A and B are no longer linked

#### Scenario: Alternative path keeps a system linked
- **WHEN** the map has home–A, home–B, A–C and B–C, and a `connection_removed` event removes A–C
- **THEN** C is still linked

#### Scenario: Malformed topology event
- **WHEN** a topology event's payload lacks an integer system ID it needs
- **THEN** the relay logs a warning, leaves the topology unchanged and advances past the event

### Requirement: Periodic resynchronisation
While the stream stays connected, the relay SHALL also reload the topology snapshot at the
configured resync interval. A periodic snapshot that fails SHALL be logged as a warning and the
current topology kept until the next attempt.

#### Scenario: Missed event is corrected
- **WHEN** a `connection_removed` event never reached the relay and the next periodic snapshot no longer contains that connection
- **THEN** after that snapshot the connection is gone and the linked systems are recomputed

#### Scenario: Periodic snapshot fails
- **WHEN** a periodic snapshot request fails with HTTP 503
- **THEN** the relay logs a warning, keeps filtering against its current topology and keeps the stream open

### Requirement: Events during a snapshot are not lost
A topology event handled while a snapshot is in flight SHALL still apply once the snapshot is
installed. Installing a snapshot SHALL never undo an event the relay has already handled.

#### Scenario: Connection removed while a periodic snapshot is loading
- **WHEN** a periodic snapshot is requested, a `connection_removed` event for A–B is handled before the snapshot response arrives, and the snapshot still contains A–B
- **THEN** after the snapshot is installed A–B is absent

### Requirement: Home system not on the map
When a snapshot does not contain the home system, the relay SHALL log a warning naming the home
system ID and keep running.

#### Scenario: Home missing from the snapshot
- **WHEN** a snapshot's systems do not include the configured home system
- **THEN** the relay logs a warning, and only kills in the home system itself pass the linked-system filter

### Requirement: Topology logging
After every snapshot the relay SHALL log at info level the number of systems on the map and the
number of linked systems. Each topology event that changes which systems are linked SHALL be logged
at debug level with the systems that became linked or unlinked.

#### Scenario: Snapshot summary
- **WHEN** a snapshot with 22 systems loads and 16 of them are linked to home
- **THEN** an info log line reports 22 systems and 16 linked systems
