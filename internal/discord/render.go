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

// Render turns one classified killmail into the embed the relay posts.
//
// systemName is the name resolved for k.SolarSystemID, or its decimal ID when
// the name could not be resolved (see internal/universe); an empty systemName
// leaves the System field out.
//
// Render is a pure function of its arguments — posting lives elsewhere in this
// package — so the layout is covered by golden tests. Every field of a
// killmail but its ID is optional (see internal/killmail), so each part of the
// embed is dropped when the payload did not carry it: the embed always
// renders, and never shows an empty field or a dangling separator.
func Render(k killmail.Killmail, c killmail.Classification, systemName string) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		Type:   discordgo.EmbedTypeRich,
		Title:  title(k.Victim),
		URL:    fmt.Sprintf(killURL, k.ID),
		Color:  color(c),
		Fields: fields(k, systemName),
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

// fields builds the inline fields, in the order the design lays them out.
// Fields with nothing to say are left out entirely.
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
	add("Victim", affiliation(k.Victim))
	add("Final blow", finalBlow(k.FinalBlow))
	return out
}

// finalBlow names the pilot who landed the last shot as "Pilot (Ship)", with
// their affiliation on a second line. An NPC final blow usually has a ship and
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
// "[TICKER] Corporation / <TICKER> Alliance", using the brackets EVE players
// see in game. Whichever half, or whichever ticker, the payload left out is
// simply absent.
func affiliation(p killmail.Party) string {
	return join(" / ",
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
