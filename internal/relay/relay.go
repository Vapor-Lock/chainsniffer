// Package relay is the pipeline between the Wanderer stream and Discord: it
// turns one SSE event into posted Discord messages and advances the cursor the
// stream resumes from.
//
// Handle is what internal/wanderer calls for every map_kill event, in stream
// order. For each killmail the event carries it skips the ones already posted,
// applies the filters, resolves the system name, renders the embed and posts
// it, recording the kill as posted only once Discord has accepted it. Once the
// whole event is handled — and not before — the cursor moves to the event's ID
// and the state is written to disk.
//
// That order is what makes the relay at-least-once on the stream and
// at-most-once on Discord: a crash halfway through an event replays the event
// on the next start, and the posted-killmail record in the state suppresses
// the kills that already went out. See the "Processing order and cursor
// semantics" section of openspec/changes/add-killmail-relay-bot/design.md.
//
// The relay composes the other packages through the three interfaces below,
// which it declares itself, so its pipeline can be tested with fakes and
// without a network.
package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Vapor-Lock/chainsniffer/internal/discord"
	"github.com/Vapor-Lock/chainsniffer/internal/killmail"
	"github.com/Vapor-Lock/chainsniffer/internal/wanderer"
	"github.com/bwmarrin/discordgo"
)

// FilterDuplicate is the reason logged for a killmail that is dropped because
// the state already records it as posted. The filters in internal/killmail
// name themselves; duplicate suppression lives here, since it is the state,
// not a filter, that remembers.
const FilterDuplicate = "duplicate"

// Poster posts one rendered killmail to the Discord channel.
//
// It is internal/discord's poster, which owns Discord's rate limits and the
// retry budget: by the time Post returns an error it has either given up on
// that one killmail, which costs the killmail alone, or hit something an
// operator has to fix — see [IsFatalPost].
type Poster interface {
	Post(ctx context.Context, embed *discordgo.MessageEmbed) error
}

// Resolver resolves a solar system ID to the name an embed shows.
//
// It is internal/universe's resolver, and it does not fail: a system whose
// name could not be looked up resolves to its decimal ID, because a kill is
// still worth posting without it.
type Resolver interface {
	Name(ctx context.Context, id int64) string
}

// Store is the relay's durable progress: which killmails have already been
// posted, and which event to resume the stream from.
//
// It is internal/state's store. Save is called once per handled event, so an
// implementation is expected to write atomically.
type Store interface {
	Posted(id int64) bool
	MarkPosted(id int64)
	SetLastEventID(id string)
	Save() error
}

// Relay is the pipeline. The zero value is not usable; call [New].
type Relay struct {
	poster   Poster
	resolver Resolver
	store    Store

	friendlies killmail.Friendlies
	filters    killmail.Filters
	logger     *slog.Logger
	fatalPost  func(error) bool
}

type options struct {
	friendlies killmail.Friendlies
	filters    killmail.Filters
	logger     *slog.Logger
	fatalPost  func(error) bool
}

// An Option configures a Relay at [New] time.
type Option func(*options)

// WithFriendlies sets the corporations and alliances every killmail is
// classified against. The zero value classifies every killmail as neutral.
func WithFriendlies(f killmail.Friendlies) Option {
	return func(o *options) { o.friendlies = f }
}

// WithFilters sets the filter chain deciding which killmails are posted. The
// zero value posts every killmail; the relay's configured defaults differ.
func WithFilters(f killmail.Filters) Option {
	return func(o *options) { o.filters = f }
}

// WithLogger sets the logger the pipeline reports on. A nil logger keeps
// slog.Default.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithFatalPost replaces how a post error is classified, for a Poster other
// than internal/discord's. A nil fn keeps [IsFatalPost].
func WithFatalPost(fn func(error) bool) Option {
	return func(o *options) {
		if fn != nil {
			o.fatalPost = fn
		}
	}
}

// New returns a relay posting through poster, naming systems through resolver
// and keeping its progress in store. All three are required.
func New(poster Poster, resolver Resolver, store Store, opts ...Option) (*Relay, error) {
	switch {
	case poster == nil:
		return nil, errors.New("relay: a poster is required")
	case resolver == nil:
		return nil, errors.New("relay: a system name resolver is required")
	case store == nil:
		return nil, errors.New("relay: a state store is required")
	}

	o := options{logger: slog.Default(), fatalPost: IsFatalPost}
	for _, opt := range opts {
		opt(&o)
	}

	return &Relay{
		poster:     poster,
		resolver:   resolver,
		store:      store,
		friendlies: o.friendlies,
		filters:    o.filters,
		logger:     o.logger,
		fatalPost:  o.fatalPost,
	}, nil
}

// Handler returns the event handler to pass to [wanderer.Client.Run], bound to
// ctx. Run calls it synchronously in the stream's read loop, so the relay's
// own pace is the stream's backpressure.
func (r *Relay) Handler(ctx context.Context) func(wanderer.Envelope) error {
	return func(env wanderer.Envelope) error { return r.Handle(ctx, env) }
}

// Handle runs one event through the pipeline and then advances the cursor to
// its ID.
//
// It returns an error only for a condition that leaves the relay unable to
// work: a fatal Discord error, or ctx being done. In both cases the cursor is
// left where it was, so the event is replayed on the next run. Everything
// else — an event carrying no kills, a malformed entry, a filtered kill, a
// killmail Discord ultimately refused — is logged, and the cursor advances
// past it so that one bad kill cannot wedge the stream.
func (r *Relay) Handle(ctx context.Context, env wanderer.Envelope) error {
	for _, k := range r.killmails(env) {
		if err := r.handle(ctx, k); err != nil {
			return err
		}
	}
	r.advance(env.ID)
	return nil
}

// killmails decodes the killmails env carries, in payload order.
func (r *Relay) killmails(env wanderer.Envelope) []killmail.Killmail {
	if env.Type != killmail.EventMapKill {
		// Run only subscribes to map_kill, so anything else is a stream the
		// relay does not understand rather than a kill it is dropping.
		r.logger.Debug("ignoring event", "event_id", env.ID, "event_type", env.Type)
		return nil
	}
	return killmail.Extract(env.Payload, r.logger)
}

// handle runs one killmail through dedupe, the filters and Discord.
func (r *Relay) handle(ctx context.Context, k killmail.Killmail) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if r.store.Posted(k.ID) {
		r.dropped(k.ID, FilterDuplicate)
		return nil
	}

	class := r.friendlies.Classify(k)
	if allowed, filter := r.filters.Allow(k, class); !allowed {
		r.dropped(k.ID, filter)
		return nil
	}

	name := r.resolver.Name(ctx, k.SolarSystemID)
	if err := r.poster.Post(ctx, discord.Render(k, class, name)); err != nil {
		switch {
		case ctx.Err() != nil:
			// A post torn down by shutdown is not a post Discord refused, and
			// the kill has to stay unmarked: whether it went out is unknown,
			// and replaying the event is the lesser evil.
			return ctx.Err()
		case r.fatalPost(err):
			return fmt.Errorf("relay: posting killmail %d: %w", k.ID, err)
		default:
			// The poster exhausted its retry budget. The kill is lost and the
			// log line is the only record of it, but the stream keeps moving.
			r.logger.Error("giving up on a killmail Discord would not accept",
				"killmail_id", k.ID, "error", err)
			return nil
		}
	}

	// Only now, with Discord's acceptance in hand, is the kill a duplicate.
	r.store.MarkPosted(k.ID)
	r.logger.Info("posted killmail", "killmail_id", k.ID, "classification", string(class),
		"solar_system", name)
	return nil
}

// dropped logs a killmail that was not posted and the filter that decided so,
// which is the record the killmail-filtering spec asks for.
func (r *Relay) dropped(id int64, filter string) {
	r.logger.Debug("dropping killmail", "killmail_id", id, "filter", filter)
}

// advance moves the cursor to the ID of the event just handled and persists
// the state, which is the one state write per event.
//
// A failed write is logged and otherwise tolerated: the posted-killmail record
// is still correct in memory, so the relay keeps posting each kill once for as
// long as it lives, and only a restart would replay what the stale file no
// longer knows about. Stopping the relay over it would turn a degraded feed
// into no feed at all.
func (r *Relay) advance(eventID string) {
	if eventID != "" {
		r.store.SetLastEventID(eventID)
	}
	if err := r.store.Save(); err != nil {
		r.logger.Error("saving the relay state failed, continuing", "error", err)
	}
}

// IsFatalPost is the default classification of a post error: fatal when it
// is, or wraps, a [discord.FatalError] — a bot token Discord refuses, or a
// channel the bot cannot see or write to, which an operator has to fix.
//
// Every other error cost its killmail and nothing more: a
// [discord.GaveUpError] from the exhausted retry budget, or an embed Discord
// rejected outright. The relay logs those and steps over them.
func IsFatalPost(err error) bool {
	var fatal *discord.FatalError
	return errors.As(err, &fatal)
}
