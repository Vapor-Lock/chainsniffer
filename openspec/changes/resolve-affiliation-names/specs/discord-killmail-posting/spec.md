# Spec Delta

## ADDED Requirements

### Requirement: Affiliation names
When a kill's victim or final blow has a corporation or alliance ID whose name or ticker the payload
left out, the embed SHALL show that corporation's or alliance's name and ticker resolved from the
ID. When either has a character ID whose name the payload left out, the embed SHALL show that
character's name resolved from the ID. Only IDs the payload carries SHALL be resolved. Resolving SHALL NOT change the kill's
classification or filtering. A failed lookup SHALL leave that part out and SHALL NOT prevent the post
or delay it beyond a short timeout.

#### Scenario: Names missing from the payload
- **WHEN** a kill arrives whose final blow has corporation ID `98735003` and alliance ID `99014747` but no corporation or alliance name or ticker
- **THEN** the embed shows that corporation's and that alliance's ticker and name, as it would had the payload carried them

#### Scenario: Character name missing from the payload
- **WHEN** a kill arrives whose victim has character ID `2124740911` but no character name
- **THEN** the embed names that character wherever it names the victim, as it would had the payload carried the name

#### Scenario: No alliance in the payload
- **WHEN** the victim has corporation ID `98736396` and no alliance ID
- **THEN** the embed shows the victim's corporation and no alliance, even if the corporation belongs to an alliance now

#### Scenario: Payload already carries the names
- **WHEN** a kill's victim and final blow come with corporation and alliance names and tickers
- **THEN** the embed shows the payload's names and tickers, and no lookup is made for them

#### Scenario: Lookup unavailable
- **WHEN** a corporation's name cannot be resolved within the timeout
- **THEN** the embed leaves that corporation out, shows everything else, and the message is still posted

### Requirement: Victim row names the pilot
In the `block` card style, and in the `strip` style built on it, the Victim row SHALL show the
victim's character name on its first line, followed by the victim's corporation and alliance on
lines of their own, as the Final blow row shows the final-blow pilot above their corporation and
alliance. The victim's ship SHALL NOT be repeated in the row, since the title names it. A victim
without a character, such as a structure, SHALL show its corporation and alliance alone.

#### Scenario: Pilot above the affiliation
- **WHEN** a Stratios flown by "Pilot A" of [CORP] Victim Corporation, <VCTM> Victim Alliance is killed and posted in the `block` style
- **THEN** the Victim row reads `Pilot A`, then `[CORP] Victim Corporation`, then `<VCTM> Victim Alliance`, each line starting in the value column

#### Scenario: Structure victim
- **WHEN** an Astrahus owned by Victim Corporation, with no character, is killed and posted in the `block` style
- **THEN** the Victim row shows the corporation alone, and the title names Victim Corporation
