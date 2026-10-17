# Spec Delta

## ADDED Requirements

### Requirement: Affiliation names
When a kill's victim or final blow has a corporation or alliance ID whose name or ticker the payload
left out, the embed SHALL show that corporation's or alliance's name and ticker resolved from the
ID. Only IDs the payload carries SHALL be resolved. Resolving SHALL NOT change the kill's
classification or filtering. A failed lookup SHALL leave that part out and SHALL NOT prevent the post
or delay it beyond a short timeout.

#### Scenario: Names missing from the payload
- **WHEN** a kill arrives whose final blow has corporation ID `98735003` and alliance ID `99014747` but no corporation or alliance name or ticker
- **THEN** the embed shows that corporation's and that alliance's ticker and name, as it would had the payload carried them

#### Scenario: No alliance in the payload
- **WHEN** the victim has corporation ID `98736396` and no alliance ID
- **THEN** the embed shows the victim's corporation and no alliance, even if the corporation belongs to an alliance now

#### Scenario: Payload already carries the names
- **WHEN** a kill's victim and final blow come with corporation and alliance names and tickers
- **THEN** the embed shows the payload's names and tickers, and no lookup is made for them

#### Scenario: Lookup unavailable
- **WHEN** a corporation's name cannot be resolved within the timeout
- **THEN** the embed leaves that corporation out, shows everything else, and the message is still posted
