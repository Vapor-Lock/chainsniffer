# Proposal

## Why

The kill card names the victim and the final blow only as text. Pilots recognise each other by
portrait and by corporation and alliance logo faster than by name. Every ID those images need
(`victim_char_id`, `final_blow_char_id`, and the corporation and alliance IDs) is already in
Wanderer's payload, and the EVE image server serves the images by ID.

Discord's native message parts cannot reproduce the requested layout: a portrait and two stacked
logos on each outer edge, with the victim on the left and the final blow on the right. So the relay
offers two layouts:
- a rendered image strip that matches the layout exactly;
- a native Discord component card that approximates it with what Discord supports.

The operator chooses between them.

## What Changes

- `CHAINSNIFFER_CARD_STYLE` gains two values, `strip` and `components`, beside `block` and
  `fields`. **The default becomes `strip`.**
- **`strip`:** the block card, monospace block unchanged, plus a PNG the relay draws and attaches
  as the embed image, so it shows under the block.
  - The left half shows the victim: corporation logo over alliance logo on the outer edge, the
    character portrait, then the victim's name over their hull.
  - The right half mirrors it for the final blow.
  - Images come from `images.evetech.net`. A missing ID or a failed download leaves a blank tile,
    and the kill still posts.
- **`components`:** the card is sent as a Discord component message (Components V2) instead of an
  embed. A container in the classification colour holds, in order:
  - the linked title with the ship render;
  - the same monospace block;
  - a victim section and a final-blow section, each showing **name**, hull and
    `[CORP] / <ALLY>` tickers, with a logo on the right: the alliance logo, or the corporation
    logo when there is no alliance, or the character portrait when there is no corporation;
  - a footer line with the classification and the kill time.
- The `strip` style needs Discord's **Attach Files** permission. The startup permission check and
  the 403 hint include it when `strip` is configured.
- **BREAKING (operations):** with the new default, an existing deployment must grant Attach Files,
  or set `CHAINSNIFFER_CARD_STYLE=block`. Otherwise the startup check stops the relay with a clear
  error.
- New dependency: `golang.org/x/image`, for drawing text with the bundled Go fonts and for
  scaling images.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `discord-killmail-posting`: adds the "Portrait strip layout", "Component card layout" and "Card
  images" requirements. "Startup channel check" and "Fatal Discord errors" now cover Attach Files
  for the strip.
- `relay-runtime`: "Card style configuration" (introduced by `card-style-toggle`) accepts `strip`
  and `components`, and defaults to `strip`.

## Impact

- New `internal/eveimage`: fetches, decodes and caches images from `images.evetech.net`, with a
  timeout.
- New `internal/cardimage`: draws the strip PNG from decoded images and text. A pure function,
  golden-tested.
- `internal/discord`: a `Message` type covering an embed with files, or a component message;
  `Poster.Post` sends either; builders for the strip and component layouts; the permission check
  and hint.
- `internal/config`, `internal/relay`, `cmd/chainsniffer`: the two new styles, image fetching for
  the strip, and wiring.
- `flake.nix` `vendorHash`, `go.mod` and `go.sum`; README (settings, permissions, layouts).
- Depends on `card-style-toggle`, which is archived first. Benefits from
  `resolve-affiliation-names`, which fills in the component card's tickers, but does not need it.
