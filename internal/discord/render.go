package discord

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Vapor-Lock/chainsniffer/internal/killmail"
	"github.com/bwmarrin/discordgo"
)

// The embed colors for each classification, per the discord-killmail-posting
// spec's "Classification styling": red for a loss, green for a kill, grey for
// a kill neither side of which is friendly.
const (
	ColorLoss    = 0xC0392B
	ColorKill    = 0x27AE60
	ColorNeutral = 0x7F8C8D
)

const (
	// killURL is zKillboard's page for one kill, which the title links to.
	killURL = "https://zkillboard.com/kill/%d/"

	// shipRenderURL is the EVE image server's render of a ship type. 128px is
	// the largest size Discord shows a thumbnail at.
	shipRenderURL = "https://images.evetech.net/types/%d/render?size=128"
)

// What the title says about a victim the payload did not name. A kill is worth
// reporting even when Wanderer sent an incomplete entry, so the title says so
// instead of coming out half empty.
const (
	unknownShip  = "Unknown ship"
	unknownPilot = "Unknown pilot"
)

// CardStyle is the layout Render gives a card: one monospace block in the
// description (CardStyleBlock), or inline fields (CardStyleFields), the cards
// the relay posted before the block replaced them. Both share the title, the
// link, the colour, the thumbnail, the timestamp and the footer.
//
// CardStyleStrip and CardStyleComponents are the styles the relay posts as
// more than one embed: the block card with a portrait strip image, and a
// Components V2 card. The relay picks their message builders; Render gives
// both the block embed.
type CardStyle string

const (
	CardStyleBlock      CardStyle = "block"
	CardStyleFields     CardStyle = "fields"
	CardStyleStrip      CardStyle = "strip"
	CardStyleComponents CardStyle = "components"
)

// Render turns one classified killmail into the embed the relay posts, laid
// out in the given style; any style but CardStyleFields renders as
// CardStyleBlock.
//
// systemName is the name resolved for k.SolarSystemID, or its decimal ID when
// the name could not be resolved (see internal/universe); an empty systemName
// leaves the System row out. alias is the system's chain alias, Wanderer's
// temporary name for it, which the System row shows in parentheses after the
// name, as "J134349 (HOTEL)"; an empty alias shows the name alone.
//
// In the block style, everything but the title, the link, the thumbnail, the
// colour, the timestamp and the footer goes in the description as one
// monospace block of labelled rows (see block), so the rows line up the same
// way on every card. The description ends in padLine, whatever the
// classification, so every card is drawn at the same width, its length set by
// whether the card has a thumbnail. In the fields style, the same items are
// inline fields (see fields) and there is no description.
//
// Render is a pure function of its arguments — posting lives elsewhere in this
// package — so the layout is covered by golden tests. Every field of a
// killmail but its ID is optional (see internal/killmail), so each part of the
// embed is dropped when the payload did not carry it: the embed always
// renders, and never shows an empty row or a dangling separator.
func Render(k killmail.Killmail, c killmail.Classification, systemName, alias string, style CardStyle) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		Type:  discordgo.EmbedTypeRich,
		Title: title(k.Victim),
		URL:   fmt.Sprintf(killURL, k.ID),
		Color: color(c),
	}
	if style == CardStyleFields {
		embed.Fields = fields(k, fieldsSystem(systemName, alias))
	} else {
		embed.Description = join("\n", block(rows(k, system(systemName, alias))), padLine(k.Victim.ShipTypeID > 0))
	}
	if k.Victim.ShipTypeID > 0 {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{
			URL: fmt.Sprintf(shipRenderURL, k.Victim.ShipTypeID),
		}
	}
	// Discord wants an RFC 3339 timestamp; a killmail whose kill_time was
	// missing or unparseable gets none rather than the zero time.
	if !k.KillTime.IsZero() {
		embed.Timestamp = k.KillTime.UTC().Format(time.RFC3339)
	}
	if text := footer(c); text != "" {
		embed.Footer = &discordgo.MessageEmbedFooter{Text: text}
	}
	return embed
}

// title reads "<ship> — <pilot>". A structure and an NPC-owned wreck have no
// victim character, so the owning corporation stands in for the pilot.
func title(victim killmail.Party) string {
	who := victim.CharName
	if who == "" {
		who = victim.CorpName
	}
	return orElse(victim.ShipName, unknownShip) + " — " + orElse(who, unknownPilot)
}

func color(c killmail.Classification) int {
	switch c {
	case killmail.Loss:
		return ColorLoss
	case killmail.Kill:
		return ColorKill
	default:
		return ColorNeutral
	}
}

// footer states the classification for a loss and a kill, and is empty for a
// neutral one, which the colour alone marks.
func footer(c killmail.Classification) string {
	switch c {
	case killmail.Loss:
		return "Loss"
	case killmail.Kill:
		return "Kill"
	default:
		return ""
	}
}

// maxAliasLen caps the alias, in characters, so that one alias cannot wrap
// into a wall of continuation lines in the block, and stays well inside
// Discord's 1024-character field value limit in the fields style: a field
// over the limit makes Discord reject the whole embed, and the kill with it.
const maxAliasLen = 100

// system is the System row's value: the system name, followed by its chain
// alias in parentheses when it has one. An alias without a name to follow has
// nothing to annotate and is left out with it. The block shows the alias
// verbatim, so it needs no markdown escaping.
func system(name, alias string) string {
	if name == "" || alias == "" {
		return name
	}
	if r := []rune(alias); len(r) > maxAliasLen {
		alias = string(r[:maxAliasLen])
	}
	return name + " (" + alias + ")"
}

// row is one labelled item of the block. Its value may span several lines.
type row struct {
	label, value string
}

// rows lists the block's rows, in the order the design lays them out. Rows
// with nothing to say are left out entirely.
func rows(k killmail.Killmail, systemName string) []row {
	var out []row
	add := func(label, value string) {
		if value == "" {
			return
		}
		out = append(out, row{label, value})
	}

	add("System", systemName)
	// A killmail whose payload omitted total_value decodes to 0 ISK, which
	// says nothing, so the row is dropped rather than claiming a free kill.
	if k.TotalValue > 0 {
		add("Value", FormatISK(k.TotalValue)+" ISK")
	}
	if k.AttackerCount > 0 {
		add("Attackers", strconv.Itoa(k.AttackerCount))
	}
	// The victim's ship and pilot are already in the title, so this row
	// carries their affiliation.
	add("Victim", affiliation(k.Victim))
	add("Final blow", finalBlow(k.FinalBlow))
	return out
}

const (
	// blockWidth is the most columns, counted in runes, that any line of the
	// block takes. It fits beside the thumbnail inside Discord's maximum embed
	// width, so the block never decides how wide a card is.
	blockWidth = 40

	// labelWidth is the label column: "Final blow" and a space. Every value
	// starts in the column after it.
	labelWidth = 11

	// valueWidth is what is left of a line for the value.
	valueWidth = blockWidth - labelWidth

	// fence opens and closes a Discord code block.
	fence = "```"
)

// fenceSafe swaps every backtick for U+02CB MODIFIER LETTER GRAVE ACCENT,
// which looks alike but is not markdown, so that no name from the payload can
// close the block's fence early. Text inside the block is otherwise shown
// verbatim.
var fenceSafe = strings.NewReplacer("`", "\u02cb")

// The padding is a run of U+2800 BRAILLE PATTERN BLANK characters. Discord
// does not trim U+2800 as whitespace and draws it blank, so the line is
// invisible but has width, and a run just short of Discord's maximum desktop
// embed width of about 518px draws every card at nearly that width. A run
// over the maximum wraps onto further blank lines, a tall band under the
// card, so the counts aim just under it.
//
// Measured in the Discord desktop client, one blank is about 10px, and the
// text column beside the ship thumbnail is narrower than the full embed: with
// a thumbnail 36 blanks came to 497px and 37 to 507px, without one 46 came to
// 504px and 47 to 514px, and none of them wrapped. 37 and 46 draw both kinds
// of card at about 505px, 3px apart, with about a blank to spare before a
// wrap. The counts are tuned for the default desktop font scale: a larger
// scale wraps the padding onto an extra blank line but keeps the full width,
// and a smaller one draws slightly narrower cards.
const (
	// padRunesThumbnail pads a card that shows the victim ship's render.
	padRunesThumbnail = 37

	// padRunesNoThumbnail pads a card with no thumbnail, whose text runs
	// the full width of the embed.
	padRunesNoThumbnail = 46
)

// padLine ends every description, so that every card, a neutral one included,
// is padded alike; how long it is depends only on whether the card has a
// thumbnail. It goes in the description rather than the footer because
// Discord draws the timestamp on the footer line, after the footer text, which
// padding there would push away.
func padLine(thumbnail bool) string {
	if thumbnail {
		return strings.Repeat("\u2800", padRunesThumbnail)
	}
	return strings.Repeat("\u2800", padRunesNoThumbnail)
}

// block lays the rows out as one code block: each label left-aligned in the
// label column, its value after it, and every further line of the value,
// whether the value has several lines or one too long to fit, indented to the
// value column. Backticks in a value are neutralised (see fenceSafe). No rows
// make no block.
func block(rows []row) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(fence + "\n")
	indent := strings.Repeat(" ", labelWidth)
	for _, r := range rows {
		prefix := fmt.Sprintf("%-*s", labelWidth, r.label)
		for _, line := range strings.Split(fenceSafe.Replace(r.value), "\n") {
			for _, part := range wrap(line, valueWidth) {
				b.WriteString(prefix + part + "\n")
				prefix = indent
			}
		}
	}
	b.WriteString(fence)
	return b.String()
}

// wrap breaks a line into pieces of at most width runes, each at the last
// space that lets it fit, and hard-breaks a word too long to fit on a line of
// its own. The space a line breaks at is dropped.
func wrap(line string, width int) []string {
	var out []string
	r := []rune(line)
	for len(r) > width {
		cut := lastSpace(r[:width+1])
		if cut <= 0 {
			out = append(out, string(r[:width]))
			r = r[width:]
			continue
		}
		out = append(out, string(r[:cut]))
		r = r[cut+1:]
	}
	return append(out, string(r))
}

func lastSpace(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if r[i] == ' ' {
			return i
		}
	}
	return -1
}

// finalBlow names the pilot who landed the last shot as "Pilot (Ship)", with
// their affiliation on the lines below. An NPC final blow usually has a ship and
// no pilot name, in which case the ship stands alone.
func finalBlow(p killmail.Party) string {
	pilot := p.CharName
	if p.ShipName != "" {
		if pilot == "" {
			pilot = p.ShipName
		} else {
			pilot += " (" + p.ShipName + ")"
		}
	}
	return join("\n", pilot, affiliation(p))
}

// affiliation renders a party's corporation and alliance as
// "[TICKER] Corporation" and "<TICKER> Alliance" on a line each, using the
// brackets EVE players see in game. Whichever half, or whichever ticker, the
// payload left out is simply absent.
func affiliation(p killmail.Party) string {
	return join("\n",
		entity(p.CorpTicker, p.CorpName, "[", "]"),
		entity(p.AllianceTicker, p.AllianceName, "<", ">"),
	)
}

func entity(ticker, name, left, right string) string {
	if ticker == "" {
		return name
	}
	return join(" ", left+ticker+right, name)
}

// markdownEscaper backslash-escapes the characters Discord reads as markdown,
// so that an alias like "A_B_" is not italicised in a field.
var markdownEscaper = strings.NewReplacer(
	`\`, `\\`,
	"*", `\*`,
	"_", `\_`,
	"~", `\~`,
	"`", "\\`",
	"|", `\|`,
)

// fieldsSystem is the System field's value: system's, with the alias
// markdown-escaped, since a field is not monospace and shows markdown.
func fieldsSystem(name, alias string) string {
	if name == "" || alias == "" {
		return name
	}
	if r := []rune(alias); len(r) > maxAliasLen {
		alias = string(r[:maxAliasLen])
	}
	return name + " (" + markdownEscaper.Replace(alias) + ")"
}

// fields builds the fields style's inline fields, in the order the design
// lays them out. Fields with nothing to say are left out entirely.
func fields(k killmail.Killmail, systemName string) []*discordgo.MessageEmbedField {
	var out []*discordgo.MessageEmbedField
	add := func(name, value string) {
		if value == "" {
			return
		}
		out = append(out, &discordgo.MessageEmbedField{Name: name, Value: value, Inline: true})
	}

	add("System", systemName)
	// A killmail whose payload omitted total_value decodes to 0 ISK, which
	// says nothing, so the field is dropped rather than claiming a free kill.
	if k.TotalValue > 0 {
		add("Value", FormatISK(k.TotalValue)+" ISK")
	}
	if k.AttackerCount > 0 {
		add("Attackers", strconv.Itoa(k.AttackerCount))
	}
	// The victim's ship and pilot are already in the title, so this field
	// carries their affiliation.
	add("Victim", fieldsAffiliation(k.Victim))
	add("Final blow", fieldsFinalBlow(k.FinalBlow))
	return out
}

// fieldsFinalBlow is finalBlow for the fields style: "Pilot (Ship)" with the
// affiliation on a single second line.
func fieldsFinalBlow(p killmail.Party) string {
	pilot := p.CharName
	if p.ShipName != "" {
		if pilot == "" {
			pilot = p.ShipName
		} else {
			pilot += " (" + p.ShipName + ")"
		}
	}
	return join("\n", pilot, fieldsAffiliation(p))
}

// fieldsAffiliation is affiliation for the fields style, on one line:
// "[TICKER] Corporation / <TICKER> Alliance".
func fieldsAffiliation(p killmail.Party) string {
	return join(" / ",
		entity(p.CorpTicker, p.CorpName, "[", "]"),
		entity(p.AllianceTicker, p.AllianceName, "<", ">"),
	)
}

// iskUnits are the units FormatISK abbreviates to, smallest first. Each is a
// thousand of the one before it, which is what lets FormatISK step up a unit
// by comparing against the next scale.
var iskUnits = []struct {
	suffix string
	scale  float64
}{
	{"K", 1e3},
	{"M", 1e6},
	{"B", 1e9},
	{"T", 1e12},
}

// FormatISK abbreviates an ISK amount the way an EVE player reads it:
// "950K", "412.5M", "1.2B". Amounts below a thousand are written out in full,
// a trailing ".0" is dropped, and an amount that would round up to a thousand
// of its unit moves up to the next one, so it reads "1B" and not "1000M".
func FormatISK(v float64) string {
	abs := math.Abs(v)
	if abs < iskUnits[0].scale {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	unit := 0
	// 999.95 of a unit rounds to "1000.0", which belongs to the next unit up.
	for unit+1 < len(iskUnits) && abs >= iskUnits[unit+1].scale*0.99995 {
		unit++
	}
	rounded := strconv.FormatFloat(v/iskUnits[unit].scale, 'f', 1, 64)
	return strings.TrimSuffix(rounded, ".0") + iskUnits[unit].suffix
}

// join concatenates the parts that are not empty, so a missing part takes its
// separator with it.
func join(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

func orElse(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
