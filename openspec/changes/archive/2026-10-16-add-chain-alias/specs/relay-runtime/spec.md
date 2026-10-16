# Spec Delta

## MODIFIED Requirements

### Requirement: Topology configuration
The relay SHALL read two optional settings: `CHAINSNIFFER_HOME_SYSTEM_ID`, the EVE solar system ID
of the home system (unset by default), and `CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL`, a Go duration
(default `15m`). The relay SHALL load and follow the map's topology whether or not the home system
is set. The home system SHALL only enable the linked-system filter.

#### Scenario: Home system set
- **WHEN** `CHAINSNIFFER_HOME_SYSTEM_ID` is `31000727`
- **THEN** the relay filters kills to systems linked to solar system 31000727

#### Scenario: Home system unset
- **WHEN** `CHAINSNIFFER_HOME_SYSTEM_ID` is not set
- **THEN** the relay still loads topology snapshots on connect and at the resync interval and follows the topology events, but does not filter by linked system

#### Scenario: Invalid home system
- **WHEN** `CHAINSNIFFER_HOME_SYSTEM_ID` is `J142845` or `0`
- **THEN** the relay exits with status 2 and names `CHAINSNIFFER_HOME_SYSTEM_ID`

#### Scenario: Invalid resync interval
- **WHEN** `CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL` is `soon`, `0` or negative
- **THEN** the relay exits with status 2 and names `CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL`

#### Scenario: Startup summary
- **WHEN** the relay starts
- **THEN** the startup configuration log line includes the resync interval, and the home system ID when one is configured
