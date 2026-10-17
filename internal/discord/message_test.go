package discord_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Vapor-Lock/chainsniffer/internal/discord"
)

// The Components V2 wire format is pinned by a golden layout using every
// component type; regenerate it with `go test ./internal/discord -update`.
func TestComponentsMarshal(t *testing.T) {
	layout := []discord.Component{
		discord.Container(0x2ECC71,
			discord.Section(discord.Thumbnail("https://images.evetech.net/types/33470/render?size=128"),
				discord.TextDisplay("### [Stratios — Pilot A](https://zkillboard.com/kill/123456789/)")),
			discord.TextDisplay("```\nSystem  J123456\n```"),
			discord.Separator(),
			discord.TextDisplay("-# Kill · <t:1792003327:f>"),
		),
	}

	got, err := json.MarshalIndent(layout, "", "  ")
	if err != nil {
		t.Fatalf("marshalling the components: %v", err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "components.json")
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
		t.Errorf("components do not match %s\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}

// A zero accent colour is black, not "no colour": it must still be sent.
func TestContainerKeepsZeroAccent(t *testing.T) {
	got, err := json.Marshal(discord.Container(0))
	if err != nil {
		t.Fatalf("marshalling the container: %v", err)
	}
	if want := `{"type":17,"accent_color":0}`; string(got) != want {
		t.Errorf("Container(0) = %s, want %s", got, want)
	}
}
