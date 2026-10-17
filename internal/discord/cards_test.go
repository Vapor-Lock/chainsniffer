package discord_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vapor-Lock/chainsniffer/internal/discord"
	"github.com/Vapor-Lock/chainsniffer/internal/killmail"
)

// ComponentsMessage's layout is pinned by golden component lists in testdata,
// named components_<case>.json; regenerate them with
// `go test ./internal/discord -update` after a deliberate change.
func TestComponentsMessageGolden(t *testing.T) {
	noAlliance := typicalKill(t)
	noAlliance.Victim.AllianceID, noAlliance.Victim.AllianceTicker, noAlliance.Victim.AllianceName = 0, "", ""

	noCorporation := typicalKill(t)
	noCorporation.FinalBlow.CorpID, noCorporation.FinalBlow.CorpTicker, noCorporation.FinalBlow.CorpName = 0, "", ""
	noCorporation.FinalBlow.AllianceID, noCorporation.FinalBlow.AllianceTicker, noCorporation.FinalBlow.AllianceName = 0, "", ""

	cases := []struct {
		name   string
		golden string
		kill   killmail.Killmail
		class  killmail.Classification
		system string
		alias  string
	}{
		{
			name:   "typical kill",
			golden: "components_kill.json",
			kill:   typicalKill(t),
			class:  killmail.Kill,
			system: "J134349",
			alias:  "HOTEL",
		},
		{
			// "Corporation logo when there is no alliance".
			name:   "victim without an alliance",
			golden: "components_no_alliance.json",
			kill:   noAlliance,
			class:  killmail.Loss,
			system: "J123456",
		},
		{
			// "Portrait when there is no corporation".
			name:   "final blow without a corporation",
			golden: "components_no_corporation.json",
			kill:   noCorporation,
			class:  killmail.Kill,
			system: "J123456",
		},
		{
			// "Nothing to show": no IDs anywhere, so no thumbnails, no block,
			// and a footer without a time.
			name:   "no IDs",
			golden: "components_no_ids.json",
			kill:   killmail.Killmail{ID: 42},
			class:  killmail.Kill,
		},
		{
			// A neutral kill's footer has only the time.
			name:   "neutral",
			golden: "components_neutral.json",
			kill:   typicalKill(t),
			class:  killmail.Neutral,
			system: "J123456",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := discord.ComponentsMessage(tc.kill, tc.class, tc.system, tc.alias)
			if msg.Embed != nil || len(msg.Files) > 0 {
				t.Errorf("ComponentsMessage carries an embed or files: %+v", msg)
			}
			testComponentsGolden(t, tc.golden, msg.Components)
		})
	}
}

func testComponentsGolden(t *testing.T, golden string, components []discord.Component) {
	t.Helper()
	got, err := json.MarshalIndent(components, "", "  ")
	if err != nil {
		t.Fatalf("marshalling the components: %v", err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", golden)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("writing the golden components: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the golden components (run `go test ./internal/discord -update` to create it): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("components do not match %s\ngot:\n%s\nwant:\n%s", golden, got, want)
	}
}

// The container's children: header, block, separator, victim, final blow,
// footer, in that order.
func componentsChildren(t *testing.T, msg discord.Message) []discord.Component {
	t.Helper()
	if len(msg.Components) != 1 || msg.Components[0].Type != discord.ComponentContainer {
		t.Fatalf("Components = %+v, want one container", msg.Components)
	}
	return msg.Components[0].Components
}

// The section images follow the spec's preference: alliance logo, else
// corporation logo, else portrait.
func TestComponentsMessagePartyImages(t *testing.T) {
	k := typicalKill(t)
	k.Victim.AllianceID = 0
	children := componentsChildren(t, discord.ComponentsMessage(k, killmail.Kill, "J123456", ""))
	victim, finalBlow := children[3], children[4]

	if got, want := victim.Accessory.Media.URL, "https://images.evetech.net/corporations/98123456/logo?size=128"; got != want {
		t.Errorf("victim image = %q, want the corporation logo %q", got, want)
	}
	if got, want := finalBlow.Accessory.Media.URL, "https://images.evetech.net/alliances/99000001/logo?size=128"; got != want {
		t.Errorf("final blow image = %q, want the alliance logo %q", got, want)
	}

	k.FinalBlow.CorpID, k.FinalBlow.AllianceID = 0, 0
	children = componentsChildren(t, discord.ComponentsMessage(k, killmail.Kill, "J123456", ""))
	if got, want := children[4].Accessory.Media.URL, "https://images.evetech.net/characters/95000002/portrait?size=128"; got != want {
		t.Errorf("final blow image = %q, want the portrait %q", got, want)
	}
}

// The text display holds the block style's monospace block, without the
// padding line.
func TestComponentsMessageBlock(t *testing.T) {
	k := typicalKill(t)
	children := componentsChildren(t, discord.ComponentsMessage(k, killmail.Kill, "J123456", ""))
	description := discord.Render(k, killmail.Kill, "J123456", "", discord.CardStyleBlock).Description
	want := description[:strings.LastIndex(description, "\n")]
	if got := children[1].Content; got != want {
		t.Errorf("block = %q, want the block style's %q", got, want)
	}
}

// Structure and NPC kills: the victim falls back to the corporation, the
// attacker to the ship.
func TestComponentsMessageNameFallbacks(t *testing.T) {
	k := killmail.Killmail{
		ID:        987654321,
		Victim:    killmail.Party{CorpID: 98000001, CorpTicker: "FRND", CorpName: "Friendly Corporation", ShipName: "Astrahus"},
		FinalBlow: killmail.Party{ShipName: "Guristas Despoiler"},
	}
	children := componentsChildren(t, discord.ComponentsMessage(k, killmail.Loss, "", ""))
	if got, want := children[3].Components[0].Content, "**Friendly Corporation**\nAstrahus\n[FRND]"; got != want {
		t.Errorf("victim text = %q, want %q", got, want)
	}
	if got, want := children[4], discord.TextDisplay("**Guristas Despoiler**\nGuristas Despoiler"); got.Type != want.Type || got.Content != want.Content {
		t.Errorf("final blow = %+v, want a plain text display %q", got, want.Content)
	}
}

func TestStripMessage(t *testing.T) {
	png := []byte("\x89PNG fake")
	embed := discord.Render(typicalKill(t), killmail.Kill, "J123456", "", discord.CardStyleBlock)
	msg := discord.StripMessage(embed, png)

	if msg.Embed != embed {
		t.Errorf("Embed = %p, want the block embed %p", msg.Embed, embed)
	}
	if embed.Image == nil || embed.Image.URL != "attachment://kill.png" {
		t.Errorf("embed image = %+v, want attachment://kill.png", embed.Image)
	}
	if len(msg.Components) != 0 {
		t.Errorf("Components = %+v, want none", msg.Components)
	}
	if len(msg.Files) != 1 {
		t.Fatalf("Files = %+v, want the one PNG", msg.Files)
	}
	f := msg.Files[0]
	if f.Name != "kill.png" || f.ContentType != "image/png" || !bytes.Equal(f.Data, png) {
		t.Errorf("file = %s %s %q, want kill.png image/png %q", f.Name, f.ContentType, f.Data, png)
	}
}
