# killmail-filtering Specification

## Purpose

Turns Wanderer `map_kill` events into individual killmails, classifies each one relative to the
operator's friendly corporations and alliances, and decides which ones are worth posting.

## Requirements

### Requirement: Killmail extraction
The relay SHALL extract killmails only from `map_kill` payloads whose `type` is `killmail_update`,
taking each entry of the payload's `killmails` list as one killmail. Payloads of type `kill_count`
and any other type SHALL be ignored.

#### Scenario: Update with two killmails
- **WHEN** a `map_kill` event arrives whose payload has `type` `killmail_update` and two entries in `killmails`
- **THEN** two killmails are passed on for classification and filtering

#### Scenario: Kill count payload
- **WHEN** a `map_kill` event arrives whose payload has `type` `kill_count`
- **THEN** no killmail is produced

#### Scenario: Entry without killmail ID
- **WHEN** an entry in `killmails` has no integer `killmail_id`
- **THEN** that entry is skipped with a warning and the other entries are still processed

### Requirement: Killmail fields
For each killmail the relay SHALL read `killmail_id`, `kill_time`, `solar_system_id`, `victim_char_id`,
`victim_char_name`, `victim_corp_id`, `victim_corp_ticker`, `victim_corp_name`, `victim_alliance_id`,
`victim_alliance_ticker`, `victim_alliance_name`, `victim_ship_type_id`, `victim_ship_name`, the
corresponding `final_blow_*` fields, `attacker_count`, `total_value` and `npc`. Any field other than
`killmail_id` MAY be absent or null.

#### Scenario: Structure kill without victim character
- **WHEN** a killmail has no `victim_char_id` or `victim_char_name` (for example a structure)
- **THEN** it is still classified, filtered and, if it passes, posted

### Requirement: Kill classification
Each killmail SHALL be classified as `loss` when the victim's corporation or alliance ID is in the
friendly lists, otherwise as `kill` when the final-blow corporation or alliance ID is in the friendly
lists, otherwise as `neutral`. With no friendly IDs configured, every killmail is `neutral`.

#### Scenario: Friendly victim
- **WHEN** friendly alliance IDs include `99000001` and a killmail's `victim_alliance_id` is `99000001`
- **THEN** it is classified as `loss`

#### Scenario: Friendly final blow
- **WHEN** friendly corporation IDs include `98000001`, the victim is not friendly and `final_blow_corp_id` is `98000001`
- **THEN** it is classified as `kill`

#### Scenario: Blue-on-blue
- **WHEN** both the victim and the final blow are friendly
- **THEN** it is classified as `loss`

#### Scenario: Unrelated pilots
- **WHEN** neither the victim nor the final blow is friendly
- **THEN** it is classified as `neutral`

### Requirement: NPC filter
When NPC exclusion is enabled (the default), the relay SHALL drop killmails whose `npc` field is true.

#### Scenario: Rat kill with default settings
- **WHEN** a killmail has `npc: true` and NPC exclusion is not configured
- **THEN** it is not posted

#### Scenario: NPC kills explicitly allowed
- **WHEN** NPC exclusion is disabled and a killmail has `npc: true`
- **THEN** it continues through the remaining filters

### Requirement: Minimum value filter
The relay SHALL drop killmails whose `total_value` is below the configured minimum ISK value. The
default minimum is 0; a missing `total_value` counts as 0.

#### Scenario: Cheap kill below threshold
- **WHEN** the minimum value is 10,000,000 and a killmail's `total_value` is 2,500,000
- **THEN** it is not posted

#### Scenario: Kill at threshold
- **WHEN** the minimum value is 10,000,000 and a killmail's `total_value` is 10,000,000
- **THEN** it continues through the remaining filters

### Requirement: Friendly-only filter
When friendly-only mode is enabled, the relay SHALL drop killmails classified as `neutral`. It is
disabled by default. Enabling it with no friendly IDs configured SHALL be rejected at startup.

#### Scenario: Neutral kill in friendly-only mode
- **WHEN** friendly-only mode is enabled and a killmail is classified `neutral`
- **THEN** it is not posted

#### Scenario: Loss in friendly-only mode
- **WHEN** friendly-only mode is enabled and a killmail is classified `loss`
- **THEN** it continues through the remaining filters

#### Scenario: Misconfiguration
- **WHEN** friendly-only mode is enabled and no friendly corporation or alliance IDs are configured
- **THEN** the relay refuses to start and reports the configuration error

### Requirement: Maximum kill age filter
The relay SHALL drop killmails whose `kill_time` is older than the configured maximum age relative
to the current time (default 1 hour), so that historical kills Wanderer sends when a system is
added to the map are not posted. A killmail with no or unparseable `kill_time` SHALL be kept.

#### Scenario: Historical kill for a newly mapped system
- **WHEN** a killmail's `kill_time` is 5 hours ago and the maximum age is 1 hour
- **THEN** it is not posted

#### Scenario: Fresh kill
- **WHEN** a killmail's `kill_time` is 2 minutes ago
- **THEN** it continues through the remaining filters

### Requirement: Duplicate suppression
The relay SHALL post each `killmail_id` at most once, including across reconnect backfills and process
restarts, using the posted-killmail record kept in the relay state.

#### Scenario: Same kill in two updates
- **WHEN** killmail `123` was already posted and a later `killmail_update` contains `123` again
- **THEN** it is not posted a second time

#### Scenario: Backfill after restart
- **WHEN** the relay restarts, Wanderer backfills an event containing killmail `123`, and `123` is in the persisted posted record
- **THEN** it is not posted again

### Requirement: Filter decision logging
For every killmail that is not posted, the relay SHALL log at debug level the `killmail_id` and the
filter that dropped it.

#### Scenario: Dropped by value
- **WHEN** a killmail is dropped by the minimum value filter
- **THEN** a debug log line names its `killmail_id` and the minimum value filter

### Requirement: Linked-system filter
With a home system configured, the relay SHALL drop a killmail whose solar system is not linked to
the home system, reporting the drop reason `unlinked_system`. The system is the killmail's
`solar_system_id`, else the payload's top-level `solar_system_id`; a killmail with neither is
dropped. With no home system configured, the filter SHALL pass every killmail.

#### Scenario: Kill in a linked system
- **WHEN** a home system is configured and a killmail's solar system is linked to it
- **THEN** it continues through the remaining filters

#### Scenario: Kill in an unlinked island
- **WHEN** a home system is configured and a killmail's solar system is on the map but has no path to home
- **THEN** it is not posted and the drop reason is `unlinked_system`

#### Scenario: Kill in the home system
- **WHEN** a killmail's solar system is the home system
- **THEN** it passes the linked-system filter

#### Scenario: System taken from the payload
- **WHEN** a killmail entry has no `solar_system_id` and its payload's top-level `solar_system_id` is a linked system
- **THEN** it passes the linked-system filter

#### Scenario: No system known
- **WHEN** a home system is configured and neither the killmail entry nor its payload has a `solar_system_id`
- **THEN** it is not posted and the drop reason is `unlinked_system`

#### Scenario: No home system
- **WHEN** no home system is configured
- **THEN** every killmail passes the linked-system filter, whatever its solar system

#### Scenario: Linked at processing time
- **WHEN** a replayed killmail's system was linked when the kill happened but is no longer linked when the relay processes it
- **THEN** it is not posted, because the filter reads the topology as it stands when the killmail is processed
