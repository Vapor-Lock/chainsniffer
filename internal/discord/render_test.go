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

// blockRow is one labelled row of the embed's monospace block, with the lines
// of its value, continuation lines included.
type blockRow struct {
	label string
	lines []string
}

// blockRows parses the embed description's code block back into rows,
// checking the layout on the way: a fenced block, no line over 40 runes, every
// label in the 11-column label column and every value starting in column 12.
func blockRows(t *testing.T, description string) []blockRow {
	t.Helper()
	if description == "" {
		return nil
	}
	lines := strings.Split(description, "\n")
	if len(lines) < 2 || lines[0] != "```" || lines[len(lines)-1] != "```" {
		t.Fatalf("description is not one code block:\n%s", description)
	}
	var rows []blockRow
	for _, line := range lines[1 : len(lines)-1] {
		r := []rune(line)
		if len(r) > 40 {
			t.Errorf("block line %q is %d runes, want at most 40", line, len(r))
		}
		if len(r) <= 11 || r[10] != ' ' || r[11] == ' ' {
			t.Fatalf("block line %q does not have its value in column 12", line)
		}
		label, value := strings.TrimSpace(string(r[:11])), string(r[11:])
		if label == "" {
			if len(rows) == 0 {
				t.Fatalf("block starts with a continuation line %q", line)
			}
			rows[len(rows)-1].lines = append(rows[len(rows)-1].lines, value)
			continue
		}
		rows = append(rows, blockRow{label: label, lines: []string{value}})
	}
	return rows
}

// rowValues maps each row's label to its value lines joined by newlines.
func rowValues(t *testing.T, description string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, r := range blockRows(t, description) {
		out[r.label] = strings.Join(r.lines, "\n")
	}
	return out
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
		alias      string
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
			// "Kill in a named system": the map's temporary name follows the
			// system name in the System row.
			name:       "chain alias",
			golden:     "render_alias.json",
			kill:       typicalKill(t),
			class:      killmail.Kill,
			systemName: "J134349",
			alias:      "HOTEL",
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
			got, err := json.MarshalIndent(discord.Render(tc.kill, tc.class, tc.systemName, tc.alias), "", "  ")
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

// "Typical kill" and "Aligned rows": the title names the ship and the pilot,
// and the block's rows carry everything the requirement lists, in order.
func TestRenderTypicalKillContent(t *testing.T) {
	embed := discord.Render(typicalKill(t), killmail.Kill, "J123456", "")

	if got, want := embed.Title, "Stratios — Pilot A"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := embed.Timestamp, "2026-10-13T18:42:07Z"; got != want {
		t.Errorf("Timestamp = %q, want the kill time %q", got, want)
	}

	if len(embed.Fields) != 0 {
		t.Errorf("Fields = %+v, want none: the details belong in the description", embed.Fields)
	}

	want := []blockRow{
		{"System", []string{"J123456"}},
		{"Value", []string{"412.5M ISK"}},
		{"Attackers", []string{"7"}},
		{"Victim", []string{"[CORP] Victim Corporation", "<VCTM> Victim Alliance"}},
		{"Final blow", []string{"Pilot B (Loki)", "[FRND] Friendly Corporation", "<BLUE> Friendly Alliance"}},
	}
	got := blockRows(t, embed.Description)
	if len(got) != len(want) {
		t.Fatalf("block rows = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].label != want[i].label || strings.Join(got[i].lines, "\n") != strings.Join(want[i].lines, "\n") {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// "Row with nothing to show": a row without a value is dropped, label and all.
func TestRenderDropsEmptyRows(t *testing.T) {
	k := typicalKill(t)
	k.TotalValue = 0
	k.AttackerCount = 0
	var labels []string
	for _, r := range blockRows(t, discord.Render(k, killmail.Kill, "J123456", "").Description) {
		labels = append(labels, r.label)
	}
	if got, want := strings.Join(labels, ","), "System,Victim,Final blow"; got != want {
		t.Errorf("row labels = %s, want %s", got, want)
	}
}

// "Long value wraps": a value too long for the value column continues on lines
// indented to it, and no line of the block exceeds 40 columns.
func TestRenderWrapsLongValues(t *testing.T) {
	tests := []struct {
		name     string
		corpName string
		want     []string
	}{
		{
			name:     "breaks at the last space that fits",
			corpName: "The Exceedingly Long Corporation Name of Doom",
			want:     []string{"[CORP] The Exceedingly Long", "Corporation Name of Doom", "<VCTM> Victim Alliance"},
		},
		{
			name:     "hard-breaks a word longer than the column",
			corpName: strings.Repeat("x", 40),
			want:     []string{"[CORP]", strings.Repeat("x", 29), strings.Repeat("x", 11), "<VCTM> Victim Alliance"},
		},
		{
			name:     "fits exactly",
			corpName: strings.Repeat("y", 22),
			want:     []string{"[CORP] " + strings.Repeat("y", 22), "<VCTM> Victim Alliance"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k := typicalKill(t)
			k.Victim.CorpName = tc.corpName
			embed := discord.Render(k, killmail.Kill, "J123456", "")
			// blockRows fails the test on any line over 40 runes or with its
			// value outside column 12.
			if got, want := rowValues(t, embed.Description)["Victim"], strings.Join(tc.want, "\n"); got != want {
				t.Errorf("Victim row = %q, want %q", got, want)
			}
		})
	}
}

// "zKillboard link" and "Links and images": the title links to the kill and
// the thumbnail is the victim ship's render.
func TestRenderLinksAndImages(t *testing.T) {
	embed := discord.Render(typicalKill(t), killmail.Kill, "Thera", "")

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
	if thumb := discord.Render(k, killmail.Kill, "Thera", "").Thumbnail; thumb != nil {
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
			embed := discord.Render(typicalKill(t), tc.class, "Thera", "")
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
	embed := discord.Render(killmail.Killmail{ID: 42}, killmail.Neutral, "", "")

	if got, want := embed.Title, "Unknown ship — Unknown pilot"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := embed.URL, "https://zkillboard.com/kill/42/"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	if embed.Description != "" {
		t.Errorf("Description = %q, want no block when the payload carried nothing", embed.Description)
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
			wantVictim:      "[CORP]\n<VCTM>",
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
			embed := discord.Render(k, killmail.Neutral, "Thera", "")
			if got := embed.Title; got != tc.wantTitlePrefix {
				t.Errorf("Title = %q, want %q", got, tc.wantTitlePrefix)
			}
			rows := rowValues(t, embed.Description)
			if got := rows["Victim"]; got != tc.wantVictim {
				t.Errorf("Victim row = %q, want %q", got, tc.wantVictim)
			}
			if got := rows["Final blow"]; got != tc.wantFinalBlow {
				t.Errorf("Final blow row = %q, want %q", got, tc.wantFinalBlow)
			}
		})
	}
}

// systemRow renders the typical kill with the given name and alias and
// returns the System row's value, its lines joined by newlines.
func systemRow(t *testing.T, systemName, alias string) string {
	t.Helper()
	for _, r := range blockRows(t, discord.Render(typicalKill(t), killmail.Kill, systemName, alias).Description) {
		if r.label == "System" {
			return strings.Join(r.lines, "\n")
		}
	}
	t.Fatalf("no System row for name %q and alias %q", systemName, alias)
	return ""
}

// "Chain alias": the alias follows the name in parentheses, is escaped for
// Discord markdown and capped, and an empty alias leaves the name alone.
func TestRenderChainAlias(t *testing.T) {
	long := strings.Repeat("a", 300)
	tests := []struct {
		name       string
		systemName string
		alias      string
		want       string
	}{
		{name: "named system", systemName: "J134349", alias: "HOTEL", want: "J134349 (HOTEL)"},
		{name: "name not resolved", systemName: "31001234", alias: "HOTEL", want: "31001234 (HOTEL)"},
		{name: "no alias", systemName: "J134349", alias: "", want: "J134349"},
		{name: "markdown escaped", systemName: "J134349", alias: "A_B_", want: `J134349 (A\_B\_)`},
		{name: "every markdown character", systemName: "J134349", alias: "\\*_~`|", want: "J134349 (\\\\\\*\\_\\~\\`\\|)"},
		// The capped alias is still too long for one line, so it wraps
		// after the name and then breaks hard every 29 columns.
		{name: "capped", systemName: "J134349", alias: long, want: "J134349\n(" + long[:28] + "\n" + long[:29] + "\n" + long[:29] + "\n" + long[:14] + ")"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := systemRow(t, tc.systemName, tc.alias); got != tc.want {
				t.Errorf("System row = %q, want %q", got, tc.want)
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
