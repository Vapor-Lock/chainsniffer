# Spec Delta

## ADDED Requirements

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
