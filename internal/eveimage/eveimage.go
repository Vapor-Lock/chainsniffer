// Package eveimage fetches portraits and logos from the EVE image server
// (https://images.evetech.net) and decodes them.
//
// Like system names, card images are a nicety: an image that cannot be
// fetched becomes a blank tile instead of holding up the post (see the
// discord-killmail-posting spec, "Card images"). Fetches are bounded by a
// timeout, failures are logged and not cached, and successes are kept in a
// small in-memory LRU so that repeat kills by the same pilots download nothing.
package eveimage

import (
	"container/list"
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // portraits are JPEG
	_ "image/png"  // logos are PNG
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBaseURL is the EVE image server.
	DefaultBaseURL = "https://images.evetech.net"

	// DefaultTimeout caps a single fetch, so that a slow image server delays a
	// card by a few seconds at most.
	DefaultTimeout = 3 * time.Second

	// DefaultCacheSize is how many decoded images the cache keeps. A
	// 128 px portrait decodes to about 64 KiB, so the cache stays in the tens
	// of megabytes even when full of large images.
	DefaultCacheSize = 256

	// userAgent identifies the bot, as in internal/universe.
	userAgent = "chainsniffer (+https://github.com/Vapor-Lock/chainsniffer)"

	// maxBodyBytes bounds how much of an answer is read. A 1024 px portrait is
	// a few hundred kilobytes; this only stops a misdirected base URL from
	// streaming something unbounded into the decoder.
	maxBodyBytes = 8 << 20
)

// Fetcher fetches and decodes images, caching the ones it has decoded. It is
// safe for concurrent use. The zero value is not usable; call New.
type Fetcher struct {
	baseURL string
	client  *http.Client
	timeout time.Duration
	logger  *slog.Logger

	mu    sync.Mutex
	size  int
	order *list.List               // most recently used at the front; values are *entry
	byURL map[string]*list.Element // into order
}

type entry struct {
	url string
	img image.Image
}

type options struct {
	baseURL   string
	client    *http.Client
	timeout   time.Duration
	logger    *slog.Logger
	cacheSize int
}

// An Option configures a Fetcher at New time.
type Option func(*options)

// WithBaseURL points the URL helpers at another image server, which is what
// tests use to aim them at an httptest server. An empty url keeps
// DefaultBaseURL.
func WithBaseURL(url string) Option {
	return func(o *options) {
		if url != "" {
			o.baseURL = url
		}
	}
}

// WithHTTPClient sets the HTTP client used for fetches. A nil client keeps the
// default one.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) {
		if c != nil {
			o.client = c
		}
	}
}

// WithTimeout caps a single fetch. Values of zero or less keep
// DefaultTimeout.
func WithTimeout(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.timeout = d
		}
	}
}

// WithLogger sets the logger failed fetches are reported on. A nil logger
// keeps slog.Default.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithCacheSize sets how many decoded images are kept. Values of zero or less
// keep DefaultCacheSize.
func WithCacheSize(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.cacheSize = n
		}
	}
}

// New returns a fetcher with an empty cache.
func New(opts ...Option) *Fetcher {
	o := options{
		baseURL:   DefaultBaseURL,
		client:    &http.Client{},
		timeout:   DefaultTimeout,
		logger:    slog.Default(),
		cacheSize: DefaultCacheSize,
	}
	for _, opt := range opts {
		opt(&o)
	}
	return &Fetcher{
		baseURL: strings.TrimSuffix(o.baseURL, "/"),
		client:  o.client,
		timeout: o.timeout,
		logger:  o.logger,
		size:    o.cacheSize,
		order:   list.New(),
		byURL:   make(map[string]*list.Element),
	}
}

// CharacterPortrait returns the URL of a character's portrait, size pixels
// square. An id of zero or less — a payload without that ID — returns the
// empty string, which Fetch treats as a missing image.
func (f *Fetcher) CharacterPortrait(id int64, size int) string {
	return f.url("characters", id, "portrait", size)
}

// CorporationLogo returns the URL of a corporation's logo, size pixels
// square, or the empty string for an id of zero or less.
func (f *Fetcher) CorporationLogo(id int64, size int) string {
	return f.url("corporations", id, "logo", size)
}

// AllianceLogo returns the URL of an alliance's logo, size pixels square, or
// the empty string for an id of zero or less.
func (f *Fetcher) AllianceLogo(id int64, size int) string {
	return f.url("alliances", id, "logo", size)
}

func (f *Fetcher) url(kind string, id int64, variant string, size int) string {
	if id <= 0 {
		return ""
	}
	return fmt.Sprintf("%s/%s/%d/%s?size=%d", f.baseURL, kind, id, variant, size)
}

// Fetch returns the decoded image at url, and whether there is one. The empty
// url has no image and makes no request. A failed fetch is logged at warn and
// tried again on the next call.
func (f *Fetcher) Fetch(ctx context.Context, url string) (image.Image, bool) {
	if url == "" {
		return nil, false
	}
	if img, ok := f.cached(url); ok {
		return img, true
	}

	img, err := f.get(ctx, url)
	if err != nil {
		f.logger.Warn("leaving a card image blank", "url", url, "error", err)
		return nil, false
	}
	f.store(url, img)
	return img, true
}

func (f *Fetcher) cached(url string) (image.Image, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	el, ok := f.byURL[url]
	if !ok {
		return nil, false
	}
	f.order.MoveToFront(el)
	return el.Value.(*entry).img, true
}

func (f *Fetcher) store(url string, img image.Image) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if el, ok := f.byURL[url]; ok {
		// A concurrent fetch of the same URL got here first.
		f.order.MoveToFront(el)
		return
	}
	f.byURL[url] = f.order.PushFront(&entry{url: url, img: img})
	for f.order.Len() > f.size {
		oldest := f.order.Back()
		f.order.Remove(oldest)
		delete(f.byURL, oldest.Value.(*entry).url)
	}
}

// get downloads and decodes one image. The timeout is applied on top of ctx,
// so a shutdown still cancels a fetch in flight.
func (f *Fetcher) get(ctx context.Context, url string) (image.Image, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/png, image/jpeg")
	req.Header.Set("User-Agent", userAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the image server answered %s", resp.Status)
	}

	img, _, err := image.Decode(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("decoding the image: %w", err)
	}
	return img, nil
}
