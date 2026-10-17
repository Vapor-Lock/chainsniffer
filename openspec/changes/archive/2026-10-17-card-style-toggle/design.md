# Design

## Context

`discord.Render(k, class, systemName, alias)` builds the block card: title, colour, footer,
thumbnail and timestamp, plus a description holding the monospace block and a padding line. The
`fields` card it replaced exists, whole, in `internal/discord/render.go` at commit `da63523`
(`fields`, `affiliation` joining with `" / "`, `markdownEscaper` on the alias), with golden embeds
in `internal/discord/testdata/` at the same commit. Settings parse in `internal/config` through
`loader` helpers such as `logFormat`, and any error ends in exit status 2.

## Goals / Non-Goals

**Goals:**
- One setting chooses the card layout, with the block style as the default and the `fields` style
  byte-for-byte what it was before `fixed-width-kill-cards`.

**Non-Goals:**
- Changing the style per kill, per classification or at runtime.
- Any third layout, or giving the `fields` style uniform-width padding.

## Decisions

### `discord.CardStyle` is the type; `config` parses into it

`internal/discord` declares `type CardStyle string` with `CardStyleBlock = "block"` and
`CardStyleFields = "fields"`, because the renderer owns what a style means. `internal/config` parses
`CHAINSNIFFER_CARD_STYLE` with a `cardStyle` loader helper modelled on `logFormat`: case-insensitive,
default `block`, any other value an error naming the variable and the two accepted values.
`Config.CardStyle` holds the parsed value, and `LogValue` includes it.

- *Alternative:* a boolean such as `CHAINSNIFFER_LEGACY_CARDS`. Rejected: a named style reads
  better in config files and leaves room for another layout without a second flag.

Having `config` import `discord` for the type would point a dependency outward. Instead, `config`
declares its own `CardStyle` string type with the same two constants, and `cmd/chainsniffer`
converts it (`discord.CardStyle(cfg.CardStyle)`). This matches how the other packages keep their
dependencies pointing inward.

### `Render` takes the style; the two layouts share everything else

`Render(k, class, systemName, alias, style)` builds the common parts once, then fills either
`Description` (block) or `Fields` (fields). The `fields` path restores `da63523`'s `fields`,
`affiliation` (joined by `" / "`) and `system` (with the alias markdown-escaped and capped at
`maxAliasLen`), renamed where they collide with the block path's helpers. An unknown style renders
as `block`, since config has already rejected it.

`relay` takes the style through a `WithCardStyle` option, defaulting to block, and passes it to
`Render`. `cmd/chainsniffer` sets it from config.

- *Alternative:* two exported functions, `RenderBlock` and `RenderFields`, chosen by the relay.
  Rejected: it puts layout choice in the relay and duplicates the shared embed parts at the call
  site.

### Goldens for both styles

The existing goldens stay as the block style. The five `da63523` goldens return as
`render_fields_<case>.json`, and `TestRenderGolden` gains a style column. Because the restored
goldens are taken verbatim from `da63523`, the test itself proves the `fields` output is unchanged
from the old cards.

## Risks / Trade-offs

- [Restored code drifts from the block path's fixes, such as backtick handling] → backticks matter
  only inside a code block, and the `fields` style has none. Its escaping is the one it always had.
- [Interaction with `resolve-affiliation-names`] → none in code. The ESI names fill in the
  `Killmail` before `Render`, so both styles show them.
- [Default stays `block`, so deployments that wanted the old look must set the variable] → the
  README's settings table documents it.
