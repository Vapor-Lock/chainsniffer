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
- **THEN** the embed title names the Stratios and Pilot A, and the embed shows J123456, [CORP], Pilot B and the Loki, 7 attackers, `412.5M ISK` and the kill time

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
(HTTP 404), the relay SHALL log an error explaining the required permissions and exit non-zero.

#### Scenario: Bot not allowed in channel
- **WHEN** posting returns 403 Missing Permissions
- **THEN** the relay logs that the bot needs View Channel, Send Messages and Embed Links on the channel and exits non-zero

### Requirement: Startup channel check
At startup, before subscribing to Wanderer, the relay SHALL verify that the bot token is valid and
that the configured channel exists and is reachable by the bot, and SHALL exit non-zero with a clear
error if not.

#### Scenario: Wrong channel ID
- **WHEN** the configured channel ID does not exist or is not visible to the bot
- **THEN** the relay exits non-zero at startup without connecting to Wanderer
