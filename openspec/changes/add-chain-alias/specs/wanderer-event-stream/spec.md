# Spec Delta

## MODIFIED Requirements

### Requirement: Authenticated map kill subscription
The relay SHALL open `GET {base_url}/api/maps/{map}/events/stream` for the configured map ID or slug,
send the map API token as an `Authorization: Bearer <token>` header and `Accept: text/event-stream`,
and request only the event types it handles through the `events` query parameter:
`map_kill,deleted_system,connection_added,connection_removed,system_metadata_changed`, whether or
not a home system is configured.

#### Scenario: Connect with configured map and token
- **WHEN** the relay starts with base URL `https://wanderer.example`, map `my-chain` and token `T`, and no home system
- **THEN** it requests `https://wanderer.example/api/maps/my-chain/events/stream?events=map_kill,deleted_system,connection_added,connection_removed,system_metadata_changed` with header `Authorization: Bearer T`

#### Scenario: Connect with a home system
- **WHEN** the relay starts with a home system configured
- **THEN** its stream request carries the same `events` list as without one

#### Scenario: Token never appears in the URL or logs
- **WHEN** the relay connects or logs a connection attempt
- **THEN** the token is sent only in the `Authorization` header and is not written to any log line

## ADDED Requirements

### Requirement: Metadata events advance the cursor
A `system_metadata_changed` event SHALL advance the remembered last event ID once it has been
handled, as kill and topology events do, whether or not it changed an alias.

#### Scenario: Metadata event advances the cursor
- **WHEN** a `system_metadata_changed` event with id `01JD` has been handled
- **THEN** the remembered last event ID is `01JD`
