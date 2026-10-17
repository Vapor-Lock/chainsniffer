# Spec Delta

## MODIFIED Requirements

### Requirement: Card style configuration
The relay SHALL read the optional setting `CHAINSNIFFER_CARD_STYLE`, case-insensitively, as `block`,
`fields`, `strip` or `components`, defaulting to `strip`, and SHALL post every kill card in that
style. Any other value SHALL be a configuration error that exits with status 2 and names the
variable. The startup configuration summary SHALL include the card style.

#### Scenario: Default style
- **WHEN** `CHAINSNIFFER_CARD_STYLE` is unset
- **THEN** kill cards are posted in the `strip` style and the startup summary says `strip`

#### Scenario: Old style chosen
- **WHEN** `CHAINSNIFFER_CARD_STYLE` is `Fields`
- **THEN** kill cards are posted in the `fields` style

#### Scenario: Component cards chosen
- **WHEN** `CHAINSNIFFER_CARD_STYLE` is `components`
- **THEN** kill cards are posted as component messages

#### Scenario: Unknown style
- **WHEN** `CHAINSNIFFER_CARD_STYLE` is `table`
- **THEN** the relay exits with status 2 and names `CHAINSNIFFER_CARD_STYLE`, before connecting anywhere
