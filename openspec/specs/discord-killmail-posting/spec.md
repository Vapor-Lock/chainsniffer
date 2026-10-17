# discord-killmail-posting Specification

## Purpose

Delivers each accepted killmail to the configured Discord channel as a readable embed, using a Discord
bot identity, without losing kills to transient Discord errors or rate limits.

## Requirements

### Requirement: Post to configured channel
The relay SHALL post each accepted killmail as one message with one embed to the configured Discord
channel ID, authenticating as a bot with the configured bot token, in the order the killmails were
received.

#### Scenario: Accepted kill is posted
- **WHEN** a killmail passes all filters
- **THEN** one message with one embed describing it is created in the configured channel

#### Scenario: Order is preserved
- **WHEN** killmails `1`, `2` and `3` are accepted in that order
- **THEN** their messages appear in the channel in the order `1`, `2`, `3`

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

### Requirement: Links and images
The embed SHALL link its title to `https://zkillboard.com/kill/{killmail_id}/` and SHALL show the
victim ship's render image from the EVE image server (`https://images.evetech.net/types/{ship_type_id}/render`)
as the thumbnail when the ship type ID is known.

#### Scenario: zKillboard link
- **WHEN** killmail `123456789` is posted
- **THEN** the embed title links to `https://zkillboard.com/kill/123456789/`

### Requirement: System name
The embed SHALL show the solar system's name when the relay can resolve it from `solar_system_id`,
and the numeric system ID otherwise. A failure to resolve the name SHALL NOT prevent or delay the
post beyond a short timeout.

#### Scenario: Name resolved
- **WHEN** `solar_system_id` is `31000005` and it resolves to `Thera`
- **THEN** the embed shows `Thera`

#### Scenario: Lookup unavailable
- **WHEN** the system name cannot be resolved within the timeout
- **THEN** the embed shows the system ID and the message is still posted

### Requirement: Classification styling
The embed color SHALL reflect the killmail's classification: red for `loss`, green for `kill`, and
grey for `neutral`. The embed footer SHALL state the classification for `loss` and `kill`.

#### Scenario: Friendly loss
- **WHEN** a killmail classified `loss` is posted
- **THEN** its embed is red and the footer says it is a loss

#### Scenario: Neutral kill
- **WHEN** a killmail classified `neutral` is posted
- **THEN** its embed is grey

### Requirement: Rate limit compliance
The relay SHALL honour Discord's rate limits: when Discord answers HTTP 429 it SHALL wait for the
indicated retry-after time and then retry the same message, without dropping it.

#### Scenario: Burst of kills
- **WHEN** 20 killmails are accepted within one second and Discord responds 429 with a retry-after
- **THEN** the relay waits as instructed and eventually posts all 20 messages, each exactly once

### Requirement: Transient failure retry
On a network error or HTTP 5xx from Discord, the relay SHALL retry the post with exponential backoff
for up to 5 minutes before giving up on that killmail. A killmail that is given up on SHALL be logged
at error level with its ID and SHALL NOT be recorded as posted.

#### Scenario: Discord briefly unavailable
- **WHEN** Discord returns 502 twice and then succeeds
- **THEN** the message is posted once and the killmail is recorded as posted

#### Scenario: Discord down for a long time
- **WHEN** posting keeps failing for more than 5 minutes
- **THEN** the relay logs an error naming the killmail ID, moves on to the next killmail, and does not record the failed one as posted

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
