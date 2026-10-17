# Spec Delta

## MODIFIED Requirements

### Requirement: Filter configuration
The relay SHALL read its filter settings from optional environment variables:
`CHAINSNIFFER_FRIENDLY_CORP_IDS` and `CHAINSNIFFER_FRIENDLY_ALLIANCE_IDS` (comma-separated, default
empty), `CHAINSNIFFER_FRIENDLY_ONLY` (default `false`), `CHAINSNIFFER_EXCLUDE_NPC` (default `true`),
`CHAINSNIFFER_MIN_VALUE` in ISK (default `0`), `CHAINSNIFFER_MAX_KILL_AGE` as a Go duration
(default `1h`) and `CHAINSNIFFER_KSPACE` (default `none`).

#### Scenario: Filter defaults
- **WHEN** none of the filter variables are set
- **THEN** NPC kills are excluded, there is no minimum value, kills older than one hour are dropped, no kill in known space is posted, and every other non-NPC kill is posted

#### Scenario: Friendly IDs with spaces
- **WHEN** `CHAINSNIFFER_FRIENDLY_CORP_IDS` is `98000001, 98000002`
- **THEN** both corporation IDs are treated as friendly

## ADDED Requirements

### Requirement: K-space configuration
The relay SHALL read `CHAINSNIFFER_KSPACE` as `none`, or as a comma-separated combination of `highsec`,
`lowsec` and `nullsec`, case-insensitive and ignoring spaces, naming the security classes whose kills are
relayed. Unset or empty SHALL mean `none`. Any other word, or `none` together with a class, SHALL be
a configuration error that exits with status 2 and names the variable. The startup summary SHALL
show the classes.

#### Scenario: Two classes
- **WHEN** `CHAINSNIFFER_KSPACE` is `Lowsec, nullsec`
- **THEN** kills in low-sec and null-sec systems are relayed and kills in high-sec systems are not

#### Scenario: Unset
- **WHEN** `CHAINSNIFFER_KSPACE` is unset
- **THEN** no kill in known space is relayed, and the startup summary shows `none`

#### Scenario: Unknown class
- **WHEN** `CHAINSNIFFER_KSPACE` is `lowsec,wormhole`
- **THEN** the relay exits with status 2 and names `CHAINSNIFFER_KSPACE`, before connecting anywhere

#### Scenario: None with a class
- **WHEN** `CHAINSNIFFER_KSPACE` is `none,highsec`
- **THEN** the relay exits with status 2 and names `CHAINSNIFFER_KSPACE`
