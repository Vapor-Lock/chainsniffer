# Spec Delta

## ADDED Requirements

### Requirement: Persistent name cache
The relay SHALL keep every corporation and alliance name and ticker, and every character name, it
resolves in a JSON file `names.json` in the state directory, and SHALL NOT look up an ID found there again, including after
a restart. Entries SHALL never expire. A missing `names.json` SHALL start an empty cache; an
unreadable one SHALL be moved aside as `names.json.corrupt` with a warning and replaced by an empty
cache. Neither SHALL affect `state.json`.

#### Scenario: Names survive restart
- **WHEN** the relay resolves corporation `98735003`, is stopped and started again, and a later kill names corporation `98735003` without its name
- **THEN** the embed shows the corporation's name and ticker and no lookup is made for it

#### Scenario: Corrupt name cache
- **WHEN** `names.json` contains invalid JSON at startup
- **THEN** the relay renames it to `names.json.corrupt`, logs a warning, resumes from the event ID in `state.json`, and resolves names afresh

#### Scenario: Failed lookups are not stored
- **WHEN** ESI cannot be reached while resolving alliance `99014747`
- **THEN** nothing is written for it to `names.json`, and the next kill naming it tries again
