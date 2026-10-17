# Spec Delta

## MODIFIED Requirements

### Requirement: Embed content
The embed SHALL show: a title naming the victim's ship and the victim (character name, or the
corporation name when there is no character); the system; the victim's corporation and alliance
(ticker and name); the final-blow pilot, ship, corporation and alliance; the attacker count; the
ISK value formatted in a short human-readable form; and the kill time as the embed timestamp.

#### Scenario: Typical kill
- **WHEN** a Stratios flown by "Pilot A" [CORP] is killed in J123456 with final blow by "Pilot B" in a Loki, 7 attackers, 412,500,000 ISK
- **THEN** in either card style the embed title names the Stratios and Pilot A, the embed shows J123456, [CORP], Pilot B and the Loki, 7 attackers and `412.5M ISK`, and the embed carries the kill time

#### Scenario: Missing optional fields
- **WHEN** a killmail lacks alliance or final-blow details
- **THEN** those parts are omitted or shown as unknown, and the message is still posted

### Requirement: Chain alias
When the kill's solar system has a chain alias at the moment the kill is posted, the embed's System
row (in the `block` style) or System field (in the `fields` style) SHALL show the alias in
parentheses after the system name, as `<system> (<alias>)`. When the system has no alias, it SHALL
show the system name alone.

#### Scenario: Kill in a named system
- **WHEN** a kill in J134349 is posted while the map names J134349 `HOTEL`
- **THEN** the embed's System row or field reads `J134349 (HOTEL)`

#### Scenario: Name not resolved
- **WHEN** a kill in solar system 31001234 is posted, its name cannot be resolved, and the map names it `HOTEL`
- **THEN** the embed's System row or field reads `31001234 (HOTEL)`

#### Scenario: Kill in a system without a temporary name
- **WHEN** a kill is posted in a system that has no alias, or that is not on the map
- **THEN** the System row or field shows the system name alone and the embed is otherwise unchanged

#### Scenario: No home system configured
- **WHEN** the relay runs without `CHAINSNIFFER_HOME_SYSTEM_ID` and the map names J134349 `HOTEL`
- **THEN** a kill in J134349 still shows `J134349 (HOTEL)`

#### Scenario: Alias set after the kill
- **WHEN** a kill in J134349 is posted, and only afterwards the map names J134349 `HOTEL`
- **THEN** the already posted embed is not edited, and later kills in J134349 show `J134349 (HOTEL)`

### Requirement: Monospace card body
In the `block` card style, everything in the embed but the title and the timestamp SHALL be shown
in one monospace block in the embed's description, one labelled row per item in the order System,
Value, Attackers, Victim, Final blow, with every value starting in the same column.

#### Scenario: Aligned rows
- **WHEN** a kill with a system, a value, an attacker count, a victim corporation and a final blow is posted in the `block` style
- **THEN** the description is a single monospace block whose rows are labelled System, Value, Attackers, Victim and Final blow in that order, and every row's value begins in the same column

#### Scenario: Row with nothing to show
- **WHEN** a killmail with no ISK value and no attacker count is posted in the `block` style
- **THEN** the block has no Value or Attackers row, and no empty row or dangling label in their place

### Requirement: Uniform card width
In the `block` card style, every posted embed SHALL render at the same width in the Discord desktop
client, whatever its content. No line of the monospace block SHALL exceed a fixed column width
shared by all cards; a longer value SHALL wrap onto continuation lines that start in the value
column. Every embed SHALL carry width padding, invisible to the reader, that depends on whether it
shows a ship image and never on its classification.

#### Scenario: Short and long kills side by side
- **WHEN** a kill with short names in Thera and a kill with long corporation and alliance names in a named wormhole are posted one after the other in the `block` style
- **THEN** both embeds appear in the channel at the same width

#### Scenario: Long value wraps
- **WHEN** the victim's corporation name is longer than the block's column width allows
- **THEN** it continues on the next line, indented to the value column, and no line of the block exceeds the column width

#### Scenario: Neutral kill is padded too
- **WHEN** a killmail classified `neutral` is posted in the `block` style
- **THEN** its embed carries the same width padding as a kill or a loss, and its footer shows no classification text

### Requirement: Literal names in the block
In the `block` card style, names, tickers and aliases SHALL appear in the monospace block as the
payload and the map give them, without markdown escapes, except that a character that would end the
monospace block SHALL be replaced so the block always closes where the relay ends it.

#### Scenario: Markdown characters in an alias
- **WHEN** a kill is posted in the `block` style in J134349 while the map names it `A_B_`
- **THEN** the System row reads `J134349 (A_B_)`, with no backslashes

#### Scenario: Backticks in a name
- **WHEN** a victim corporation's name contains three backticks in a row and the kill is posted in the `block` style
- **THEN** the block still ends after the last row, and the rest of the embed renders unchanged

## ADDED Requirements

### Requirement: Fields card style
In the `fields` card style, the embed SHALL have no description and SHALL show System, Value,
Attackers, Victim and Final blow as inline fields, in that order, each left out when it has nothing
to show. Victim SHALL read `[TICKER] Corporation / <TICKER> Alliance`. Final blow SHALL read
`Pilot (Ship)` with that affiliation on a second line. Markdown characters in the alias SHALL be
backslash-escaped.

#### Scenario: Typical kill in the fields style
- **WHEN** the typical Stratios kill is posted in the `fields` style
- **THEN** the embed has inline fields System, Value `412.5M ISK`, Attackers `7`, Victim `[CORP] Victim Corporation / <VCTM> Victim Alliance` and Final blow `Pilot B (Loki)` over `[FRND] Friendly Corporation / <BLUE> Friendly Alliance`, and no description

#### Scenario: Markdown characters in an alias, fields style
- **WHEN** a kill is posted in the `fields` style in J134349 while the map names it `A_B_`
- **THEN** the System field reads `J134349 (A\_B\_)`, so Discord shows `A_B_` without italics
