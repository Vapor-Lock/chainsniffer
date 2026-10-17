// Package relay is the pipeline between the Wanderer stream and Discord: it
// turns one SSE event into posted Discord messages and advances the cursor the
// stream resumes from.
//
// Handle is what internal/wanderer calls for every event, in stream order, and
// it dispatches on the event type. For each killmail a map_kill event carries
// it skips the ones already posted, applies the filters, fills in the
// corporation and alliance names the payload left out, resolves the system
// name and the chain alias, renders the card in the configured style and
// posts it, recording the kill as posted only once Discord has accepted it. A
// topology event (deleted_system, connection_added, connection_removed,
// system_metadata_changed) goes to the [Topology], when one is configured,
// which keeps the map graph the linked-system filter reads and the chain
// aliases the [Aliases] answer from. Once the whole event is handled — and
// not before — the cursor moves to the event's ID and the state is written to
// disk, whatever the event's type.
//
// That order is what makes the relay at-least-once on the stream and
// at-most-once on Discord: a crash halfway through an event replays the event
// on the next start, and the posted-killmail record in the state suppresses
// the kills that already went out. See the "Processing order and cursor
// semantics" section of openspec/changes/archive/2026-10-14-add-killmail-relay-bot/design.md.
//
// The relay composes the other packages through the interfaces below,
// which it declares itself, so its pipeline can be tested with fakes and
// without a network.
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"sync"
	"time"

	"github.com/Vapor-Lock/chainsniffer/internal/cardimage"
	"github.com/Vapor-Lock/chainsniffer/internal/discord"
	"github.com/Vapor-Lock/chainsniffer/internal/killmail"
	"github.com/Vapor-Lock/chainsniffer/internal/universe"
	"github.com/Vapor-Lock/chainsniffer/internal/wanderer"
)

// FilterDuplicate is the reason logged for a killmail that is dropped because
// the state already records it as posted. The filters in internal/killmail
// name themselves; duplicate suppression lives here, since it is the state,
// not a filter, that remembers.
const FilterDuplicate = "duplicate"

// Wanderer's SSE event types that change the map topology or a system's chain
// alias. Handle passes them to the [Topology]; the wiring subscribes to them
// alongside [killmail.EventMapKill].
const (
	EventDeletedSystem         = "deleted_system"
	EventConnectionAdded       = "connection_added"
	EventConnectionRemoved     = "connection_removed"
	EventSystemMetadataChanged = "system_metadata_changed"
)

// Poster posts one rendered killmail to the Discord channel.
//
// It is internal/discord's poster, which owns Discord's rate limits and the
// retry budget: by the time Post returns an error it has either given up on
// that one killmail, which costs the killmail alone, or hit something an
// operator has to fix — see [IsFatalPost].
type Poster interface {
	Post(ctx context.Context, msg discord.Message) error
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

// Topology is the map graph the linked-system filter reads, kept current from
// the stream's topology and metadata events.
//
// It is internal/topology's graph. Apply decodes the payload itself and logs
// and ignores one it cannot use, so it cannot fail the event.
type Topology interface {
	Apply(eventType string, payload json.RawMessage)
}

// Aliases answers a solar system's chain alias, the temporary name the map
// gives it, for the embed's System row.
//
// It is internal/topology's graph, the same one the [Topology] events go to.
// Alias returns "" for a system with no alias or not on the map.
type Aliases interface {
	Alias(id int64) string
}

// Affiliations resolves corporation and alliance IDs to the names and tickers
// an embed shows, for the parties whose payload carried the ID alone.
//
// It is internal/universe's resolver, backed by the names.json cache. A lookup
// that fails returns false, and the embed leaves that part out.
type Affiliations interface {
	Corporation(ctx context.Context, id int64) (universe.Entity, bool)
	Alliance(ctx context.Context, id int64) (universe.Entity, bool)
}

// Images fetches the portraits and logos the strip card style draws.
//
// It is internal/eveimage's fetcher. The URL methods return "" for an ID of
// zero or less, and Fetch returns false for the empty URL and for an image it
// could not fetch or decode, which the strip draws as a blank tile.
type Images interface {
	CharacterPortrait(id int64, size int) string
	CorporationLogo(id int64, size int) string
	AllianceLogo(id int64, size int) string
	Fetch(ctx context.Context, url string) (image.Image, bool)
}

// The sizes the strip's images are fetched at: the portrait and logo tiles
// cardimage draws them into.
const (
	portraitSize = 128
	logoSize     = 64
)

// imageTimeout bounds all of one strip's image fetches together, which run
// concurrently, so a slow image server delays a card by about this much and
// the strip then draws blank tiles.
const imageTimeout = 3 * time.Second

// affiliationTimeout bounds all of one kill's affiliation lookups together,
// which run concurrently, so a cold kill waits about one lookup at most.
const affiliationTimeout = universe.DefaultTimeout

// Relay is the pipeline. The zero value is not usable; call [New].
type Relay struct {
	poster   Poster
	resolver Resolver
	store    Store
	topology Topology
	aliases  Aliases
	style    discord.CardStyle
	affils   Affiliations
	images   Images

	friendlies killmail.Friendlies
	filters    killmail.Filters
	logger     *slog.Logger
	fatalPost  func(error) bool
	// drawStrip is cardimage.Strip, replaced in tests to make it fail.
	drawStrip func(cardimage.StripInput) ([]byte, error)
}

type options struct {
	topology   Topology
	aliases    Aliases
	style      discord.CardStyle
	affils     Affiliations
	images     Images
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

// WithTopology sets the graph topology events are applied to. Without one,
// topology events are ignored, though they still advance the cursor.
func WithTopology(t Topology) Option {
	return func(o *options) { o.topology = t }
}

// WithAliases sets where the chain alias of a posted kill's system is looked
// up. Without one, or with a nil one, no embed shows an alias.
func WithAliases(a Aliases) Option {
	return func(o *options) { o.aliases = a }
}

// WithCardStyle sets the layout every kill card is rendered in. Without it,
// cards use [discord.CardStyleBlock].
func WithCardStyle(s discord.CardStyle) Option {
	return func(o *options) { o.style = s }
}

// WithAffiliations sets where the corporation and alliance names and tickers a
// payload left out are looked up. Without one, or with a nil one, an embed
// shows only the names the payload carried.
func WithAffiliations(a Affiliations) Option {
	return func(o *options) { o.affils = a }
}

// WithImages sets where the strip card style's portraits and logos are
// fetched. Without one, or with a nil one, a strip draws every tile blank.
func WithImages(i Images) Option {
	return func(o *options) { o.images = i }
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

	o := options{style: discord.CardStyleBlock, logger: slog.Default(), fatalPost: IsFatalPost}
	for _, opt := range opts {
		opt(&o)
	}

	return &Relay{
		poster:     poster,
		resolver:   resolver,
		store:      store,
		topology:   o.topology,
		aliases:    o.aliases,
		style:      o.style,
		affils:     o.affils,
		images:     o.images,
		friendlies: o.friendlies,
		filters:    o.filters,
		logger:     o.logger,
		fatalPost:  o.fatalPost,
		drawStrip:  cardimage.Strip,
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
// killmail Discord ultimately refused, a topology event — is logged or
// applied, and the cursor advances past it so that one bad kill cannot wedge
// the stream and a restart does not replay topology already applied.
func (r *Relay) Handle(ctx context.Context, env wanderer.Envelope) error {
	switch env.Type {
	case killmail.EventMapKill:
		for _, k := range killmail.Extract(env.Payload, r.logger) {
			if err := r.handle(ctx, k); err != nil {
				return err
			}
		}
	case EventDeletedSystem, EventConnectionAdded, EventConnectionRemoved, EventSystemMetadataChanged:
		if r.topology == nil {
			r.logger.Debug("ignoring topology event without a topology",
				"event_id", env.ID, "event_type", env.Type)
			break
		}
		r.topology.Apply(env.Type, env.Payload)
	default:
		// The subscription names only the types above, so anything else is a
		// stream the relay does not understand rather than one it is dropping.
		r.logger.Debug("ignoring event", "event_id", env.ID, "event_type", env.Type)
	}
	r.advance(env.ID)
	return nil
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

	// Classification and the filters above judged the payload alone; the
	// names filled in here are display text only.
	r.enrich(ctx, &k)
	name := r.resolver.Name(ctx, k.SolarSystemID)
	alias := r.alias(k.SolarSystemID)
	if err := r.poster.Post(ctx, r.message(ctx, k, class, name, alias)); err != nil {
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
	attrs := []any{"killmail_id", k.ID, "classification", string(class), "solar_system", name}
	if alias != "" {
		attrs = append(attrs, "chain_alias", alias)
	}
	r.logger.Info("posted killmail", attrs...)
	return nil
}

// message builds the card for one kill in the configured style.
func (r *Relay) message(ctx context.Context, k killmail.Killmail, class killmail.Classification, name, alias string) discord.Message {
	switch r.style {
	case discord.CardStyleComponents:
		return discord.ComponentsMessage(k, class, name, alias)
	case discord.CardStyleStrip:
		embed := discord.Render(k, class, name, alias, discord.CardStyleBlock)
		png, err := r.drawStrip(r.stripInput(ctx, k))
		if err != nil {
			// The strip is decoration: the block card says everything it does.
			r.logger.Warn("posting the block card without its strip", "killmail_id", k.ID, "error", err)
			return discord.Message{Embed: embed}
		}
		return discord.StripMessage(embed, png)
	default:
		return discord.Message{Embed: discord.Render(k, class, name, alias, r.style)}
	}
}

// Text fallbacks for the strip, matching the cards' own.
const (
	unknownPilot = "Unknown pilot"
	unknownShip  = "Unknown ship"
)

// stripInput is what the strip draws for k: each party's text, with the
// fallbacks applied, and their portrait and logos, fetched concurrently under
// one context bounded by imageTimeout. Only IDs the payload gives are fetched;
// a missing or failed image is nil, a blank tile.
func (r *Relay) stripInput(ctx context.Context, k killmail.Killmail) cardimage.StripInput {
	in := cardimage.StripInput{
		Victim: cardimage.Party{
			Name: orElse(orElse(k.Victim.CharName, k.Victim.CorpName), unknownPilot),
			Hull: orElse(k.Victim.ShipName, unknownShip),
		},
		Attacker: cardimage.Party{
			// A final blow without a character is an NPC, named by its ship.
			Name: orElse(k.FinalBlow.CharName, k.FinalBlow.ShipName),
			Hull: orElse(k.FinalBlow.ShipName, unknownShip),
		},
	}
	if r.images == nil {
		return in
	}
	ctx, cancel := context.WithTimeout(ctx, imageTimeout)
	defer cancel()

	var wg sync.WaitGroup
	fetch := func(url string, dst *image.Image) {
		if url == "" {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if img, ok := r.images.Fetch(ctx, url); ok {
				*dst = img
			}
		}()
	}
	for _, side := range []struct {
		p   killmail.Party
		dst *cardimage.Party
	}{{k.Victim, &in.Victim}, {k.FinalBlow, &in.Attacker}} {
		fetch(r.images.CharacterPortrait(side.p.CharID, portraitSize), &side.dst.Portrait)
		fetch(r.images.CorporationLogo(side.p.CorpID, logoSize), &side.dst.Corp)
		fetch(r.images.AllianceLogo(side.p.AllianceID, logoSize), &side.dst.Alliance)
	}
	wg.Wait()
	return in
}

// orElse returns s, or fallback when s is empty.
func orElse(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// enrich fills in the corporation and alliance names and tickers that the
// victim and the final blow carry an ID for but no name or ticker, without an
// [Affiliations] doing nothing. Only empty fields are filled, and only IDs the
// payload gives are looked up: an AllianceID of 0 means the pilot had no
// alliance at kill time, and stays that way. The lookups run concurrently
// under one context bounded by affiliationTimeout; one that fails leaves its
// fields empty.
func (r *Relay) enrich(ctx context.Context, k *killmail.Killmail) {
	if r.affils == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, affiliationTimeout)
	defer cancel()

	var wg sync.WaitGroup
	fill := func(lookup func(context.Context, int64) (universe.Entity, bool), id int64, name, ticker *string) {
		if id <= 0 || (*name != "" && *ticker != "") {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, ok := lookup(ctx, id)
			if !ok {
				return
			}
			if *name == "" {
				*name = e.Name
			}
			if *ticker == "" {
				*ticker = e.Ticker
			}
		}()
	}
	for _, p := range []*killmail.Party{&k.Victim, &k.FinalBlow} {
		fill(r.affils.Corporation, p.CorpID, &p.CorpName, &p.CorpTicker)
		fill(r.affils.Alliance, p.AllianceID, &p.AllianceName, &p.AllianceTicker)
	}
	wg.Wait()
}

// alias is the chain alias of a solar system, or "" without an [Aliases].
func (r *Relay) alias(id int64) string {
	if r.aliases == nil {
		return ""
	}
	return r.aliases.Alias(id)
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
