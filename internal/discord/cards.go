package discord

import (
	"fmt"
	"strconv"

	"github.com/Vapor-Lock/chainsniffer/internal/killmail"
	"github.com/bwmarrin/discordgo"
)

// The EVE image server's portraits and logos, which the components card shows
// beside each party. 128px is the largest size Discord shows a thumbnail at.
const (
	portraitURL        = "https://images.evetech.net/characters/%d/portrait?size=128"
	corporationLogoURL = "https://images.evetech.net/corporations/%d/logo?size=128"
	allianceLogoURL    = "https://images.evetech.net/alliances/%d/logo?size=128"
)

// The strip style's attachment: the PNG cardimage draws, which the embed shows
// as its image under the monospace block.
const (
	stripFileName    = "kill.png"
	stripContentType = "image/png"
)

// StripMessage returns the strip style's card: embed, the block style's card,
// showing png as its image. It sets embed's image to the attachment, so the
// embed is the message's and the caller should not reuse it.
func StripMessage(embed *discordgo.MessageEmbed, png []byte) Message {
	embed.Image = &discordgo.MessageEmbedImage{URL: "attachment://" + stripFileName}
	return Message{
		Embed: embed,
		Files: []File{{Name: stripFileName, ContentType: stripContentType, Data: png}},
	}
}

// ComponentsMessage returns the components style's card, a Components V2
// layout in one container of the classification colour: the title linked to
// zKillboard beside the ship render, the block style's monospace block, a
// separator, a section each for the victim and the final blow, and a footer
// with the classification and the kill time.
//
// systemName and alias are as for Render. The monospace block is the one
// Render builds, without the padding line: a container's width is Discord's,
// not its text's. Like Render, ComponentsMessage is a pure function, and every
// part the payload gave nothing for is left out.
func ComponentsMessage(k killmail.Killmail, c killmail.Classification, systemName, alias string) Message {
	header := TextDisplay("### [" + markdownEscaper.Replace(title(k.Victim)) + "](" + fmt.Sprintf(killURL, k.ID) + ")")
	if k.Victim.ShipTypeID > 0 {
		header = Section(Thumbnail(fmt.Sprintf(shipRenderURL, k.Victim.ShipTypeID)), header)
	}

	children := []Component{header}
	if b := block(rows(k, system(systemName, alias))); b != "" {
		children = append(children, TextDisplay(b))
	}
	children = append(children,
		Separator(),
		party(victimName(k.Victim), k.Victim),
		party(orElse(k.FinalBlow.CharName, k.FinalBlow.ShipName), k.FinalBlow),
	)
	if text := componentsFooter(c, k); text != "" {
		children = append(children, TextDisplay(text))
	}
	return Message{Components: []Component{Container(color(c), children...)}}
}

// victimName is who title names: the character, else the owning corporation
// of a structure or an NPC-owned wreck.
func victimName(p killmail.Party) string {
	return orElse(orElse(p.CharName, p.CorpName), unknownPilot)
}

// party is a victim or final-blow section: the name in bold, the hull, and
// the "[CORP] / <ALLY>" tickers, each on a line of its own and left out when
// there is nothing to say, beside the party's image (see partyImage). A party
// with no image is a plain text display, since a section must have an
// accessory.
func party(name string, p killmail.Party) Component {
	text := TextDisplay(join("\n",
		bold(name),
		markdownEscaper.Replace(orElse(p.ShipName, unknownShip)),
		markdownEscaper.Replace(tickers(p)),
	))
	if url := partyImage(p); url != "" {
		return Section(Thumbnail(url), text)
	}
	return text
}

func bold(s string) string {
	if s == "" {
		return ""
	}
	return "**" + markdownEscaper.Replace(s) + "**"
}

// tickers reads "[CORP] / <ALLY>", in the brackets EVE players see in game. A
// corporation or an alliance with a name but no ticker shows its name instead.
func tickers(p killmail.Party) string {
	return join(" / ",
		bracket(orElse(p.CorpTicker, p.CorpName), "[", "]"),
		bracket(orElse(p.AllianceTicker, p.AllianceName), "<", ">"),
	)
}

func bracket(s, left, right string) string {
	if s == "" {
		return ""
	}
	return left + s + right
}

// partyImage picks the one image a section shows: the alliance logo, else the
// corporation logo, else the character portrait, else none.
func partyImage(p killmail.Party) string {
	switch {
	case p.AllianceID > 0:
		return fmt.Sprintf(allianceLogoURL, p.AllianceID)
	case p.CorpID > 0:
		return fmt.Sprintf(corporationLogoURL, p.CorpID)
	case p.CharID > 0:
		return fmt.Sprintf(portraitURL, p.CharID)
	default:
		return ""
	}
}

// componentsFooter is the footer line, in Discord's small text: the
// classification, as Render's footer states it, and the kill time as a
// timestamp, which Discord shows in the reader's own time zone. A neutral kill
// has only the time, and a kill without a time only the classification.
func componentsFooter(c killmail.Classification, k killmail.Killmail) string {
	var when string
	if !k.KillTime.IsZero() {
		when = "<t:" + strconv.FormatInt(k.KillTime.Unix(), 10) + ":f>"
	}
	text := join(" · ", footer(c), when)
	if text == "" {
		return ""
	}
	return "-# " + text
}
