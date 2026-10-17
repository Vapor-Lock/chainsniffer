package eveimage_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Vapor-Lock/chainsniffer/internal/eveimage"
)

// newFetcher builds a fetcher aimed at srv with its warnings discarded, so a
// deliberate failure does not litter the test output.
func newFetcher(t *testing.T, srv *httptest.Server, opts ...eveimage.Option) *eveimage.Fetcher {
	t.Helper()
	base := []eveimage.Option{
		eveimage.WithBaseURL(srv.URL),
		eveimage.WithHTTPClient(srv.Client()),
		eveimage.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	return eveimage.New(append(base, opts...)...)
}

// fixture encodes a 4×2 solid image with the given encoder.
func fixture(t *testing.T, encode func(io.Writer, image.Image) error) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for y := range 2 {
		for x := range 4 {
			img.Set(x, y, color.RGBA{R: 200, G: 40, B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := encode(&buf, img); err != nil {
		t.Fatalf("encoding the fixture: %v", err)
	}
	return buf.Bytes()
}

func encodePNG(w io.Writer, img image.Image) error  { return png.Encode(w, img) }
func encodeJPEG(w io.Writer, img image.Image) error { return jpeg.Encode(w, img, nil) }

// imageHandler serves body for every request and counts them.
func imageHandler(t *testing.T, body []byte, requests *atomic.Int64) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("User-Agent") == "" {
			t.Error("User-Agent header is empty, want the bot's identity")
		}
		_, _ = w.Write(body)
	}
}

// Logos are PNG and portraits JPEG; both decode.
func TestFetchDecodes(t *testing.T) {
	tests := []struct {
		name   string
		encode func(io.Writer, image.Image) error
	}{
		{name: "PNG", encode: encodePNG},
		{name: "JPEG", encode: encodeJPEG},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int64
			srv := httptest.NewServer(imageHandler(t, fixture(t, tc.encode), &requests))
			defer srv.Close()

			f := newFetcher(t, srv)
			img, ok := f.Fetch(context.Background(), f.CharacterPortrait(90000001, 128))
			if !ok {
				t.Fatal("Fetch() reported no image")
			}
			if got, want := img.Bounds(), image.Rect(0, 0, 4, 2); got != want {
				t.Errorf("image bounds = %v, want %v", got, want)
			}
		})
	}
}

// A decoded image is cached, so a repeat kill by the same pilot costs no
// request.
func TestFetchCachesSuccess(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(imageHandler(t, fixture(t, encodePNG), &requests))
	defer srv.Close()

	f := newFetcher(t, srv)
	url := f.CorporationLogo(98000001, 64)
	for i := range 3 {
		if _, ok := f.Fetch(context.Background(), url); !ok {
			t.Fatalf("call %d: Fetch() reported no image", i+1)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("requests = %d, want 1 (the rest should come from the cache)", got)
	}
}

// A missing image is reported as none, and the failure is not cached.
func TestFetch404IsNotCached(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	f := newFetcher(t, srv)
	url := f.AllianceLogo(99000001, 64)
	for i := range 2 {
		if img, ok := f.Fetch(context.Background(), url); ok || img != nil {
			t.Fatalf("call %d: Fetch() = %v, %v, want nil, false", i+1, img, ok)
		}
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("requests = %d, want 2 (a failed fetch must not be cached)", got)
	}
}

// An answer that is not an image is a failure like any other.
func TestFetchUndecodable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>gateway</html>"))
	}))
	defer srv.Close()

	f := newFetcher(t, srv)
	if _, ok := f.Fetch(context.Background(), f.CharacterPortrait(1, 128)); ok {
		t.Error("Fetch() reported an image for an HTML answer")
	}
}

// "Card images" / "Image server unavailable": a fetch that runs past the
// timeout gives up instead of delaying the post further.
func TestFetchTimeout(t *testing.T) {
	released := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-released:
		}
	}))
	defer func() {
		close(released)
		srv.Close()
	}()

	f := newFetcher(t, srv, eveimage.WithTimeout(50*time.Millisecond))
	start := time.Now()
	if _, ok := f.Fetch(context.Background(), f.CharacterPortrait(1, 128)); ok {
		t.Error("Fetch() reported an image, want none after the timeout")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Fetch() took %s, want it bounded by the timeout", elapsed)
	}
}

// The cache keeps the most recently used images: past its size, the least
// recently used one is fetched again.
func TestFetchEvictsLeastRecentlyUsed(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(imageHandler(t, fixture(t, encodePNG), &requests))
	defer srv.Close()

	ctx := context.Background()
	f := newFetcher(t, srv, eveimage.WithCacheSize(2))
	a, b, c := f.CharacterPortrait(1, 64), f.CharacterPortrait(2, 64), f.CharacterPortrait(3, 64)

	f.Fetch(ctx, a)
	f.Fetch(ctx, b)
	f.Fetch(ctx, a) // a is now the most recently used
	f.Fetch(ctx, c) // evicts b
	if got := requests.Load(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}

	f.Fetch(ctx, a) // still cached
	if got := requests.Load(); got != 3 {
		t.Errorf("requests after refetching a = %d, want 3 (a should still be cached)", got)
	}
	f.Fetch(ctx, b) // evicted, fetched again
	if got := requests.Load(); got != 4 {
		t.Errorf("requests after refetching b = %d, want 4 (b should have been evicted)", got)
	}
}

// The empty URL — what a helper returns for a missing ID — is no image and
// makes no request.
func TestFetchEmptyURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the fetcher made a request for the empty URL")
	}))
	defer srv.Close()

	if img, ok := newFetcher(t, srv).Fetch(context.Background(), ""); ok || img != nil {
		t.Errorf("Fetch(\"\") = %v, %v, want nil, false", img, ok)
	}
}

func TestURLHelpers(t *testing.T) {
	f := eveimage.New()
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"portrait", f.CharacterPortrait(90000001, 128), "https://images.evetech.net/characters/90000001/portrait?size=128"},
		{"corporation", f.CorporationLogo(98000001, 64), "https://images.evetech.net/corporations/98000001/logo?size=64"},
		{"alliance", f.AllianceLogo(99000001, 64), "https://images.evetech.net/alliances/99000001/logo?size=64"},
		{"no character", f.CharacterPortrait(0, 128), ""},
		{"no corporation", f.CorporationLogo(-1, 64), ""},
		{"no alliance", f.AllianceLogo(0, 64), ""},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s: URL = %q, want %q", tc.name, tc.got, tc.want)
		}
	}

	custom := eveimage.New(eveimage.WithBaseURL("http://example.test/"))
	if got, want := custom.AllianceLogo(1, 32), "http://example.test/alliances/1/logo?size=32"; got != want {
		t.Errorf("custom base URL = %q, want %q", got, want)
	}
}
