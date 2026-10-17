# Design

## Context

After `card-style-toggle`, `discord.Render(k, class, systemName, alias, style)` returns a
`*discordgo.MessageEmbed` in the `block` or `fields` style. `relay.handle` passes it to
`Poster.Post(ctx, embed)`, which sends it with `ChannelMessageSendEmbed`, retrying on 429/5xx and
classifying 401/403/404 as fatal (`classify`, `hintFor`). The startup check (`checkPermissions`)
requires View Channel, Send Messages and Embed Links. `config` parses the style with a `cardStyle`
helper, and `discord.CardStyle` names the styles.

What Discord can do, and why there are two layouts:

- **Embeds** have one thumbnail (top right), one large image (bottom), and small author and footer
  icons. Text cannot hold images, so the requested layout, with images on both outer edges, cannot
  be built from an embed.
- **Components V2** messages (flag `IS_COMPONENTS_V2`, `1 << 15`) can hold a container (type 17,
  with an accent colour), sections (type 9: up to three text displays plus one accessory), text
  displays (type 10, markdown), thumbnails (type 11) and separators (type 14).
  - A section's accessory is always on the right, and sections stack vertically.
  - A components message cannot also carry `content` or `embeds`.
  - `discordgo` v0.29.0 has no types for them, so they are sent as raw JSON.

The EVE image server serves `characters/{id}/portrait`, `corporations/{id}/logo` and
`alliances/{id}/logo`, with `?size=` in powers of two from 32 to 512 (to 1024 for portraits).
Portraits are JPEG and logos are PNG. Wanderer's payload carries `victim_char_id`,
`final_blow_char_id` and the corporation and alliance IDs, even when it leaves out their names.

## Goals / Non-Goals

**Goals:**
- `strip`: draw the requested layout exactly, as an image under the block card's monospace block.
- `components`: the closest native layout, needing no downloads by the relay.
- Images never block or fail a post.

**Non-Goals:**
- Theme-aware strip colours. Discord does not tell a bot the reader's theme, so the strip draws its
  own dark panel.
- Persisting downloaded images; portraits can change, and an in-memory cache is enough.
- Changing the `block` or `fields` cards.

## Decisions

### A `discord.Message` covers every card; `Post` sends either kind

`internal/discord` gains:

```go
type Message struct {
    Embed      *discordgo.MessageEmbed // block, fields, strip
    Files      []File                  // strip: the PNG
    Components []Component             // components: sent as Components V2
}
type File struct { Name, ContentType string; Data []byte }
```

`Post(ctx, Message)` keeps its retry loop and its error classification:
- An embed is sent with `ChannelMessageSendComplex`. A fresh `bytes.Reader` is made over each
  file's `Data` on every attempt, so a retry re-uploads the whole file.
- Components are sent with `session.RequestWithBucketID("POST", EndpointChannelMessages(ch),
  {flags: 1<<15, components}, ...)`, whose errors are the same `*discordgo.RESTError` that
  `classify` reads.

`Component` is a small set of Go structs with JSON tags for types 9, 10, 11, 14 and 17. They are
marshalled as is, and golden-tested.

- *Alternative:* upgrading `discordgo` for typed components. Rejected: no released version models
  Components V2 completely, and a handful of structs is less risk than a dependency jump.

### `strip`: the relay fetches, `internal/cardimage` draws, `discord` attaches

- **`internal/eveimage`** fetches an image URL with a timeout and decodes it (`image/png`,
  `image/jpeg`). It keeps decoded images in a bounded in-memory LRU (256 entries) and returns
  `(image.Image, bool)`; a failure is logged at warn and not cached. It declares nothing the other
  packages need.
- **`internal/cardimage`** is pure: `Strip(StripInput) ([]byte, error)` takes the two parties'
  text and their decoded images, `nil` for missing, and returns a PNG. It imports only the standard
  library and `golang.org/x/image` (`font/opentype`, `font/gofont/gobold`, `gofont/goregular`,
  `draw.CatmullRom`). Golden PNGs, built from fixture images, pin its output.
- **`relay`** declares `Images` (`Fetch(ctx, url) (image.Image, bool)`). For a `strip` card it
  fetches the six images concurrently under one context bounded by about 3 seconds, calls
  `cardimage.Strip`, and passes the PNG to `discord.StripMessage(embed, png)`. That function sets
  `embed.Image.URL = "attachment://kill.png"` and attaches the file.
  - A strip that fails to draw is logged at warn, and the card goes out without its gallery.

**The strip card became a components card in the live check.** Posted side by side, the `strip`
embed's footer ("Kill • Today at 4:08 PM", the embed timestamp Discord draws) did not match the
`components` card's footer line ("Kill · October 17, 2026 4:08 PM", a `<t:…:f>` tag). An embed
footer is plain text, so it cannot carry the tag. The strip card is now the `components` card
with a media gallery showing `attachment://kill.png` where the components card has its two party
sections, which the strip already shows: `discord.StripMessage(k, class, system, alias, png)`
builds it on the same container, and a nil PNG leaves the gallery out. `discord.Message` allows
files beside components, which `Post` uploads as one multipart body (`MultipartBodyWithJSON` and
`RequestRaw`, since discordgo has no call taking both). The `block` card the strip used to carry
is unchanged for the `block` style.

**Strip geometry** (constants, retuned in the live check):
- **Canvas:** 1024 × 136 px on a dark rounded panel (`#2B2D31`).
- **Logos:** 64 px squares stacked at the outer edges, corporation on top.
- **Portraits:** 128 px squares inside them.
- **Text:**
  - Name: Go Bold at 34 px in `#F2F3F5`.
  - Hull: Go Regular at 30 px in `#B5BAC1`.
  - Two lines aligned with the tiles. The victim's text is left-aligned and the attacker's
    right-aligned, each truncated with `…` to its half of the canvas.
- **Missing image:** a blank tile in a slightly lighter grey, so the layout never shifts.
- **Text fallbacks:**
  - Victim's name: character, else corporation, else `Unknown pilot`.
  - Attacker's name: character, else ship (an NPC).
  - Hull: ship name, else `Unknown ship`.

The canvas is drawn at roughly twice the size Discord shows it, so it stays sharp on high-DPI
screens. An embed image also affects the card's width, so the live check reviews the padding
counts with the strip attached.

### `components`: native layout from image URLs, no downloads

`discord.ComponentsMessage(k, class, systemName, alias)` builds:

1. A **container** with `accent_color` set to the classification colour.
2. A **header**: a section whose text is `### [<ship> — <pilot>](<zkill url>)`, with the ship render
   as a thumbnail accessory. Without a ship type ID it is a text display.
3. A **text display** holding the same monospace block the `block` style draws, built by the same
   function.
4. A **separator**.
5. Two **party sections**, victim then final blow. Their text is `**<name>**`, then the hull, then
   the `[CORP] / <ALLY>` tickers. The accessory is a thumbnail of the alliance logo, else the
   corporation logo, else the character portrait. A party with none of those IDs becomes a plain
   text display, since a section requires an accessory.
6. A **footer** text display, `-# Kill · <t:UNIX:f>`. Discord's timestamp markdown shows the kill
   time in the reader's own time zone. A neutral kill has only the time.

Discord fetches the thumbnails itself, so the relay downloads nothing. Total text stays far below
the 4000-character limit for component messages.

The monospace block keeps no padding line here, because a container's width is set by Discord, not
by its text. The live check confirms that, and adds the line back if containers turn out to size to
content.

### Configuration and permissions

The `CardStyle` constants in `config` and `discord` gain `strip` and `components`, and the default
moves to `components`. It was `strip` until the live check, which settled on the component card as
the default; `strip` stays one setting away, and a default without an upload needs no new
permission. `discord.New` takes a `WithAttachFiles(bool)` option, which `cmd/chainsniffer`
sets for the `strip` style. It adds Attach Files to `missingPermissions` and to the 403/404 hint.

## Risks / Trade-offs

- [A deployment that picks `strip` lacks Attach Files] → the startup check stops the relay before
  it connects, naming the permission. The default, `components`, uploads nothing and needs no new
  permission; `CHAINSNIFFER_CARD_STYLE=block` is the one-line way back to the earlier cards.
- [Discord shows embed images small, so the strip's text may be tiny] → the canvas size and font
  sizes are constants, tuned in the live check against real cards.
- [Image server slow] → a strip card waits at most about 3 seconds, then draws blank tiles. Repeat
  kills by the same pilots hit the cache.
- [Components V2 API changes or client rendering quirks] → the JSON is golden-tested, and the
  `block` style stays one setting away.
- [`golang.org/x/image` dependency] → maintained by the Go team; `vendorHash` changes once, in the
  first bead that adds it.
- [Upload size] → a 1024 × 136 PNG is tens of kilobytes, far below Discord's attachment limit.
