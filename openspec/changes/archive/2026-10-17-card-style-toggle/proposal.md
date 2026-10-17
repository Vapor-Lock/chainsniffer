# Proposal

## Why

The aligned monospace kill card from `fixed-width-kill-cards` replaced the inline-field card
outright. Some channels or readers may prefer the old layout, which reads more naturally on mobile
and needs no width padding. An operator should be able to choose between the two without a
rebuild.

## What Changes

- New optional setting `CHAINSNIFFER_CARD_STYLE`:
  - `block` (the default) posts the aligned monospace card with uniform-width padding, as today.
  - `fields` posts the pre-`fixed-width-kill-cards` card, with System, Value, Attackers, Victim and
    Final blow as inline embed fields.
- The value is case-insensitive. Any other value is a configuration error: the relay exits with
  status 2 and names the variable, like the other settings.
- The startup configuration summary includes the card style.
- Title, link, thumbnail, colour, timestamp and footer are identical in both styles, and so is the
  System name and alias rule (the `fields` style keeps markdown-escaping the alias, as it always
  did).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `relay-runtime`: adds the `CHAINSNIFFER_CARD_STYLE` setting.
- `discord-killmail-posting`: the monospace body, uniform-width and literal-name requirements
  apply to the `block` style. A new requirement describes the `fields` style. "Embed content" and
  "Chain alias" are reworded to cover both.

## Impact

- `internal/config`: a `CardStyle` field, its parsing, and the redacted summary.
- `internal/discord/render.go`: `Render` takes the style. The `fields` layout returns from the code
  as of commit `da63523`, next to the block layout, sharing the title, colour, footer, thumbnail
  and timestamp code.
- `internal/relay` and `cmd/chainsniffer`: pass the configured style through to `Render`.
- Golden embeds for the `fields` style come back alongside the block ones.
- README: the settings table, and a sentence on each card style.
