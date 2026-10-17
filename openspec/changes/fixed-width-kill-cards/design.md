# Design

## Context

`internal/discord/render.go` builds the embed as a title, a thumbnail, a colour, a timestamp, a
classification footer and up to five inline fields (System, Value, Attackers, Victim, Final blow),
dropping every part the payload did not carry. `Render` is a pure function pinned by golden JSON
embeds in `internal/discord/testdata/`. The System field's alias is markdown-escaped and capped at
`maxAliasLen` characters. Discord sizes an embed to its content, up to a maximum of about 520px on
desktop; a narrow mobile screen caps it lower. See proposal.md for why the ragged width matters.

## Goals / Non-Goals

**Goals:**
- One layout for every card: the same rows in the same places, at the same width.
- Keep `Render` pure and golden-tested, with no new dependency or configuration.

**Non-Goals:**
- Identical width on mobile clients, where the screen width decides and cards are already about as
  wide as the screen.
- Rendering the card as an image, or any change to the title, link, thumbnail, colour or timestamp.
- Making the column width or padding configurable; they are constants tuned once in a real channel.

## Decisions

### The body is one code block in the description

The description becomes:

````
```
System     J134349 (HOTEL)
Value      412.5M ISK
Attackers  7
Victim     [CORP] Victim Corporation
           <VCTM> Victim Alliance
Final blow Pilot B (Loki)
           [FRND] Friendly Corporation
           <BLUE> Friendly Alliance
```
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
````

- Labels are left-aligned in a column 11 characters wide (`Final blow` plus one space), so every
  value starts in column 12. A row whose value is empty is dropped, as fields are today.
- The Victim and Final blow rows give the corporation and the alliance a line each, under the
  value column, in place of today's `" / "` join; the Final blow row keeps `Pilot (Ship)` on its
  first line. Each half is still dropped when the payload lacks it.
- *Alternative:* keep inline fields and only pad them. Rejected: Discord reflows inline fields into
  one to three columns from their content, so the rows move between cards even when the width is
  fixed.

### Lines are wrapped at a fixed 40 columns

`blockWidth = 40` columns, leaving 29 for the value. A value that does not fit wraps at the last
space before the limit onto a continuation line indented to the value column; a word longer than
29 columns is broken hard. Width is counted in runes. 40 columns fits beside the thumbnail inside
Discord's maximum embed width with room to spare, so the block itself never decides a card's
width; the padding does. The constant is tuned in the live check (see tasks).

### Uniform width comes from a padding line at the end of the description

The description ends, after the closing fence, with a line of `padRunes` U+2800 BRAILLE PATTERN
BLANK characters. Discord does not trim U+2800 as whitespace, and it renders blank, so the line is
invisible but has width. `padRunes` is chosen to exceed the maximum embed width on desktop, so
every card is drawn at that maximum: nothing else in the embed, the title included, can then make
a card wider, because Discord wraps everything at the maximum.

- Overshooting is safe: an over-long run wraps onto a second blank line, which costs a line of
  height but keeps the width at the maximum. Undershooting is not, so the constant errs high.
- Every classification, neutral included, gets the same line, so the neutral card no longer needs
  a footer to be padded and keeps having none.
- *Alternative:* pad the footer, as first suggested. Rejected: Discord draws the timestamp on the
  footer line after the footer text, so padding there pushes the timestamp off to the right or onto
  a line of its own.
- *Alternative:* pad each block line with trailing spaces to 40 columns. Rejected: browsers let
  trailing preserved spaces hang outside the line box, so they may not widen the block at all, and
  the block would still be narrower than a long title.
- *Alternative:* a wide transparent spacer image. Rejected with the user: it needs hosting or an
  upload per message and adds blank space under every card.

### Names are literal; backticks are neutralised

Text inside a code block is shown verbatim, so the alias is no longer markdown-escaped (an escape
would show up as a backslash). The one character that matters there is the backtick, which could
close the fence early: every backtick in a value is replaced with U+02CB MODIFIER LETTER GRAVE
ACCENT, which looks alike and is not markdown. This applies to every value in the block, not just
the alias, because corporation and alliance names come from the payload. `maxAliasLen` stays: it
keeps one alias from wrapping into a wall of continuation lines, and the description's 4096
character limit is far off.

## Risks / Trade-offs

- [Discord's maximum embed width or its font changes] → `padRunes` errs high, so a wider maximum is
  the only case that breaks uniformity; the live check documents how the value was chosen, so it is
  quick to retune.
- [On mobile the 40-column block may be wider than the screen and wrap inside the block] → accepted
  (a non-goal); the rows stay labelled and in order, just less neatly aligned.
- [Wide characters, such as CJK names, take two columns but count as one rune] → such a line may run
  past 40 columns; Discord then wraps it inside the block. EVE names are almost always ASCII, so the
  rune count is kept rather than adding a width table.
- [Every card now has an extra blank line under the block] → the price of the padding carrier; it is
  less intrusive than a displaced timestamp or a spacer image.
- [Anything that read `fields` from the posts stops finding them] → the bot's posts have no known
  machine consumer; the proposal marks it breaking.

## Migration Plan

None beyond deploying the new binary: already posted embeds are not edited (unchanged policy), so
the channel shows old-style cards above new-style ones. Rolling back is redeploying the previous
image.
