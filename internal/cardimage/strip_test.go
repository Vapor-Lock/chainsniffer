package cardimage_test

import (
	"bytes"
	"flag"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Vapor-Lock/chainsniffer/internal/cardimage"
)

var update = flag.Bool("update", false, "rewrite the golden strips in testdata")

// fixture stands in for an image from the EVE image server: a size×size
// diagonal gradient between two colours, with a light frame, so that scaling,
// placement and the tiles' edges all show in the golden strips.
func fixture(size int, from, to color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			if x < size/16 || y < size/16 || x >= size-size/16 || y >= size-size/16 {
				img.SetRGBA(x, y, color.RGBA{0xE0, 0xE0, 0xE0, 0xFF})
				continue
			}
			t := float64(x+y) / float64(2*size)
			img.SetRGBA(x, y, color.RGBA{
				R: lerp(from.R, to.R, t),
				G: lerp(from.G, to.G, t),
				B: lerp(from.B, to.B, t),
				A: 0xFF,
			})
		}
	}
	return img
}

func lerp(a, b uint8, t float64) uint8 {
	return uint8(float64(a) + (float64(b)-float64(a))*t)
}

// The image server's sizes: portraits are fetched larger than logos, so both
// are scaled, down to their tiles.
var (
	portraitA = fixture(256, color.RGBA{0x80, 0x20, 0x20, 0xFF}, color.RGBA{0xF0, 0xA0, 0x40, 0xFF})
	portraitB = fixture(256, color.RGBA{0x20, 0x40, 0x80, 0xFF}, color.RGBA{0x40, 0xC0, 0xF0, 0xFF})
	corpLogo  = fixture(128, color.RGBA{0x20, 0x80, 0x30, 0xFF}, color.RGBA{0xA0, 0xF0, 0x60, 0xFF})
	allyLogo  = fixture(128, color.RGBA{0x60, 0x20, 0x80, 0xFF}, color.RGBA{0xD0, 0x80, 0xF0, 0xFF})
)

// Strip's output is pinned by golden PNGs in testdata; regenerate them with
// `go test ./internal/cardimage -update` after a deliberate change, and look
// at them before committing.
func TestStripGolden(t *testing.T) {
	tests := []struct {
		name   string
		golden string
		in     cardimage.StripInput
	}{
		{
			name:   "typical kill",
			golden: "strip_kill.png",
			in: cardimage.StripInput{
				Victim:   cardimage.Party{Name: "Pilot A", Hull: "Stratios", Portrait: portraitA, Corp: corpLogo, Alliance: allyLogo},
				Attacker: cardimage.Party{Name: "Pilot B", Hull: "Loki", Portrait: portraitB, Corp: corpLogo, Alliance: allyLogo},
			},
		},
		{
			// "Victim without an alliance": only that tile is blank.
			name:   "missing alliance",
			golden: "strip_no_alliance.png",
			in: cardimage.StripInput{
				Victim:   cardimage.Party{Name: "Pilot A", Hull: "Stratios", Portrait: portraitA, Corp: corpLogo},
				Attacker: cardimage.Party{Name: "Pilot B", Hull: "Loki", Portrait: portraitB, Corp: corpLogo, Alliance: allyLogo},
			},
		},
		{
			// "Structure or NPC kill": the caller's fallbacks name the
			// owning corporation and the NPC ship, and the portraits and
			// the NPC's logos are blank.
			name:   "no character",
			golden: "strip_no_character.png",
			in: cardimage.StripInput{
				Victim:   cardimage.Party{Name: "Friendly Corporation", Hull: "Astrahus", Corp: corpLogo},
				Attacker: cardimage.Party{Name: "Guristas Despoiler", Hull: "Guristas Despoiler"},
			},
		},
		{
			// Names and hulls too wide for their half are cut with an
			// ellipsis and never cross the middle.
			name:   "long names",
			golden: "strip_long_names.png",
			in: cardimage.StripInput{
				Victim: cardimage.Party{
					Name: "Captain Longname of the Very Long Corporation", Hull: "Revelation Navy Issue Extended",
					Portrait: portraitA, Corp: corpLogo, Alliance: allyLogo,
				},
				Attacker: cardimage.Party{
					Name: "Another Exceedingly Long Character Name", Hull: "Vindicator Abyssal Edition",
					Portrait: portraitB, Corp: corpLogo, Alliance: allyLogo,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cardimage.Strip(tc.in)
			if err != nil {
				t.Fatalf("Strip: %v", err)
			}

			path := filepath.Join("testdata", tc.golden)
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("writing the golden strip: %v", err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the golden strip (run `go test ./internal/cardimage -update` to create it): %v", err)
			}

			// Compare pixels rather than bytes, so a change in the PNG
			// encoder's compression does not fail the test.
			gotImg, wantImg := decode(t, got), decode(t, want)
			if gotImg.Bounds() != wantImg.Bounds() {
				t.Fatalf("strip is %v, %s is %v", gotImg.Bounds(), tc.golden, wantImg.Bounds())
			}
			b := gotImg.Bounds()
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					if g, w := color.RGBAModel.Convert(gotImg.At(x, y)), color.RGBAModel.Convert(wantImg.At(x, y)); g != w {
						t.Fatalf("strip differs from %s at (%d, %d): got %v, want %v", tc.golden, x, y, g, w)
					}
				}
			}
		})
	}
}

func decode(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decoding a strip: %v", err)
	}
	return img
}

// The strip is the documented size, and a missing image leaves a blank tile
// in place rather than shifting the layout.
func TestStripLayout(t *testing.T) {
	full := cardimage.Party{Name: "Pilot", Hull: "Ship", Portrait: portraitA, Corp: corpLogo, Alliance: allyLogo}
	b, err := cardimage.Strip(cardimage.StripInput{Victim: full, Attacker: cardimage.Party{}})
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	img := decode(t, b)
	if got, want := img.Bounds(), image.Rect(0, 0, cardimage.Width, cardimage.Height); got != want {
		t.Fatalf("strip is %v, want %v", got, want)
	}

	// The centre of each of the attacker's three tiles is the blank grey,
	// mirroring the victim's tiles, which show their fixtures.
	blank := color.RGBA{0x38, 0x3A, 0x40, 0xFF}
	tiles := []struct {
		name   string
		victim image.Point
	}{
		{"corporation logo", image.Pt(4+32, 4+32)},
		{"alliance logo", image.Pt(4+32, 4+64+32)},
		{"portrait", image.Pt(4+64+4+64, 4+64)},
	}
	for _, tile := range tiles {
		attacker := image.Pt(cardimage.Width-1-tile.victim.X, tile.victim.Y)
		if got := color.RGBAModel.Convert(img.At(attacker.X, attacker.Y)); got != blank {
			t.Errorf("attacker's %s at %v is %v, want blank %v", tile.name, attacker, got, blank)
		}
		if got := color.RGBAModel.Convert(img.At(tile.victim.X, tile.victim.Y)); got == blank {
			t.Errorf("victim's %s at %v is blank, want its image", tile.name, tile.victim)
		}
	}
}
