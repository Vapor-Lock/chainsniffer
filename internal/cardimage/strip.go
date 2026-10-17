// Package cardimage draws the portrait strip that the strip card style attaches
// under its embed: the victim on the left, the final blow on the right, each
// with their corporation and alliance logos at the outer edge, their portrait
// inside them, and their name above their hull.
//
// The package is pure: it takes decoded images and text and returns a PNG. It
// fetches nothing and applies no text fallbacks; those are the caller's job.
package cardimage

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"sync"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Strip geometry. The canvas is drawn at about twice the size Discord shows
// it, so it stays sharp on high-DPI screens; these are retuned against real
// cards in the live check.
const (
	// Width and Height are the canvas size in pixels.
	Width  = 1024
	Height = 136

	// panelRadius rounds the corners of the dark panel.
	panelRadius = 12
	// margin is the gap between the canvas edge and the outermost tiles.
	margin = 4
	// logoSize is the side of the corporation and alliance logo squares,
	// stacked at the outer edge with the corporation on top.
	logoSize = 64
	// portraitSize is the side of the character portrait square, which
	// spans both logos.
	portraitSize = 2 * logoSize
	// tileGap separates the logos from the portrait.
	tileGap = 4
	// textGap separates the portrait from the text beside it.
	textGap = 16
	// centreGap keeps the two halves' text apart in the middle.
	centreGap = 8

	// nameSize and hullSize are the font sizes, in pixels, of the name line
	// (Go Bold) and the hull line (Go Regular).
	nameSize = 34
	hullSize = 30
)

// Strip colours, Discord's dark theme: the strip draws its own panel because
// Discord does not tell a bot the reader's theme.
var (
	panelColour = color.RGBA{0x2B, 0x2D, 0x31, 0xFF}
	blankColour = color.RGBA{0x38, 0x3A, 0x40, 0xFF}
	nameColour  = color.RGBA{0xF2, 0xF3, 0xF5, 0xFF}
	hullColour  = color.RGBA{0xB5, 0xBA, 0xC1, 0xFF}
)

// ellipsis ends a name or hull truncated to its half of the canvas.
const ellipsis = "…"

// Party is one side of the strip. A nil image leaves its tile blank, so the
// layout never shifts.
type Party struct {
	// Name is drawn in bold on the first line. The caller supplies the
	// fallback: for a victim the character, else the corporation, else
	// "Unknown pilot"; for the final blow the character, else the ship
	// (an NPC).
	Name string
	// Hull is drawn on the second line. The caller supplies the fallback:
	// the ship name, else "Unknown ship".
	Hull string

	Portrait image.Image
	Corp     image.Image
	Alliance image.Image
}

// StripInput is everything Strip draws: the victim on the left, text
// left-aligned, and the final blow on the right, text right-aligned.
type StripInput struct {
	Victim   Party
	Attacker Party
}

type faces struct {
	name, hull font.Face
}

var loadFaces = sync.OnceValues(func() (faces, error) {
	name, err := newFace(gobold.TTF, nameSize)
	if err != nil {
		return faces{}, fmt.Errorf("loading Go Bold: %w", err)
	}
	hull, err := newFace(goregular.TTF, hullSize)
	if err != nil {
		return faces{}, fmt.Errorf("loading Go Regular: %w", err)
	}
	return faces{name: name, hull: hull}, nil
})

func newFace(ttf []byte, size float64) (font.Face, error) {
	f, err := opentype.Parse(ttf)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
}

// Strip draws in as a PNG of Width × Height pixels.
func Strip(in StripInput) ([]byte, error) {
	fc, err := loadFaces()
	if err != nil {
		return nil, err
	}

	canvas := image.NewRGBA(image.Rect(0, 0, Width, Height))
	draw.DrawMask(canvas, canvas.Bounds(), image.NewUniform(panelColour), image.Point{},
		roundedRect{w: Width, h: Height, r: panelRadius}, image.Point{}, draw.Over)

	drawSide(canvas, fc, in.Victim, false)
	drawSide(canvas, fc, in.Attacker, true)

	var buf bytes.Buffer
	if err := png.Encode(&buf, canvas); err != nil {
		return nil, fmt.Errorf("encoding the strip: %w", err)
	}
	return buf.Bytes(), nil
}

// drawSide draws one party in its half of the canvas, mirrored for the right.
func drawSide(dst *image.RGBA, fc faces, p Party, right bool) {
	logoX := margin
	portraitX := logoX + logoSize + tileGap
	textLeft := portraitX + portraitSize + textGap
	textRight := Width/2 - centreGap/2
	if right {
		logoX = Width - margin - logoSize
		portraitX = logoX - tileGap - portraitSize
		textLeft = Width/2 + centreGap/2
		textRight = portraitX - textGap
	}

	drawTile(dst, image.Rect(logoX, margin, logoX+logoSize, margin+logoSize), p.Corp)
	drawTile(dst, image.Rect(logoX, margin+logoSize, logoX+logoSize, margin+2*logoSize), p.Alliance)
	drawTile(dst, image.Rect(portraitX, margin, portraitX+portraitSize, margin+portraitSize), p.Portrait)

	// The name sits beside the corporation logo and the hull beside the
	// alliance logo, each centred vertically on its row.
	drawText(dst, fc.name, nameColour, p.Name, textLeft, textRight, margin, right)
	drawText(dst, fc.hull, hullColour, p.Hull, textLeft, textRight, margin+logoSize, right)
}

// drawTile scales src into r, or fills r with the blank colour when src is nil.
func drawTile(dst *image.RGBA, r image.Rectangle, src image.Image) {
	if src == nil {
		draw.Draw(dst, r, image.NewUniform(blankColour), image.Point{}, draw.Src)
		return
	}
	draw.CatmullRom.Scale(dst, r, src, src.Bounds(), draw.Over, nil)
}

// drawText draws s on the row of logo height starting at top, between left
// and right, aligned to the right edge when alignRight is set, and truncated
// with an ellipsis when it does not fit.
func drawText(dst *image.RGBA, face font.Face, c color.Color, s string, left, right, top int, alignRight bool) {
	width := fixed.I(right - left)
	s, advance := fit(face, s, width)
	if s == "" {
		return
	}

	m := face.Metrics()
	baseline := fixed.I(top) + (fixed.I(logoSize)+m.Ascent-m.Descent)/2

	x := fixed.I(left)
	if alignRight {
		x = fixed.I(right) - advance
	}
	d := font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: face, Dot: fixed.Point26_6{X: x, Y: baseline}}
	d.DrawString(s)
}

// fit returns s, or its longest prefix followed by an ellipsis, so that it is
// no wider than width, together with its advance.
func fit(face font.Face, s string, width fixed.Int26_6) (string, fixed.Int26_6) {
	if adv := font.MeasureString(face, s); adv <= width {
		return s, adv
	}
	runes := []rune(s)
	for n := len(runes) - 1; n >= 0; n-- {
		t := strings.TrimRight(string(runes[:n]), " ") + ellipsis
		if adv := font.MeasureString(face, t); adv <= width {
			return t, adv
		}
	}
	return "", 0
}

// roundedRect is an antialiased mask of a w × h rectangle with corners of
// radius r.
type roundedRect struct {
	w, h, r int
}

func (m roundedRect) ColorModel() color.Model { return color.AlphaModel }

func (m roundedRect) Bounds() image.Rectangle { return image.Rect(0, 0, m.w, m.h) }

func (m roundedRect) At(x, y int) color.Color {
	if x < 0 || y < 0 || x >= m.w || y >= m.h {
		return color.Alpha{}
	}
	// Distance from the pixel's centre to the nearest corner's centre, when
	// the pixel lies in a corner square.
	r := float64(m.r)
	px, py := float64(x)+0.5, float64(y)+0.5
	cx, cy := px, py
	switch {
	case px < r:
		cx = r
	case px > float64(m.w)-r:
		cx = float64(m.w) - r
	}
	switch {
	case py < r:
		cy = r
	case py > float64(m.h)-r:
		cy = float64(m.h) - r
	}
	if cx == px || cy == py {
		return color.Alpha{A: 0xFF}
	}
	cover := r - math.Hypot(px-cx, py-cy) + 0.5
	switch {
	case cover <= 0:
		return color.Alpha{}
	case cover >= 1:
		return color.Alpha{A: 0xFF}
	}
	return color.Alpha{A: uint8(cover * 0xFF)}
}
