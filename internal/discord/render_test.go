package discord_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Vapor-Lock/chainsniffer/internal/discord"
	"github.com/Vapor-Lock/chainsniffer/internal/killmail"
)

var update = flag.Bool("update", false, "rewrite the golden embeds in testdata")

func killTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing the kill time %q: %v", s, err)
	}
	return ts
}

// typicalKill is the kill from the discord-killmail-posting spec's "Typical
// kill" scenario: a Stratios flown by Pilot A [CORP], final blow by Pilot B in
// a Loki, 7 attackers, 412,500,000 ISK.
func typicalKill(t *testing.T) killmail.Killmail {
	t.Helper()
	return killmail.Killmail{
		ID:            123456789,
		KillTime:      killTime(t, "2026-10-13T18:42:07Z"),
		SolarSystemID: 31000005,
		Victim: killmail.Party{
			CharID: 95000001, CharName: "Pilot A",
			CorpID: 98123456, CorpTicker: "CORP", CorpName: "Victim Corporation",
			AllianceID: 99123456, AllianceTicker: "VCTM", AllianceName: "Victim Alliance",
			ShipTypeID: 33470, ShipName: "Stratios",
		},
		FinalBlow: killmail.Party{
			CharID: 95000002, CharName: "Pilot B",
			CorpID: 98000001, CorpTicker: "FRND", CorpName: "Friendly Corporation",
			AllianceID: 99000001, AllianceTicker: "BLUE", AllianceName: "Friendly Alliance",
			ShipTypeID: 29990, ShipName: "Loki",
		},
		AttackerCount: 7,
		TotalValue:    412_500_000,
	}
}

// Render's layout is pinned by golden embeds in testdata; regenerate them with
// `go test ./internal/discord -update` after a deliberate change.
func TestRenderGolden(t *testing.T) {
	tests := []struct {
		name       string
		golden     string
		kill       killmail.Killmail
		class      killmail.Classification
		systemName string
	}{
		{
			name:       "typical kill",
			golden:     "render_kill.json",
			kill:       typicalKill(t),
			class:      killmail.Kill,
			systemName: "Thera",
		},
		{
			// A friendly loss: the same shape, classified the other way, so
			// the golden embeds differ only in colour and footer.
			name:       "friendly loss",
			golden:     "render_loss.json",
			kill:       typicalKill(t),
			class:      killmail.Loss,
			systemName: "J123456",
		},
		{
			// A structure has no victim character and no alliance, and NPCs
			// land the final blow, so the title falls back to the owning
			// corporation and the final blow shows only a ship.
			name:   "structure kill",
			golden: "render_structure.json",
			kill: killmail.Killmail{
				ID:            987654321,
				KillTime:      killTime(t, "2026-10-13T04:05:06Z"),
				SolarSystemID: 31002604,
				Victim: killmail.Party{
					CorpID: 98000001, CorpTicker: "FRND", CorpName: "Friendly Corporation",
					ShipTypeID: 35832, ShipName: "Astrahus",
				},
				FinalBlow:     killmail.Party{ShipTypeID: 30447, ShipName: "Guristas Despoiler"},
				AttackerCount: 3,
				TotalValue:    1_240_000_000,
				NPC:           true,
			},
			class:      killmail.Loss,
			systemName: "J100001",
		},
		{
			// Everything optional is missing, which is all Wanderer
			// guarantees: the embed still has a title, a link and a colour.
			name:       "minimal fields",
			golden:     "render_minimal.json",
			kill:       killmail.Killmail{ID: 42},
			class:      killmail.Neutral,
			systemName: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.MarshalIndent(discord.Render(tc.kill, tc.class, tc.systemName), "", "  ")
			if err != nil {
				t.Fatalf("marshalling the embed: %v", err)
			}
			got = append(got, '\n')

			path := filepath.Join("testdata", tc.golden)
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("writing the golden embed: %v", err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the golden embed (run `go test ./internal/discord -update` to create it): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("embed does not match %s\ngot:\n%s\nwant:\n%s", tc.golden, got, want)
			}
		})
	}
}

// "Typical kill": the title names the ship and the pilot, and the fields carry
// everything the requirement lists.
func TestRenderTypicalKillContent(t *testing.T) {
	embed := discord.Render(typicalKill(t), killmail.Kill, "J123456")

	if got, want := embed.Title, "Stratios — Pilot A"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := embed.Timestamp, "2026-10-13T18:42:07Z"; got != want {
		t.Errorf("Timestamp = %q, want the kill time %q", got, want)
	}

	fields := map[string]string{}
	for _, f := range embed.Fields {
		if !f.Inline {
			t.Errorf("field %q is not inline", f.Name)
		}
		fields[f.Name] = f.Value
	}
	want := map[string]string{
		"System":     "J123456",
		"Value":      "412.5M ISK",
		"Attackers":  "7",
		"Victim":     "[CORP] Victim Corporation / <VCTM> Victim Alliance",
		"Final blow": "Pilot B (Loki)\n[FRND] Friendly Corporation / <BLUE> Friendly Alliance",
	}
	for name, value := range want {
		if got := fields[name]; got != value {
			t.Errorf("field %q = %q, want %q", name, got, value)
		}
	}
	if len(fields) != len(want) {
		t.Errorf("fields = %v, want exactly %d of them", fields, len(want))
	}
}

// "zKillboard link" and "Links and images": the title links to the kill and
// the thumbnail is the victim ship's render.
func TestRenderLinksAndImages(t *testing.T) {
	embed := discord.Render(typicalKill(t), killmail.Kill, "Thera")

	if got, want := embed.URL, "https://zkillboard.com/kill/123456789/"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	if embed.Thumbnail == nil {
		t.Fatal("Thumbnail is nil, want the victim ship's render")
	}
	if got, want := embed.Thumbnail.URL, "https://images.evetech.net/types/33470/render?size=128"; got != want {
		t.Errorf("Thumbnail.URL = %q, want %q", got, want)
	}

	// An unknown ship type has no render to show.
	k := typicalKill(t)
	k.Victim.ShipTypeID = 0
	if thumb := discord.Render(k, killmail.Kill, "Thera").Thumbnail; thumb != nil {
		t.Errorf("Thumbnail = %+v, want nil when the ship type ID is unknown", thumb)
	}
}

// "Classification styling": colour per classification, with a footer naming it
// for a loss and a kill only.
func TestRenderClassificationStyling(t *testing.T) {
	tests := []struct {
		class      killmail.Classification
		wantColor  int
		wantFooter string
	}{
		{class: killmail.Loss, wantColor: discord.ColorLoss, wantFooter: "Loss"},
		{class: killmail.Kill, wantColor: discord.ColorKill, wantFooter: "Kill"},
		{class: killmail.Neutral, wantColor: discord.ColorNeutral},
	}
	for _, tc := range tests {
		t.Run(string(tc.class), func(t *testing.T) {
			embed := discord.Render(typicalKill(t), tc.class, "Thera")
			if embed.Color != tc.wantColor {
				t.Errorf("Color = %#x, want %#x", embed.Color, tc.wantColor)
			}
			switch {
			case tc.wantFooter == "":
				if embed.Footer != nil {
					t.Errorf("Footer = %+v, want nil for a neutral kill", embed.Footer)
				}
			case embed.Footer == nil:
				t.Errorf("Footer is nil, want %q", tc.wantFooter)
			case embed.Footer.Text != tc.wantFooter:
				t.Errorf("Footer.Text = %q, want %q", embed.Footer.Text, tc.wantFooter)
			}
		})
	}
}

// "Missing optional fields": whatever the payload left out is dropped, and the
// embed is still postable.
func TestRenderDegradesGracefully(t *testing.T) {
	embed := discord.Render(killmail.Killmail{ID: 42}, killmail.Neutral, "")

	if got, want := embed.Title, "Unknown ship — Unknown pilot"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := embed.URL, "https://zkillboard.com/kill/42/"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	if len(embed.Fields) != 0 {
		t.Errorf("Fields = %+v, want none when the payload carried nothing", embed.Fields)
	}
	if embed.Timestamp != "" {
		t.Errorf("Timestamp = %q, want empty when kill_time is unknown", embed.Timestamp)
	}
	if embed.Thumbnail != nil {
		t.Errorf("Thumbnail = %+v, want nil", embed.Thumbnail)
	}
}

// Partial affiliations must not leave a stray bracket or separator behind.
func TestRenderPartialAffiliations(t *testing.T) {
	tests := []struct {
		name            string
		victim          killmail.Party
		finalBlow       killmail.Party
		wantVictim      string
		wantFinalBlow   string
		wantTitlePrefix string
	}{
		{
			name:            "no alliance",
			victim:          killmail.Party{CharName: "Pilot A", CorpTicker: "CORP", CorpName: "Victim Corporation", ShipName: "Stratios"},
			finalBlow:       killmail.Party{CharName: "Pilot B", CorpName: "Friendly Corporation", ShipName: "Loki"},
			wantVictim:      "[CORP] Victim Corporation",
			wantFinalBlow:   "Pilot B (Loki)\nFriendly Corporation",
			wantTitlePrefix: "Stratios — Pilot A",
		},
		{
			name:            "tickers only",
			victim:          killmail.Party{CharName: "Pilot A", CorpTicker: "CORP", AllianceTicker: "VCTM", ShipName: "Stratios"},
			finalBlow:       killmail.Party{AllianceTicker: "BLUE"},
			wantVictim:      "[CORP] / <VCTM>",
			wantFinalBlow:   "<BLUE>",
			wantTitlePrefix: "Stratios — Pilot A",
		},
		{
			name:            "no final blow at all",
			victim:          killmail.Party{CorpName: "Victim Corporation", ShipName: "Astrahus"},
			wantVictim:      "Victim Corporation",
			wantTitlePrefix: "Astrahus — Victim Corporation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k := killmail.Killmail{ID: 7, Victim: tc.victim, FinalBlow: tc.finalBlow}
			embed := discord.Render(k, killmail.Neutral, "Thera")
			if got := embed.Title; got != tc.wantTitlePrefix {
				t.Errorf("Title = %q, want %q", got, tc.wantTitlePrefix)
			}
			fields := map[string]string{}
			for _, f := range embed.Fields {
				fields[f.Name] = f.Value
			}
			if got := fields["Victim"]; got != tc.wantVictim {
				t.Errorf("Victim field = %q, want %q", got, tc.wantVictim)
			}
			if got := fields["Final blow"]; got != tc.wantFinalBlow {
				t.Errorf("Final blow field = %q, want %q", got, tc.wantFinalBlow)
			}
			for name, value := range fields {
				if strings.Contains(value, " / ") && !strings.Contains(tc.wantVictim+tc.wantFinalBlow, " / ") {
					t.Errorf("field %q = %q, want no dangling separator", name, value)
				}
			}
		})
	}
}

func TestFormatISK(t *testing.T) {
	tests := []struct {
		value float64
		want  string
	}{
		{value: 0, want: "0"},
		{value: 1, want: "1"},
		{value: 750, want: "750"},
		{value: 999, want: "999"},
		{value: 1_000, want: "1K"},
		{value: 950_000, want: "950K"},
		{value: 1_500, want: "1.5K"},
		{value: 1_000_000, want: "1M"},
		{value: 412_500_000, want: "412.5M"},
		{value: 1_200_000_000, want: "1.2B"},
		{value: 999_990_000, want: "1B"},
		{value: 2_500_000_000_000, want: "2.5T"},
		{value: 412.75, want: "412.75"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if got := discord.FormatISK(tc.value); got != tc.want {
				t.Errorf("FormatISK(%v) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}
