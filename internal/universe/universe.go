// Package universe resolves EVE Online solar system IDs to system names.
//
// Wanderer's kill payload names the system only by `solar_system_id`, so the
// relay looks the name up in ESI (https://esi.evetech.net), which is public
// and needs no authentication.
//
// Lookups are deliberately best-effort. A system name is a nicety on an embed,
// so an unreachable, slow or ignorant ESI falls back to the numeric ID instead
// of holding up the post (see the discord-killmail-posting spec, "System
// name"). Only successful lookups are cached, so a system whose name failed to
// resolve is tried again on the next kill there.
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
	// DefaultBaseURL is ESI's versioned base URL. The endpoint used below is
	// `GET /universe/systems/{id}/`.
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
}

type options struct {
	baseURL string
	client  *http.Client
	timeout time.Duration
	logger  *slog.Logger
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
	return &Resolver{
		baseURL: strings.TrimSuffix(o.baseURL, "/"),
		client:  o.client,
		timeout: o.timeout,
		logger:  o.logger,
		names:   make(map[int64]string),
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

// lookup asks ESI for one system. The timeout is applied on top of ctx, so a
// shutdown still cancels a lookup in flight.
func (r *Resolver) lookup(ctx context.Context, id int64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/universe/systems/%d/", r.baseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ESI answered %s", resp.Status)
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&body); err != nil {
		return "", fmt.Errorf("decoding the ESI answer: %w", err)
	}
	if body.Name == "" {
		return "", errors.New("the ESI answer carries no system name")
	}
	return body.Name, nil
}
