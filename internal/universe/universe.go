// Package universe resolves EVE Online IDs to names: solar systems to system
// names, and corporations and alliances to their names and tickers.
//
// Wanderer's kill payload names the system only by `solar_system_id`, and
// sometimes carries a corporation or alliance ID without its name or ticker,
// so the relay looks them up in ESI (https://esi.evetech.net), which is public
// and needs no authentication.
//
// Lookups are deliberately best-effort. A name is a nicety on an embed, so an
// unreachable, slow or ignorant ESI falls back to the numeric system ID, or to
// leaving the affiliation as the payload had it, instead of holding up the
// post (see the discord-killmail-posting spec, "System name"). Only
// successful lookups are cached, so an ID that failed to resolve is tried
// again on the next kill that carries it. Corporation and alliance names go
// through a NameCache, which the caller can back with something that outlives
// the process.
package universe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBaseURL is ESI's versioned base URL. The endpoints used below
	// are `GET /universe/systems/{id}/`, `GET /corporations/{id}/` and
	// `GET /alliances/{id}/`.
	DefaultBaseURL = "https://esi.evetech.net/latest"

	// DefaultTimeout caps a single lookup. ESI normally answers well inside
	// it; the point is to bound how long a sick ESI can delay a post.
	DefaultTimeout = 3 * time.Second

	// userAgent identifies the bot to ESI, which asks callers to say who they
	// are so that it can reach the author of a misbehaving client.
	userAgent = "chainsniffer (+https://github.com/Vapor-Lock/chainsniffer)"

	// maxBodyBytes bounds how much of an ESI answer is read. The real one is a
	// few hundred bytes; this only stops a misdirected base URL from streaming
	// something unbounded into the decoder.
	maxBodyBytes = 1 << 16
)

// The kinds a NameCache is keyed by, alongside the ID.
const (
	KindCorporation = "corporation"
	KindAlliance    = "alliance"
)

// Entity is the name and ticker of a corporation or an alliance.
type Entity struct {
	Name   string
	Ticker string
}

// NameCache stores resolved corporation and alliance names and tickers. kind
// is KindCorporation or KindAlliance. It deals in plain values so that another
// package can implement it without importing this one. Implementations must
// be safe for concurrent use.
//
// Only successful lookups are Put. A corporation or alliance can be neither
// renamed nor re-tickered by players, so an entry never goes stale.
type NameCache interface {
	Get(kind string, id int64) (name, ticker string, ok bool)
	Put(kind string, id int64, name, ticker string)
}

// Resolver resolves solar system IDs to names, caching the ones it has seen.
// It is safe for concurrent use. The zero value is not usable; call
// NewResolver.
type Resolver struct {
	baseURL string
	client  *http.Client
	timeout time.Duration
	logger  *slog.Logger

	mu    sync.RWMutex
	names map[int64]string

	entities NameCache
}

type options struct {
	baseURL  string
	client   *http.Client
	timeout  time.Duration
	logger   *slog.Logger
	entities NameCache
}

// An Option configures a Resolver at NewResolver time.
type Option func(*options)

// WithBaseURL points the resolver at another ESI base URL, which is what tests
// use to aim it at an httptest server. An empty url keeps DefaultBaseURL.
func WithBaseURL(url string) Option {
	return func(o *options) {
		if url != "" {
			o.baseURL = url
		}
	}
}

// WithHTTPClient sets the HTTP client used for lookups. A nil client keeps the
// default one.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) {
		if c != nil {
			o.client = c
		}
	}
}

// WithTimeout caps a single lookup. Values of zero or less keep
// DefaultTimeout.
func WithTimeout(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.timeout = d
		}
	}
}

// WithLogger sets the logger failed lookups are reported on. A nil logger
// keeps slog.Default.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithNameCache sets where resolved corporation and alliance names are cached.
// A nil cache keeps the default, an in-memory map that lasts as long as the
// resolver.
func WithNameCache(c NameCache) Option {
	return func(o *options) {
		if c != nil {
			o.entities = c
		}
	}
}

// NewResolver returns a resolver with an empty cache.
//
// The cache is unbounded on purpose: a chain holds dozens of systems, so the
// working set is tiny and system names never change.
func NewResolver(opts ...Option) *Resolver {
	o := options{
		baseURL: DefaultBaseURL,
		client:  &http.Client{},
		timeout: DefaultTimeout,
		logger:  slog.Default(),
	}
	for _, opt := range opts {
		opt(&o)
	}
	if o.entities == nil {
		o.entities = &memoryNames{entries: make(map[memoryKey]Entity)}
	}
	return &Resolver{
		baseURL: strings.TrimSuffix(o.baseURL, "/"),
		client:  o.client,
		timeout: o.timeout,
		logger:  o.logger,
		names:   make(map[int64]string),

		entities: o.entities,
	}
}

// Name returns the name of the solar system with the given ID, falling back to
// the ID in decimal when it cannot be resolved.
//
// An id of zero or less — what a kill payload without a `solar_system_id`
// decodes to — has nothing to resolve and returns the empty string, which the
// embed renderer leaves out rather than showing as a system.
func (r *Resolver) Name(ctx context.Context, id int64) string {
	if id <= 0 {
		return ""
	}
	if name, ok := r.cached(id); ok {
		return name
	}

	name, err := r.lookup(ctx, id)
	if err != nil {
		r.logger.Warn("falling back to the numeric solar system ID",
			"solar_system_id", id, "error", err)
		return strconv.FormatInt(id, 10)
	}

	r.mu.Lock()
	r.names[id] = name
	r.mu.Unlock()
	return name
}

func (r *Resolver) cached(id int64) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	name, ok := r.names[id]
	return name, ok
}

// Corporation returns the name and ticker of the corporation with the given
// ID, and false when it cannot be resolved. An id of zero or less returns
// false without a request.
func (r *Resolver) Corporation(ctx context.Context, id int64) (Entity, bool) {
	return r.entity(ctx, KindCorporation, "corporations", id)
}

// Alliance returns the name and ticker of the alliance with the given ID, and
// false when it cannot be resolved. An id of zero or less returns false
// without a request.
func (r *Resolver) Alliance(ctx context.Context, id int64) (Entity, bool) {
	return r.entity(ctx, KindAlliance, "alliances", id)
}

// entity resolves one corporation or alliance through the name cache, asking
// ESI's `GET /{collection}/{id}/` on a miss.
func (r *Resolver) entity(ctx context.Context, kind, collection string, id int64) (Entity, bool) {
	if id <= 0 {
		return Entity{}, false
	}
	if name, ticker, ok := r.entities.Get(kind, id); ok {
		return Entity{Name: name, Ticker: ticker}, true
	}

	var body struct {
		Name   string `json:"name"`
		Ticker string `json:"ticker"`
	}
	err := r.fetch(ctx, fmt.Sprintf("/%s/%d/", collection, id), &body)
	if err == nil && (body.Name == "" || body.Ticker == "") {
		err = fmt.Errorf("the ESI answer lacks the %s name or ticker", kind)
	}
	if err != nil {
		r.logger.Warn("leaving the "+kind+" unresolved", kind+"_id", id, "error", err)
		return Entity{}, false
	}

	r.logger.Debug("resolved the "+kind+" from ESI", kind+"_id", id, "name", body.Name, "ticker", body.Ticker)
	r.entities.Put(kind, id, body.Name, body.Ticker)
	return Entity{Name: body.Name, Ticker: body.Ticker}, true
}

// lookup asks ESI for one system.
func (r *Resolver) lookup(ctx context.Context, id int64) (string, error) {
	var body struct {
		Name string `json:"name"`
	}
	if err := r.fetch(ctx, fmt.Sprintf("/universe/systems/%d/", id), &body); err != nil {
		return "", err
	}
	if body.Name == "" {
		return "", errors.New("the ESI answer carries no system name")
	}
	return body.Name, nil
}

// fetch GETs path below the base URL and decodes the JSON answer into v. The
// timeout is applied on top of ctx, so a shutdown still cancels a lookup in
// flight.
func (r *Resolver) fetch(ctx context.Context, path string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ESI answered %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(v); err != nil {
		return fmt.Errorf("decoding the ESI answer: %w", err)
	}
	return nil
}

type memoryKey struct {
	kind string
	id   int64
}

// memoryNames is the default NameCache: a map that lasts as long as the
// resolver.
type memoryNames struct {
	mu      sync.RWMutex
	entries map[memoryKey]Entity
}

func (m *memoryNames) Get(kind string, id int64) (name, ticker string, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[memoryKey{kind, id}]
	return e.Name, e.Ticker, ok
}

func (m *memoryNames) Put(kind string, id int64, name, ticker string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[memoryKey{kind, id}] = Entity{Name: name, Ticker: ticker}
}
