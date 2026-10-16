# chain-aliases Specification

## Purpose
Keeps the temporary name (chain alias) that the Wanderer map gives each system, such as `HOTEL`
for J134349, so posted kills can name the system the way the chain does.

## Requirements

### Requirement: Aliases tracked for every map
The relay SHALL track each map system's temporary name, its chain alias, whether or not a home
system is configured.

#### Scenario: Home system set
- **WHEN** `CHAINSNIFFER_HOME_SYSTEM_ID` is set and the map names J134349 `HOTEL`
- **THEN** the relay knows J134349's alias is `HOTEL`

#### Scenario: Home system unset
- **WHEN** `CHAINSNIFFER_HOME_SYSTEM_ID` is not set and the map names J134349 `HOTEL`
- **THEN** the relay still knows J134349's alias is `HOTEL`

### Requirement: Aliases from the snapshot
Each topology snapshot SHALL replace every alias with the `temporary_name` of each system the
snapshot lists. A system whose `temporary_name` is null, missing, or blank after trimming whitespace
SHALL have no alias.

#### Scenario: Snapshot names a system
- **WHEN** a snapshot lists solar system 31001234 with `temporary_name` `HOTEL`
- **THEN** after the snapshot the alias of 31001234 is `HOTEL`

#### Scenario: Snapshot clears a stale alias
- **WHEN** the relay holds alias `HOTEL` for 31001234 and a new snapshot lists 31001234 with a null `temporary_name`
- **THEN** after the snapshot 31001234 has no alias

#### Scenario: Blank temporary name
- **WHEN** a snapshot lists a system whose `temporary_name` is `"  "`
- **THEN** that system has no alias

### Requirement: Alias changes from the stream
On a `system_metadata_changed` event the relay SHALL set the alias of the event's `solar_system_id`
to its `temporary_name`, or remove the alias when that is null, missing or blank. On a
`deleted_system` event it SHALL remove the alias of the event's `solar_system_id`.

#### Scenario: Temporary name set after the system is linked
- **WHEN** J134349 has no alias and a `system_metadata_changed` event for it carries `temporary_name` `HOTEL`
- **THEN** J134349's alias is `HOTEL`

#### Scenario: Temporary name renamed
- **WHEN** J134349's alias is `HOTEL` and a `system_metadata_changed` event for it carries `temporary_name` `INDIA`
- **THEN** J134349's alias is `INDIA`

#### Scenario: Temporary name cleared
- **WHEN** J134349's alias is `HOTEL` and a `system_metadata_changed` event for it carries a null `temporary_name`
- **THEN** J134349 has no alias

#### Scenario: Other metadata changes
- **WHEN** a `system_metadata_changed` event for J134349 changes only its status or labels and still carries `temporary_name` `HOTEL`
- **THEN** J134349's alias stays `HOTEL`

#### Scenario: System removed from the map
- **WHEN** J134349's alias is `HOTEL` and a `deleted_system` event removes J134349
- **THEN** J134349 has no alias

#### Scenario: Malformed metadata event
- **WHEN** a `system_metadata_changed` event's payload lacks an integer `solar_system_id`
- **THEN** the relay logs a warning, leaves every alias unchanged and advances past the event

### Requirement: Alias changes during a snapshot are not lost
An alias change handled while a snapshot is in flight SHALL still apply once the snapshot is
installed. Installing a snapshot SHALL never undo an alias change the relay has already handled.

#### Scenario: Rename while a periodic snapshot is loading
- **WHEN** a periodic snapshot is requested, a `system_metadata_changed` event renames J134349 to `INDIA` before the snapshot response arrives, and the snapshot still says `HOTEL`
- **THEN** after the snapshot is installed J134349's alias is `INDIA`

### Requirement: Alias logging
The info line logged after every snapshot SHALL also report how many systems have an alias. Each
alias change from the stream SHALL be logged at debug level with the system ID and the new alias.

#### Scenario: Snapshot summary
- **WHEN** a snapshot with 22 systems loads and 9 of them have a temporary name
- **THEN** the snapshot's info log line reports 9 aliases
