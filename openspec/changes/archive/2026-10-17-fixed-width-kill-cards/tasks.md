# Tasks

## 1. Monospace block

- [x] 1.1 In `internal/discord/render.go`, replace `fields` with a builder for the description's code block: rows System, Value, Attackers, Victim, Final blow in that order, labels in an 11-column column, empty rows dropped, Victim and Final blow with corporation and alliance on lines of their own (design: "The body is one code block"); stop setting `embed.Fields`. Verify with `go test ./internal/discord` after regenerating the goldens (`go test ./internal/discord -update`) and reading each changed `testdata/render_*.json` by eye against the design's example.
- [x] 1.2 Wrap values at `blockWidth = 40` columns: break at the last space that fits, hard-break words longer than the value column, indent continuation lines to the value column. Verify with a table test in `render_test.go` asserting that no block line exceeds 40 runes and that a long corporation name continues in column 12.
- [x] 1.3 Drop the markdown escaping of the alias inside the block and replace every backtick in every block value with U+02CB; keep `maxAliasLen`. Verify with tests for the spec's "Markdown characters in an alias" (`J134349 (A_B_)`, no backslashes) and "Backticks in a name" (the description contains exactly two fences) scenarios, and that the existing alias tests still pass with the System row.

## 2. Uniform width

- [x] 2.1 End every description, after the closing fence, with a line of `padRunes` U+2800 characters, the same for every classification; leave the footer as today (Loss, Kill, none for neutral). Start `padRunes` at a generous value with a comment saying how it was chosen. Verify with a test that the kill, loss and neutral embeds of one killmail end in the same padding line and that the neutral embed still has no footer.
- [x] 2.2 Update `README.md`'s description of an embed (the bullet list near "Each killmail becomes an embed" and the mentions of "the System field") to describe the aligned block and the System row. Verify with `grep -n "System field\|Fields:" README.md` returning nothing stale.
- [x] 2.3 Run the gate: `nix flake check` passes (tests, vet, golangci-lint, image).

## 3. Live check

- [x] 3.1 Post the golden kills (typical, structure, minimal, alias, and one with long corporation and alliance names) to a test Discord channel with the built binary or a throwaway Go snippet using `discord.Render`, and check in the desktop client that every card is the same width and the rows line up; widen `padRunes` or narrow `blockWidth` if not, then rerun 2.3. Record the chosen values and the client checked in the commit message. This needs a bot token and a test channel from the user, so an agent without them leaves this task open and reports it.

## Workflow follow-up

- Archive the change with `/opsx:archive` once it is merged.
