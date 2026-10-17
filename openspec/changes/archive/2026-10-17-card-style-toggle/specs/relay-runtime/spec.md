# Spec Delta

## ADDED Requirements

### Requirement: Card style configuration
The relay SHALL read the optional setting `CHAINSNIFFER_CARD_STYLE`, case-insensitively, as `block`
or `fields`, defaulting to `block`, and SHALL post every kill card in that style. Any other value
SHALL be a configuration error that exits with status 2 and names the variable. The startup
configuration summary SHALL include the card style.

#### Scenario: Default style
- **WHEN** `CHAINSNIFFER_CARD_STYLE` is unset
- **THEN** kill cards are posted in the `block` style and the startup summary says `block`

#### Scenario: Old style chosen
- **WHEN** `CHAINSNIFFER_CARD_STYLE` is `Fields`
- **THEN** kill cards are posted in the `fields` style

#### Scenario: Unknown style
- **WHEN** `CHAINSNIFFER_CARD_STYLE` is `table`
- **THEN** the relay exits with status 2 and names `CHAINSNIFFER_CARD_STYLE`, before connecting anywhere
