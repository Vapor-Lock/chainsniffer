# Tasks

## 1. Configuration

- [x] 1.1 Add `CardStyle` (`block` default, `fields`) to `internal/config`: a `cardStyle` loader helper reading `CHAINSNIFFER_CARD_STYLE` case-insensitively, an error naming the variable for any other value, and the style in `LogValue`. Verify with config tests: unset gives block, `Fields` gives fields, `table` is an error naming the variable, and the summary shows the style.

## 2. Rendering

- [x] 2.1 Add `discord.CardStyle` and a `style` parameter to `Render`; restore the `fields` layout from commit `da63523` (inline fields, `" / "` affiliation, markdown-escaped alias) beside the block layout, sharing title, colour, footer, thumbnail and timestamp; restore the `da63523` goldens as `testdata/render_fields_*.json` and run the golden test over both styles. Verify `go test ./internal/discord` passes with the restored fields goldens unchanged from `da63523`.
- [x] 2.2 Pass the style through `relay.WithCardStyle` and set it in `cmd/chainsniffer` from config; document `CHAINSNIFFER_CARD_STYLE` in the README settings table, with a sentence on each style. Verify with a relay test that a configured `fields` style posts an embed with fields and no description, and with `nix flake check` passing.

## 3. Live check

- [x] 3.1 Run the binary with `CHAINSNIFFER_CARD_STYLE=fields` and then unset, and confirm the cards in Discord match the old and the new layout.
