// Package wanderer subscribes to one Wanderer map's Server-Sent Events stream
// and hands its kill events to a handler.
//
// A Client owns the connection: it builds the authenticated request, keeps the
// stream open, reconnects with exponential backoff, resumes where it left off
// through Wanderer's last_event_id query parameter, and replaces a connection
// that has gone silent. Responses that no retry can fix — a rejected token, an
// unknown map, SSE switched off — come back as a *FatalError so that the
// process can stop instead of hammering the server.
//
// The wire format follows Wanderer's events controller rather than its blog
// post: the token is only accepted in an Authorization header, every event is
// framed as "id: <ULID>\nevent: <type>\ndata: <envelope JSON>\n\n", keepalives
// are ": keepalive" comments, and the first event on every connection has type
// "connected". internal/sse does the framing; this package owns the connection
// loop because Wanderer resumes a stream with a query parameter instead of the
// Last-Event-ID header the event stream format prescribes.
package wanderer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Vapor-Lock/chainsniffer/internal/sse"
)

// Event types on the stream. The client subscribes to EventMapKill only, so
// EventConnected is the only other type Wanderer is expected to send.
const (
	// EventConnected is the type of the first event on every connection.
	EventConnected = "connected"
	// EventMapKill is the event type carrying kill payloads.
	EventMapKill = "map_kill"
)

// Defaults for the connection loop, all overridable with options.
const (
	// DefaultMinBackoff is the first wait after a failed connection.
	DefaultMinBackoff = 1 * time.Second
	// DefaultMaxBackoff caps the wait between connection attempts.
	DefaultMaxBackoff = 60 * time.Second
	// DefaultJitter is the fraction the wait is randomly stretched or
	// shortened by, so several relays do not retry in lockstep.
	DefaultJitter = 0.2
	// DefaultStaleTimeout is how long a connection may deliver nothing at
	// all — not even a keepalive — before it is treated as dead. Wanderer
	// sends a keepalive about every 30 seconds.
	DefaultStaleTimeout = 90 * time.Second
)

// Client streams one map's kill events. Use New to build one; a Client is safe
// to use from one Run at a time.
type Client struct {
	stream url.URL // the stream endpoint, without a query
	mapID  string
	token  string

	httpClient *http.Client
	logger     *slog.Logger

	minBackoff   time.Duration
	maxBackoff   time.Duration
	jitter       float64
	staleTimeout time.Duration
	sleep        func(context.Context, time.Duration) error
}

type options struct {
	httpClient   *http.Client
	logger       *slog.Logger
	minBackoff   time.Duration
	maxBackoff   time.Duration
	jitter       float64
	staleTimeout time.Duration
	sleep        func(context.Context, time.Duration) error
}

// An Option configures a Client at New time.
type Option func(*options)

// WithHTTPClient sets the client used for the stream requests. The client must
// not have a Timeout, since that would also bound the lifetime of a healthy
// stream; use WithStaleTimeout to bound silence instead.
func WithHTTPClient(h *http.Client) Option {
	return func(o *options) {
		if h != nil {
			o.httpClient = h
		}
	}
}

// WithLogger sets the logger for connection, reconnection and malformed event
// messages. The map token is never logged.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithBackoff sets the first and the maximum wait between connection attempts.
// Non-positive values select the defaults, and a maximum below the minimum is
// raised to it.
func WithBackoff(initial, maximum time.Duration) Option {
	return func(o *options) {
		if initial > 0 {
			o.minBackoff = initial
		}
		if maximum > 0 {
			o.maxBackoff = maximum
		}
	}
}

// WithJitter sets the fraction of the wait that is randomised, so 0.2 spreads
// a wait over ±20%. Zero makes the waits exact, which is mainly useful in
// tests; values outside [0, 1) select the default.
func WithJitter(fraction float64) Option {
	return func(o *options) {
		if fraction >= 0 && fraction < 1 {
			o.jitter = fraction
		}
	}
}

// WithStaleTimeout sets how long a connection may deliver no bytes before it
// is closed and retried. Non-positive values select DefaultStaleTimeout.
func WithStaleTimeout(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.staleTimeout = d
		}
	}
}

// WithSleep replaces the wait between connection attempts. It exists so tests
// can record the backoff instead of spending it; the function must return a
// non-nil error when ctx is done.
func WithSleep(fn func(ctx context.Context, d time.Duration) error) Option {
	return func(o *options) {
		if fn != nil {
			o.sleep = fn
		}
	}
}

// New returns a Client for the map identified by mapID — its UUID or its slug —
// on the Wanderer instance at baseURL, authenticating with token, the map's
// public API key. baseURL may carry a path prefix; the stream endpoint is
// appended to it.
func New(baseURL, mapID, token string, opts ...Option) (*Client, error) {
	switch {
	case strings.TrimSpace(baseURL) == "":
		return nil, errors.New("wanderer: base URL is required")
	case strings.TrimSpace(mapID) == "":
		return nil, errors.New("wanderer: map ID is required")
	case token == "":
		return nil, errors.New("wanderer: map API token is required")
	}

	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("wanderer: parsing base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("wanderer: base URL %q needs an http or https scheme", baseURL)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("wanderer: base URL %q has no host", baseURL)
	}

	o := options{
		httpClient:   &http.Client{},
		logger:       slog.Default(),
		minBackoff:   DefaultMinBackoff,
		maxBackoff:   DefaultMaxBackoff,
		jitter:       DefaultJitter,
		staleTimeout: DefaultStaleTimeout,
		sleep:        sleep,
	}
	for _, opt := range opts {
		opt(&o)
	}
	if o.maxBackoff < o.minBackoff {
		o.maxBackoff = o.minBackoff
	}

	return &Client{
		stream:       *parsed.JoinPath("api", "maps", mapID, "events", "stream"),
		mapID:        mapID,
		token:        token,
		httpClient:   o.httpClient,
		logger:       o.logger,
		minBackoff:   o.minBackoff,
		maxBackoff:   o.maxBackoff,
		jitter:       o.jitter,
		staleTimeout: o.staleTimeout,
		sleep:        o.sleep,
	}, nil
}

// Run streams kill events until ctx is done, a response turns out to be fatal,
// or handle fails.
//
// cursor is read before every connection attempt and supplies the ID of the
// last event the caller has finished processing, or "" when there is none; the
// ID becomes the last_event_id query parameter, which makes Wanderer replay
// the events the relay missed. Reading it afresh each time means a reconnect
// resumes from the newest processed event rather than from where the previous
// connection started.
//
// handle is called synchronously for every map_kill event, in stream order, so
// a slow handler applies backpressure to the stream instead of queueing up
// behind it. While it runs, the staleness watchdog is paused: it measures the
// server's silence, not the relay's own work. A handler error ends Run, since
// it means the relay itself, not the connection, is broken.
//
// Run returns ctx.Err() on a clean shutdown, a *FatalError for a response that
// retrying cannot fix, or the handler's error. Every other failure — a network
// error, the end of a stream, 429, 5xx, a silent connection — is logged and
// retried with backoff, so Run does not return for those.
func (c *Client) Run(ctx context.Context, cursor func() string, handle func(Envelope) error) error {
	if handle == nil {
		return errors.New("wanderer: an event handler is required")
	}
	if cursor == nil {
		cursor = func() string { return "" }
	}

	// Consecutive attempts that did not reach a healthy connection. One means
	// "wait the minimum", which is also where a dropped healthy connection
	// starts again.
	failures := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		healthy, err := c.connect(ctx, cursor(), handle)

		// A cancelled context is a shutdown, whatever error the in-flight
		// request reported as it was torn down.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		var handlerErr *handlerError
		if IsFatal(err) || errors.As(err, &handlerErr) {
			return err
		}

		if healthy {
			failures = 1
		} else {
			failures++
		}
		delay := c.delay(failures)
		c.logger.Warn("Wanderer stream disconnected, reconnecting",
			"error", err, "retry_in", delay, "attempt", failures+1)
		if err := c.sleep(ctx, delay); err != nil {
			return err
		}
	}
}

// connect opens the stream once and reads it until it ends. It reports whether
// the connection ever delivered anything — an event or a keepalive — which is
// what resets the backoff, together with the reason the connection ended.
func (c *Client) connect(ctx context.Context, cursor string, handle func(Envelope) error) (bool, error) {
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The watchdog is armed before the request, so a server that accepts the
	// connection and then says nothing at all — not even response headers —
	// is caught by the same timeout as one that goes quiet mid-stream.
	var stale atomic.Bool
	watchdog := time.AfterFunc(c.staleTimeout, func() {
		stale.Store(true)
		cancel()
	})
	defer watchdog.Stop()

	req, err := c.request(reqCtx, cursor)
	if err != nil {
		return false, err
	}

	// The token travels in a header, so neither the request URL nor the
	// *url.Error wrapping a transport failure can leak it.
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if stale.Load() {
			return false, fmt.Errorf("wanderer: no response within %s", c.staleTimeout)
		}
		return false, fmt.Errorf("wanderer: connecting to the map stream: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return false, statusError(resp)
	}

	healthy := false
	reader := sse.NewReader(resp.Body, func() {
		healthy = true
		watchdog.Reset(c.staleTimeout)
	})
	for {
		event, err := reader.Next()
		if err != nil {
			switch {
			case stale.Load():
				return healthy, fmt.Errorf("wanderer: stream delivered nothing for %s", c.staleTimeout)
			case errors.Is(err, io.EOF):
				return healthy, errors.New("wanderer: stream ended")
			default:
				return healthy, fmt.Errorf("wanderer: reading the stream: %w", err)
			}
		}

		switch event.Type {
		case EventMapKill:
			envelope, err := decodeEnvelope(event)
			if err != nil {
				// Payload drift must not take the connection down: the event
				// is dropped and the stream keeps running.
				c.logger.Warn("skipping malformed Wanderer event", "event_id", event.ID, "error", err)
				continue
			}
			watchdog.Stop()
			handleErr := handle(envelope)
			watchdog.Reset(c.staleTimeout)
			if handleErr != nil {
				return healthy, &handlerError{err: fmt.Errorf("wanderer: handling event %s: %w", envelope.ID, handleErr)}
			}
		case EventConnected:
			c.logger.Info("subscribed to the Wanderer map kill stream",
				"map", c.mapID, "url", c.stream.String(), "last_event_id", cursor)
		default:
			c.logger.Debug("ignoring Wanderer event", "type", event.Type, "event_id", event.ID)
		}
	}
}

// request builds one stream request. cursor, when set, resumes the stream from
// Wanderer's in-memory relay of recent events.
func (c *Client) request(ctx context.Context, cursor string) (*http.Request, error) {
	endpoint := c.stream
	query := url.Values{"events": {EventMapKill}}
	if cursor != "" {
		query.Set("last_event_id", cursor)
	}
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("wanderer: building the stream request: %w", err)
	}
	// Wanderer's controller accepts the map API key in this header only.
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	return req, nil
}

// delay is how long to wait before connection attempt failures+1: the minimum
// doubled once per preceding failure, capped, then jittered.
func (c *Client) delay(failures int) time.Duration {
	if failures < 1 {
		return 0
	}
	d := float64(c.minBackoff) * math.Pow(2, float64(failures-1))
	if limit := float64(c.maxBackoff); d > limit || math.IsInf(d, 0) {
		d = limit
	}
	if c.jitter > 0 {
		d *= 1 + c.jitter*(2*rand.Float64()-1)
	}
	return time.Duration(d)
}

// sleep is the default wait between connection attempts.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// handlerError marks an error as the handler's rather than the connection's,
// so Run stops instead of reconnecting.
type handlerError struct{ err error }

func (e *handlerError) Error() string { return e.err.Error() }
func (e *handlerError) Unwrap() error { return e.err }
