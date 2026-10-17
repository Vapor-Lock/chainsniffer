package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	// DefaultRetryBudget is how long Post keeps retrying one embed before it
	// gives up on it. The relay drops the killmail rather than stalling the
	// stream behind a Discord outage.
	DefaultRetryBudget = 5 * time.Minute

	// defaultBackoff is the delay before the first retry, and
	// defaultMaxBackoff the cap it doubles up to.
	defaultBackoff    = 1 * time.Second
	defaultMaxBackoff = 60 * time.Second
)

// Poster posts embeds to one Discord channel as a bot. Create it with New.
//
// A Poster is safe for concurrent use as far as discordgo is, but the relay
// posts from a single goroutine so that killmails reach the channel in the
// order they arrived.
type Poster struct {
	session   *discordgo.Session
	channelID string
	logger    *slog.Logger

	// budget caps the total time Post spends on one embed; backoff and
	// maxBackoff shape the delays between its attempts.
	budget     time.Duration
	backoff    time.Duration
	maxBackoff time.Duration

	// now and sleep are the Poster's clock, replaced in tests to spend the
	// retry budget without waiting for it.
	now   func() time.Time
	sleep func(context.Context, time.Duration) error

	// openGateway and closeGateway are the gateway session's lifecycle. They
	// are fields so tests can exercise the startup check against an HTTP fake
	// without standing up a websocket server.
	openGateway  func() error
	closeGateway func() error

	// attachFiles adds Attach Files to the permissions the bot needs, for
	// cards that upload an image.
	attachFiles bool

	mu          sync.Mutex
	gatewayOpen bool
}

type options struct {
	logger      *slog.Logger
	baseURL     string
	budget      time.Duration
	attachFiles bool
}

// An Option configures a Poster at New time.
type Option func(*options)

// WithLogger sets the logger used to report retries and what the startup check
// found.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithRetryBudget sets how long Post retries one embed. Values below one
// select DefaultRetryBudget.
func WithRetryBudget(d time.Duration) Option {
	return func(o *options) { o.budget = d }
}

// WithAttachFiles makes Attach Files one of the permissions the bot needs on
// the channel: the startup check requires it, and the hint for a 403 or 404
// names it. Set it when the cards carry files, as the strip style's do.
func WithAttachFiles(on bool) Option {
	return func(o *options) { o.attachFiles = on }
}

// WithBaseURL points the Poster at another Discord API root, such as
// "http://127.0.0.1:1234/api/v9/". Only the scheme and host are used; tests
// pass an httptest server's URL to stand in for Discord.
//
// Redirecting the HTTP client is deliberate. discordgo keeps its endpoints in
// package-level variables, and rewriting those would reach into every session
// in the process, including ones other tests are using.
func WithBaseURL(raw string) Option {
	return func(o *options) { o.baseURL = raw }
}

// New returns a Poster that posts to channelID as the bot identified by token,
// which is the bot token without discordgo's "Bot " prefix.
func New(token, channelID string, opts ...Option) (*Poster, error) {
	if token == "" {
		return nil, errors.New("discord: empty bot token")
	}
	if channelID == "" {
		return nil, errors.New("discord: empty channel ID")
	}

	o := options{logger: slog.New(slog.DiscardHandler), budget: DefaultRetryBudget}
	for _, opt := range opts {
		opt(&o)
	}
	if o.budget <= 0 {
		o.budget = DefaultRetryBudget
	}

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("discord: new session: %w", err)
	}
	// The relay only writes. It never reads gateway events, so it identifies
	// with no intents at all.
	session.Identify.Intents = discordgo.IntentsNone

	if o.baseURL != "" {
		base, err := url.Parse(o.baseURL)
		if err != nil {
			return nil, fmt.Errorf("discord: base URL %q: %w", o.baseURL, err)
		}
		if base.Scheme == "" || base.Host == "" {
			return nil, fmt.Errorf("discord: base URL %q needs a scheme and a host", o.baseURL)
		}
		session.Client = &http.Client{
			Timeout:   session.Client.Timeout,
			Transport: &rewriteTransport{scheme: base.Scheme, host: base.Host},
		}
	}

	p := &Poster{
		session:     session,
		channelID:   channelID,
		logger:      o.logger,
		budget:      o.budget,
		attachFiles: o.attachFiles,
		backoff:     defaultBackoff,
		maxBackoff:  defaultMaxBackoff,
		now:         time.Now,
		sleep:       sleepContext,
	}
	p.openGateway = session.Open
	p.closeGateway = session.Close
	return p, nil
}

// ChannelID is the channel the Poster posts to.
func (p *Poster) ChannelID() string { return p.channelID }

// Post sends msg to the configured channel as a single message: an embed with
// its files, or a Components V2 layout.
//
// Rate limits are discordgo's business: it waits out a 429's retry_after and
// resends the same message. Post adds a retry budget for the failures
// discordgo hands back instead — network errors, timeouts and 5xx — doubling
// the delay between attempts and returning a *GaveUpError once the budget is
// spent. The caller skips that killmail and does not record it as posted, so
// one undeliverable kill cannot wedge the stream.
//
// A *FatalError means an operator has to change something: the bot token, the
// channel ID, or the bot's permissions on the channel. The relay exits
// non-zero rather than retrying.
func (p *Poster) Post(ctx context.Context, msg Message) error {
	if err := msg.validate(); err != nil {
		return fmt.Errorf("discord: post to channel %s: %w", p.channelID, err)
	}
	start := p.now()
	delay := p.backoff

	for attempt := 1; ; attempt++ {
		err := p.send(ctx, msg)
		if err == nil {
			return nil
		}
		// A cancelled context surfaces as an opaque transport error, which
		// classify would read as worth another try.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("discord: post to channel %s: %w", p.channelID, ctxErr)
		}

		class, status := classify(err)
		switch class {
		case classFatal:
			return &FatalError{Op: "post", Status: status, Hint: p.hintFor(status), Err: err}
		case classPermanent:
			return fmt.Errorf("discord: post to channel %s: %w", p.channelID, err)
		}

		elapsed := p.now().Sub(start)
		if elapsed+delay > p.budget {
			return &GaveUpError{Attempts: attempt, Elapsed: elapsed, Err: err}
		}
		p.logger.Warn("retrying Discord post",
			"channel_id", p.channelID, "attempt", attempt, "delay", delay, "error", err)
		if err := p.sleep(ctx, delay); err != nil {
			return fmt.Errorf("discord: post to channel %s: %w", p.channelID, err)
		}
		delay = min(2*delay, p.maxBackoff)
	}
}

// send makes one attempt at posting msg.
func (p *Poster) send(ctx context.Context, msg Message) error {
	endpoint := discordgo.EndpointChannelMessages(p.channelID)
	if len(msg.Components) > 0 {
		body := componentsBody{Flags: flagComponentsV2, Components: msg.Components}
		_, err := p.session.RequestWithBucketID(http.MethodPost, endpoint, body, endpoint, discordgo.WithContext(ctx))
		return err
	}
	_, err := p.session.ChannelMessageSendComplex(p.channelID, msg.send(), discordgo.WithContext(ctx))
	return err
}

// A FatalError reports a rejection retrying cannot fix: a token Discord
// refuses, or a channel the bot cannot see or write to. The relay logs it and
// exits non-zero.
type FatalError struct {
	// Op is the call that was rejected, "post" or "channel check".
	Op string
	// Status is the HTTP status Discord answered with, or 0 when the
	// rejection did not come from a response.
	Status int
	// Hint says what an operator has to change.
	Hint string
	Err  error
}

func (e *FatalError) Error() string {
	msg := "discord: " + e.Op + ": " + e.Err.Error()
	if e.Hint != "" {
		msg += " (" + e.Hint + ")"
	}
	return msg
}

func (e *FatalError) Unwrap() error { return e.Err }

// A GaveUpError reports that the retry budget ran out on one embed. The
// killmail is dropped, not recorded as posted, and the error log is the only
// record of it.
type GaveUpError struct {
	Attempts int
	Elapsed  time.Duration
	Err      error
}

func (e *GaveUpError) Error() string {
	return fmt.Sprintf("discord: gave up after %d attempts in %s: %v",
		e.Attempts, e.Elapsed.Round(time.Millisecond), e.Err)
}

func (e *GaveUpError) Unwrap() error { return e.Err }

// class says how an error from discordgo should be treated.
type class int

const (
	// classTransient errors may succeed on another attempt.
	classTransient class = iota
	// classFatal errors need an operator; the relay exits.
	classFatal
	// classPermanent errors will not change on a retry but say nothing about
	// the relay's configuration, so only this one call fails.
	classPermanent
)

// classify sorts an error from discordgo and reports the HTTP status behind
// it, or 0 when there was no response.
func classify(err error) (class, int) {
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) {
		status := 0
		if restErr.Response != nil {
			status = restErr.Response.StatusCode
		}
		switch {
		case status == http.StatusUnauthorized,
			status == http.StatusForbidden,
			status == http.StatusNotFound:
			return classFatal, status
		case status == http.StatusTooManyRequests, status >= 500:
			return classTransient, status
		default:
			return classPermanent, status
		}
	}
	// Everything else is a dial failure, a timeout, a *discordgo.RateLimitError
	// for a 429 discordgo declined to wait out, or its own wrapper around a
	// 502 it ran out of retries on. None of those say the next attempt will
	// fail too.
	return classTransient, 0
}

// hintFor returns what an operator has to change to get past a fatal status.
func (p *Poster) hintFor(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "Discord rejected the bot token"
	case http.StatusForbidden, http.StatusNotFound:
		return "bot needs " + strings.Join(requiredPermissionNames(p.attachFiles), ", ") +
			" on the channel, and the channel ID must name a channel it can see"
	}
	return ""
}

// sleepContext waits for d, or until ctx is done.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// rewriteTransport sends every request to another host, keeping its path. It
// backs WithBaseURL.
type rewriteTransport struct {
	scheme string
	host   string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.scheme
	clone.URL.Host = t.host
	clone.Host = ""
	return http.DefaultTransport.RoundTrip(clone)
}
