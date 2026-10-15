# Spec Delta

## Purpose

Maintains a durable real-time subscription to one Wanderer map's Server-Sent Events stream so that
every `map_kill` event the map emits reaches the relay, including events emitted while it was briefly
disconnected.

## ADDED Requirements

### Requirement: Authenticated map kill subscription
The relay SHALL open `GET {base_url}/api/maps/{map}/events/stream` for the configured map ID or slug,
send the map API token as an `Authorization: Bearer <token>` header and `Accept: text/event-stream`,
and request only kill events with the query parameter `events=map_kill`.

#### Scenario: Connect with configured map and token
- **WHEN** the relay starts with base URL `https://wanderer.example`, map `my-chain` and token `T`
- **THEN** it requests `https://wanderer.example/api/maps/my-chain/events/stream?events=map_kill` with header `Authorization: Bearer T`

#### Scenario: Token never appears in the URL or logs
- **WHEN** the relay connects or logs a connection attempt
- **THEN** the token is sent only in the `Authorization` header and is not written to any log line

### Requirement: SSE framing
The relay SHALL parse the stream per the SSE format: events are separated by a blank line, `id:`,
`event:` and `data:` fields are recognised, multiple `data:` lines are joined with newlines, and lines
starting with `:` are comments. The `data` of each event SHALL be decoded as a JSON envelope with
`id`, `type`, `map_id`, `timestamp` and `payload`.

#### Scenario: Kill event is decoded
- **WHEN** the stream delivers `id: 01J…`, `event: map_kill` and a `data:` line holding a JSON envelope, followed by a blank line
- **THEN** the relay hands one event of type `map_kill` with that envelope's `payload` to kill processing

#### Scenario: Keepalive comment is ignored
- **WHEN** the stream delivers `: keepalive` followed by a blank line
- **THEN** no event is produced and the connection is considered alive

#### Scenario: Connected event is not treated as a kill
- **WHEN** the stream delivers the initial `connected` event
- **THEN** the relay logs the successful subscription and produces no kill

#### Scenario: Malformed event data
- **WHEN** an event's `data` is not valid JSON or lacks a `type`
- **THEN** the relay logs a warning, skips that event and keeps the connection open

### Requirement: Event ID tracking
The relay SHALL remember the `id` of the most recent event it has finished processing and expose it to
the relay state so that it survives restarts.

#### Scenario: Processed event advances the cursor
- **WHEN** a `map_kill` event with id `01JB` has been fully processed (posted, filtered out, or skipped as malformed)
- **THEN** the remembered last event ID is `01JB`

#### Scenario: Unprocessed event does not advance the cursor
- **WHEN** the process stops while an event is still being processed
- **THEN** the remembered last event ID is still that of the previous event

### Requirement: Resume with backfill
Whenever a last event ID is known, the relay SHALL include it as the `last_event_id` query parameter
when (re)connecting so Wanderer replays the events it missed.

#### Scenario: Reconnect after a drop
- **WHEN** the connection drops after the relay processed event `01JB` and the relay reconnects
- **THEN** the reconnect request includes `last_event_id=01JB`

#### Scenario: Restart with persisted cursor
- **WHEN** the relay starts and the state file holds last event ID `01JB`
- **THEN** the first connection request includes `last_event_id=01JB`

#### Scenario: First start without state
- **WHEN** the relay starts with no persisted last event ID
- **THEN** it connects without `last_event_id` and processes live events only

### Requirement: Reconnect with backoff
On a network error, end of stream, HTTP 429 or HTTP 5xx, the relay SHALL reconnect with exponential
backoff starting at 1 second, doubling up to a 60 second cap, with random jitter. The backoff SHALL
reset once a connection delivers its first event or keepalive.

#### Scenario: Server error triggers backoff
- **WHEN** three consecutive connection attempts return HTTP 503
- **THEN** the relay waits roughly 1s, 2s and 4s (plus jitter) before the successive retries

#### Scenario: Backoff is capped
- **WHEN** connection attempts keep failing
- **THEN** the wait between attempts never exceeds 60 seconds plus jitter

#### Scenario: Backoff resets after a healthy connection
- **WHEN** a connection succeeds and delivers a keepalive, and later drops
- **THEN** the next reconnect waits roughly 1 second again

### Requirement: Stale connection detection
The relay SHALL treat a connection that delivers no bytes (events or keepalive comments) for 90 seconds
as dead, close it, and reconnect using the backoff rules.

#### Scenario: Silent connection is replaced
- **WHEN** an open connection produces no data for 90 seconds
- **THEN** the relay closes it and reconnects with `last_event_id` set to its last processed event

### Requirement: Fatal subscription errors
The relay SHALL treat HTTP 401, 402, 403 and 404 responses from the stream endpoint, and a 503 that
reports SSE as disabled on the server, as configuration errors: it SHALL log the status and the
server's error message and exit with a non-zero status instead of retrying.

#### Scenario: Invalid token
- **WHEN** the stream endpoint responds 401
- **THEN** the relay logs that the Wanderer token was rejected for the map and exits non-zero

#### Scenario: Unknown map
- **WHEN** the stream endpoint responds 404
- **THEN** the relay logs that the map was not found and exits non-zero

#### Scenario: SSE disabled for the map
- **WHEN** the stream endpoint responds 403 with code `SSE_DISABLED_FOR_MAP`
- **THEN** the relay logs that SSE must be enabled in the map's settings and exits non-zero

#### Scenario: SSE disabled on the server
- **WHEN** the stream endpoint responds 503 with the plain-text body `Server-Sent Events are disabled on this server` or a JSON body with code `SSE_GLOBALLY_DISABLED`
- **THEN** the relay logs that this Wanderer instance does not offer SSE and exits non-zero

#### Scenario: Ordinary server error
- **WHEN** the stream endpoint responds 503 with any other body
- **THEN** the relay treats it as transient and reconnects with backoff

#### Scenario: Connection limit reached
- **WHEN** the stream endpoint responds 429 with code `MAP_CONNECTION_LIMIT` or `API_KEY_CONNECTION_LIMIT`
- **THEN** the relay logs the limit and reconnects with backoff
