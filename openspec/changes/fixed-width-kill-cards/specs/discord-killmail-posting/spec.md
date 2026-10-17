# Spec Delta

## MODIFIED Requirements

### Requirement: Embed content
The embed SHALL show: a title naming the victim's ship and the victim (character name, or the
corporation name when there is no character); the system; the victim's corporation and alliance
(ticker and name); the final-blow pilot, ship, corporation and alliance; the attacker count; the
ISK value formatted in a short human-readable form; and the kill time as the embed timestamp.
Everything but the title and the timestamp SHALL be shown in one monospace block in the embed's
description, one labelled row per item in the order System, Value, Attackers, Victim, Final blow,
with every value starting in the same column.

#### Scenario: Typical kill
- **WHEN** a Stratios flown by "Pilot A" [CORP] is killed in J123456 with final blow by "Pilot B" in a Loki, 7 attackers, 412,500,000 ISK
- **THEN** the embed title names the Stratios and Pilot A, and the embed's monospace block shows J123456, [CORP], Pilot B and the Loki, 7 attackers, `412.5M ISK`, and the embed carries the kill time

#### Scenario: Missing optional fields
- **WHEN** a killmail lacks alliance or final-blow details
- **THEN** those parts are omitted or shown as unknown, and the message is still posted

#### Scenario: Aligned rows
- **WHEN** a kill with a system, a value, an attacker count, a victim corporation and a final blow is posted
- **THEN** the description is a single monospace block whose rows are labelled System, Value, Attackers, Victim and Final blow in that order, and every row's value begins in the same column

#### Scenario: Row with nothing to show
- **WHEN** a killmail has no ISK value and no attacker count
- **THEN** the block has no Value or Attackers row, and no empty row or dangling label in their place

### Requirement: Chain alias
When the kill's solar system has a chain alias at the moment the kill is posted, the embed's System
row SHALL show the alias in parentheses after the system name, as `<system> (<alias>)`. When the
system has no alias, the System row SHALL show the system name alone.

#### Scenario: Kill in a named system
- **WHEN** a kill in J134349 is posted while the map names J134349 `HOTEL`
- **THEN** the embed's System row reads `J134349 (HOTEL)`

#### Scenario: Name not resolved
- **WHEN** a kill in solar system 31001234 is posted, its name cannot be resolved, and the map names it `HOTEL`
- **THEN** the embed's System row reads `31001234 (HOTEL)`

#### Scenario: Kill in a system without a temporary name
- **WHEN** a kill is posted in a system that has no alias, or that is not on the map
- **THEN** the System row shows the system name alone and the embed is otherwise unchanged

#### Scenario: No home system configured
- **WHEN** the relay runs without `CHAINSNIFFER_HOME_SYSTEM_ID` and the map names J134349 `HOTEL`
- **THEN** a kill in J134349 still shows `J134349 (HOTEL)`

#### Scenario: Alias set after the kill
- **WHEN** a kill in J134349 is posted, and only afterwards the map names J134349 `HOTEL`
- **THEN** the already posted embed is not edited, and later kills in J134349 show `J134349 (HOTEL)`

## ADDED Requirements

### Requirement: Uniform card width
Every posted embed SHALL render at the same width in the Discord desktop client, whatever its
content. No line of the monospace block SHALL exceed a fixed column width shared by all cards; a
longer value SHALL wrap onto continuation lines that start in the value column. Every embed SHALL
carry the same width padding, invisible to the reader, whatever its classification.

#### Scenario: Short and long kills side by side
- **WHEN** a kill with short names in Thera and a kill with long corporation and alliance names in a named wormhole are posted one after the other
- **THEN** both embeds appear in the channel at the same width

#### Scenario: Long value wraps
- **WHEN** the victim's corporation name is longer than the block's column width allows
- **THEN** it continues on the next line, indented to the value column, and no line of the block exceeds the column width

#### Scenario: Neutral kill is padded too
- **WHEN** a killmail classified `neutral` is posted
- **THEN** its embed carries the same width padding as a kill or a loss, and its footer shows no classification text

### Requirement: Literal names in the block
Names, tickers and aliases SHALL appear in the monospace block as the payload and the map give
them, without markdown escapes, except that a character that would end the monospace block SHALL
be replaced so the block always closes where the relay ends it.

#### Scenario: Markdown characters in an alias
- **WHEN** a kill is posted in J134349 while the map names it `A_B_`
- **THEN** the System row reads `J134349 (A_B_)`, with no backslashes

#### Scenario: Backticks in a name
- **WHEN** a victim corporation's name contains three backticks in a row
- **THEN** the block still ends after the last row, and the rest of the embed renders unchanged
