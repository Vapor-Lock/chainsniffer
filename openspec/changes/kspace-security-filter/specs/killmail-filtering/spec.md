# Spec Delta

## ADDED Requirements

### Requirement: K-space security filter
The relay SHALL drop a killmail in a known-space system whose security class is not one of the
configured classes, reporting the drop reason `kspace_security`. Killmails in wormhole systems, and
killmails with no known system, SHALL pass this filter whatever the configuration.

#### Scenario: Class not configured
- **WHEN** the configured classes are `low,null` and a killmail is in a high-sec system
- **THEN** it is not posted and the drop reason is `kspace_security`

#### Scenario: Class configured
- **WHEN** the configured classes are `low,null` and a killmail is in a low-sec system
- **THEN** it continues through the remaining filters

#### Scenario: No classes configured
- **WHEN** no class is configured and a killmail is in a null-sec system
- **THEN** it is not posted and the drop reason is `kspace_security`

#### Scenario: Wormhole kill
- **WHEN** no class is configured and a killmail is in a wormhole system, or in Thera
- **THEN** it passes the k-space security filter

### Requirement: Security classes
A known-space system SHALL be high-sec when its security status is 0.45 or above, low-sec when it is
above 0.0 and below 0.45, and null-sec when it is 0.0 or below.

#### Scenario: Rounded into high-sec
- **WHEN** a system's security status is 0.46, which the game shows as 0.5
- **THEN** it is high-sec

#### Scenario: Low edge of low-sec
- **WHEN** a system's security status is 0.02, which the game shows as 0.1
- **THEN** it is low-sec

#### Scenario: Pochven
- **WHEN** a system's security status is -1.0, as in Pochven and Zarzakh
- **THEN** it is null-sec

### Requirement: Unresolved security
When a known-space system's security status cannot be resolved within the lookup timeout, the
k-space security filter SHALL pass the killmail and the relay SHALL log a warning naming the
system. The status SHALL be looked up only when the configured classes are some but not all of the
three.

#### Scenario: Security lookup fails
- **WHEN** the configured classes are `null` and a killmail's known-space system cannot be resolved
- **THEN** it passes the k-space security filter and a warning names the system

#### Scenario: No lookup needed
- **WHEN** no class, or all three, are configured
- **THEN** no security status is looked up for any killmail
