# Spec Delta

## MODIFIED Requirements

### Requirement: Authenticated map kill subscription
The relay SHALL open `GET {base_url}/api/maps/{map}/events/stream` for the configured map ID or slug,
send the map API token as an `Authorization: Bearer <token>` header and `Accept: text/event-stream`,
and request only the event types it handles through the `events` query parameter: `map_kill` alone
without a home system, and `map_kill,deleted_system,connection_added,connection_removed` with one.

#### Scenario: Connect with configured map and token
- **WHEN** the relay starts with base URL `https://wanderer.example`, map `my-chain` and token `T`, and no home system
- **THEN** it requests `https://wanderer.example/api/maps/my-chain/events/stream?events=map_kill` with header `Authorization: Bearer T`

#### Scenario: Connect with a home system
- **WHEN** the relay starts with a home system configured
- **THEN** its stream request carries `events=map_kill,deleted_system,connection_added,connection_removed`

#### Scenario: Token never appears in the URL or logs
- **WHEN** the relay connects or logs a connection attempt
- **THEN** the token is sent only in the `Authorization` header and is not written to any log line

### Requirement: Event ID tracking
The relay SHALL remember the `id` of the most recent event it has finished processing and expose it to
the relay state so that it survives restarts. Topology events advance it the same way kill events do.

#### Scenario: Processed event advances the cursor
- **WHEN** a `map_kill` event with id `01JB` has been fully processed (posted, filtered out, or skipped as malformed)
- **THEN** the remembered last event ID is `01JB`

#### Scenario: Topology event advances the cursor
- **WHEN** a `connection_removed` event with id `01JC` has been applied to the topology
- **THEN** the remembered last event ID is `01JC`

#### Scenario: Unprocessed event does not advance the cursor
- **WHEN** the process stops while an event is still being processed
- **THEN** the remembered last event ID is still that of the previous event
