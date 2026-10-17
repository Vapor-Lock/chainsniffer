# Proposal

## Why

Kill cards in the channel come out a different width each time: Discord sizes an embed to its
content, and inline fields reflow into one, two or three columns depending on how long the system
name, affiliations and final-blow line are. A run of kills reads as a ragged stack that is hard to
scan, and the same field lands in a different place on every card.

## What Changes

- The card body moves from inline embed fields to one monospace code block in the embed
  description: one row per item (System, Value, Attackers, Victim, Final blow), labels in a fixed
  column, values aligned after them. Values longer than the block's width wrap onto continuation
  lines indented to the value column. Rows with nothing to show are still left out.
- The Victim and Final blow rows put the corporation and the alliance on lines of their own instead
  of joining them with ` / `, so they fit the block's width.
- Every card is padded to one width: the description ends with a line of blank padding characters
  (U+2800) wide enough that Discord draws each embed at its maximum width, whatever its content and
  classification.
- Text inside the code block is no longer markdown-escaped (a code block shows backslashes
  literally); instead backticks in names and aliases are replaced so they cannot close the block.
- Title, title link, thumbnail, colour, timestamp and footer classification are unchanged.
- **BREAKING** (for anyone reading the posts programmatically): the embed no longer has `fields`;
  the same information is in `description`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `discord-killmail-posting`: "Embed content" now requires the details in an aligned monospace
  block rather than separate fields; a new "Uniform card width" requirement makes every card the
  same width; "Chain alias" refers to the System row instead of the System field.

## Impact

- `internal/discord/render.go`: `Render` builds a padded description instead of fields; `system`
  stops markdown-escaping the alias.
- `internal/discord/render_test.go` and every golden embed in `internal/discord/testdata/`.
- `README.md`: the description of what an embed looks like.
- No new dependencies, configuration, or change to posting, retries or the relay.
