# Spec Delta

## ADDED Requirements

### Requirement: Chain alias
When the kill's solar system has a chain alias at the moment the kill is posted, the embed's System
field SHALL show the alias in parentheses after the system name, as `<system> (<alias>)`. When the
system has no alias, the System field SHALL show the system name alone.

#### Scenario: Kill in a named system
- **WHEN** a kill in J134349 is posted while the map names J134349 `HOTEL`
- **THEN** the embed's System field reads `J134349 (HOTEL)`

#### Scenario: Name not resolved
- **WHEN** a kill in solar system 31001234 is posted, its name cannot be resolved, and the map names it `HOTEL`
- **THEN** the embed's System field reads `31001234 (HOTEL)`

#### Scenario: Kill in a system without a temporary name
- **WHEN** a kill is posted in a system that has no alias, or that is not on the map
- **THEN** the System field shows the system name alone and the embed is otherwise unchanged

#### Scenario: No home system configured
- **WHEN** the relay runs without `CHAINSNIFFER_HOME_SYSTEM_ID` and the map names J134349 `HOTEL`
- **THEN** a kill in J134349 still shows `J134349 (HOTEL)`

#### Scenario: Alias set after the kill
- **WHEN** a kill in J134349 is posted, and only afterwards the map names J134349 `HOTEL`
- **THEN** the already posted embed is not edited, and later kills in J134349 show `J134349 (HOTEL)`
