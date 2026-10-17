# Tasks

## 1. Building blocks

- [ ] 1.1 Add `internal/eveimage`: fetch and decode an image URL (PNG and JPEG) with a timeout, a 256-entry in-memory LRU of successes, failures logged at warn and not cached, plus URL helpers for character portraits, corporation logos and alliance logos. Verify with httptest-backed tests: PNG and JPEG decode, cache hit makes no request, 404, timeout, eviction.
- [ ] 1.2 Add `internal/cardimage.Strip` and the `golang.org/x/image` dependency (update `vendorHash` in `flake.nix`): draw the 1024 × 136 strip per the design's geometry, with blank tiles for nil images, right-aligned attacker text, ellipsis truncation and the text fallbacks. Verify with golden PNGs built from fixture images (typical, missing alliance, no character, long names) and `nix flake check`.
- [ ] 1.3 Add `discord.Message`, `File` and `Component` types; `Post(ctx, Message)` sends embeds with files through `ChannelMessageSendComplex` (a fresh reader per attempt) and components as Components V2 through `RequestWithBucketID`, keeping retries and classification; add `WithAttachFiles` to the permission check and hint; update the existing `Post` callers to wrap their embed. Verify with poster tests against the fake Discord server: multipart upload carries the file on a retry, the components body has flag 32768 and no embeds, 429 and 403 behave as before, and Attach Files is required only when enabled.

## 2. Layouts

- [ ] 2.1 Add `discord.ComponentsMessage` per the design (container colour, header section with ship render, the shared monospace block, separator, victim and final-blow sections with the alliance, then corporation, then portrait accessory, timestamp footer) and `discord.StripMessage(embed, png)`. Verify with golden JSON for typical, no alliance, no corporation, no IDs, and neutral, plus a test that the strip embed's image is `attachment://kill.png`.
- [ ] 2.2 Extend the card style in `internal/config` and `internal/discord` with `strip` and `components`, defaulting to `components` (moved from `strip` after the live check). Verify with config tests for the default, both new values, and the error message listing all four.

## 3. Wiring

- [ ] 3.1 In `internal/relay`, declare `Images`, dispatch on the style (block, fields, strip with images fetched concurrently under one timeout and a fallback to the block embed, components), and wire `eveimage`, the style and `WithAttachFiles` in `cmd/chainsniffer`; document the four styles, the Attach Files permission and the new default in `README.md`. Verify with relay tests using fakes (strip posts an embed with a file, failed images still post, a failed draw falls back to block, components posts components) and `nix flake check`.

## 4. Live check

- [ ] 4.1 Post the golden kills in the `strip` and `components` styles to the test channel; check strip legibility and card width (retune canvas, fonts and padding if needed) and whether component cards need the padding line; confirm a bot without Attach Files fails the startup check in `strip`.
