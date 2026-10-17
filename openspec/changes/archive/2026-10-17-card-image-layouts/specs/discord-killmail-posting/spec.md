# Spec Delta

## MODIFIED Requirements

### Requirement: Fatal Discord errors
If Discord rejects the bot token (HTTP 401), the relay SHALL log the error and exit with a non-zero
status. If Discord rejects a post for missing access or permissions (HTTP 403) or an unknown channel
(HTTP 404), the relay SHALL log an error explaining the required permissions and exit non-zero. In
the `strip` card style the required permissions SHALL include Attach Files.

#### Scenario: Bot not allowed in channel
- **WHEN** posting returns 403 Missing Permissions
- **THEN** the relay logs that the bot needs View Channel, Send Messages and Embed Links on the channel and exits non-zero

#### Scenario: Strip style without Attach Files
- **WHEN** the card style is `strip` and posting returns 403 Missing Permissions
- **THEN** the relay logs that the bot needs View Channel, Send Messages, Embed Links and Attach Files on the channel and exits non-zero

### Requirement: Startup channel check
At startup, before subscribing to Wanderer, the relay SHALL verify that the bot token is valid and
that the configured channel exists and is reachable by the bot, and SHALL exit non-zero with a clear
error if not. When the bot's channel permissions can be resolved and the card style is `strip`, a
missing Attach Files permission SHALL also fail the check.

#### Scenario: Wrong channel ID
- **WHEN** the configured channel ID does not exist or is not visible to the bot
- **THEN** the relay exits non-zero at startup without connecting to Wanderer

#### Scenario: Strip style without Attach Files
- **WHEN** the card style is `strip` and the bot lacks Attach Files on the channel
- **THEN** the relay exits non-zero at startup, naming Attach Files, without connecting to Wanderer

## ADDED Requirements

### Requirement: Card images
Portraits and logos SHALL come from the EVE image server by ID:
`https://images.evetech.net/characters/{id}/portrait`, `.../corporations/{id}/logo` and
`.../alliances/{id}/logo`. A party's image SHALL be used only when the payload carries that ID, and
an image that cannot be fetched SHALL NOT prevent the post or delay it beyond a short timeout.

#### Scenario: Image server unavailable
- **WHEN** a kill is posted in the `strip` style while the image server does not answer
- **THEN** the card is posted within a few seconds, with blank tiles where the images would be

### Requirement: Portrait strip layout
In the `strip` card style, the card SHALL be the `components` card with an attached image, shown
in a media gallery under the monospace block in place of the victim and final-blow sections, so
that its header, block and footer line read exactly as the `components` card's. A strip that cannot
be drawn SHALL leave the gallery out and post the rest. The image's left half SHALL show the victim: corporation logo above
alliance logo at the outer edge, then the character portrait, then the victim's name above their
hull. Its right half SHALL mirror this for the final blow, text right-aligned. A missing image SHALL
leave its tile blank.

#### Scenario: Typical kill as a strip
- **WHEN** the typical Stratios kill is posted in the `strip` style
- **THEN** the card carries the `components` card's title, monospace block and footer line, and its image shows Pilot A and `Stratios` with Pilot A's portrait and their corporation and alliance logos on the left, and Pilot B and `Loki` with Pilot B's portrait and logos on the right

#### Scenario: Same footer as the components card
- **WHEN** the same kill is posted in the `strip` and the `components` style
- **THEN** both footers read the classification and the kill time in the reader's own time zone, in the same small text

#### Scenario: Victim without an alliance
- **WHEN** the victim has a corporation but no alliance ID
- **THEN** the victim's alliance tile is blank and the rest of the strip is drawn

#### Scenario: Structure or NPC kill
- **WHEN** the victim has no character, or the final blow has no character
- **THEN** that side shows the corporation name for a victim, or the ship name for an attacker, in place of the pilot, and its portrait tile is blank

### Requirement: Component card layout
In the `components` card style, the card SHALL be a Discord component message with no embed: a
container in the classification colour holding the title linked to zKillboard with the ship render,
the same monospace block as the `block` style, a victim section and a final-blow section, and a
footer line with the classification and the kill time. Each section SHALL show the party's name in
bold, their hull, and their corporation and alliance tickers, with one logo beside it.

#### Scenario: Alliance logo preferred
- **WHEN** the final blow's pilot has a character, a corporation and an alliance
- **THEN** the final-blow section's image is the alliance logo

#### Scenario: Corporation logo when there is no alliance
- **WHEN** the victim has a character and a corporation but no alliance
- **THEN** the victim section's image is the corporation logo

#### Scenario: Portrait when there is no corporation
- **WHEN** a party has a character but no corporation or alliance ID
- **THEN** that section's image is the character portrait

#### Scenario: Nothing to show
- **WHEN** a party has no character, corporation or alliance ID
- **THEN** that section shows its text with no image, and the message is still posted
