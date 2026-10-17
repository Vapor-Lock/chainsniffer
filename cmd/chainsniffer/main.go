// Command chainsniffer relays killmails from one Wanderer map's event stream
// to one Discord channel.
//
// Everything it needs comes from the environment, which internal/config reads
// and validates; the command itself only wires the packages together, owns the
// process' signals, and turns what went wrong into an exit status:
//
//	0  a clean shutdown on SIGINT or SIGTERM
//	1  a failure an operator has to look at: a rejected token, a channel the
//	   bot cannot post to, a map whose stream or topology Wanderer refuses,
//	   an unusable state directory
//	2  a configuration the relay refuses to start with, with every bad
//	   variable named on standard error
//
// See the "Structured logging" and "Graceful shutdown" requirements of
// openspec/changes/archive/2026-10-14-add-killmail-relay-bot/specs/relay-runtime/spec.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Vapor-Lock/chainsniffer/internal/config"
	"github.com/Vapor-Lock/chainsniffer/internal/discord"
	"github.com/Vapor-Lock/chainsniffer/internal/eveimage"
	"github.com/Vapor-Lock/chainsniffer/internal/killmail"
	"github.com/Vapor-Lock/chainsniffer/internal/relay"
	"github.com/Vapor-Lock/chainsniffer/internal/state"
	"github.com/Vapor-Lock/chainsniffer/internal/topology"
	"github.com/Vapor-Lock/chainsniffer/internal/universe"
	"github.com/Vapor-Lock/chainsniffer/internal/wanderer"
)

// version is stamped into release builds with -ldflags "-X main.version=...".
var version = "dev"

// The process' exit statuses. A configuration error gets one of its own
// because it is the failure an operator can always fix from the outside, and
// because a container that exits 2 on every start is unambiguous.
const (
	exitOK      = 0
	exitFailure = 1
	exitConfig  = 2
)

// shutdownGrace is how long a Discord post that is already in flight may keep
// running after a signal before its context is cancelled too. The relay-runtime
// spec puts the bound at ten seconds.
const shutdownGrace = 10 * time.Second

func main() {
	os.Exit(run(context.Background(), os.Stderr, os.LookupEnv))
}

// options are the seams the end-to-end test replaces. They are not reachable
// from the environment: pointing the relay at another Discord or ESI is a
// testing affordance, not a setting.
type options struct {
	discordBaseURL string
	esiBaseURL     string
	imageBaseURL   string
}

// An option overrides one default in [run].
type option func(*options)

// withDiscordBaseURL sends the Discord REST calls to another API root.
func withDiscordBaseURL(url string) option {
	return func(o *options) { o.discordBaseURL = url }
}

// withESIBaseURL sends the ESI name lookups to another ESI root.
func withESIBaseURL(url string) option {
	return func(o *options) { o.esiBaseURL = url }
}

// withImageBaseURL fetches the strip's portraits and logos from another image
// server.
func withImageBaseURL(url string) option {
	return func(o *options) { o.imageBaseURL = url }
}

// run is main with its environment, its output and its lifetime passed in. It
// returns the process' exit status.
func run(ctx context.Context, stderr io.Writer, lookup config.LookupFunc, opts ...option) int {
	cfg, err := config.Load(lookup)
	if err != nil {
		// The log level and format are part of what failed to load, so this
		// one message goes to standard error unformatted.
		_, _ = fmt.Fprintf(stderr, "chainsniffer: %v\n", err)
		return exitConfig
	}

	var o options
	for _, opt := range opts {
		opt(&o)
	}

	logger := newLogger(stderr, cfg)
	// Logging the Config logs its redacted summary: both tokens report
	// themselves as "[redacted]" through slog.LogValuer.
	logger.Info("starting chainsniffer", "version", version, "config", cfg)

	if cfg.ReplayChannelID != "" {
		return replay(ctx, logger, cfg, o)
	}
	return serve(ctx, logger, cfg, o)
}

// serve runs the relay until a signal, a fatal failure or a cancelled ctx
// stops it, and reports the exit status that leaves.
func serve(ctx context.Context, logger *slog.Logger, cfg *config.Config, o options) int {
	store, err := state.Open(cfg.StateDir, state.WithLogger(logger))
	if err != nil {
		logger.Error("the state directory is unusable", "dir", cfg.StateDir, "error", err)
		return exitFailure
	}
	logger.Info("loaded the relay state", "path", store.Path(),
		"last_event_id", store.LastEventID(), "posted_killmails", len(store.PostedIDs()))

	// A signal stops the stream, but not the post in flight: streamCtx is what
	// the Wanderer client reads under, while postCtx outlives it by at most
	// shutdownGrace so that a killmail already on its way to Discord either
	// lands or is given up on before the process exits.
	streamCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	postCtx, cancelPosts := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelPosts()

	stopped := make(chan struct{})
	defer close(stopped)
	go grace(ctx, streamCtx, stopped, cancelPosts, logger)

	poster, err := newPoster(cfg, cfg.DiscordChannelID, logger, o)
	if err != nil {
		logger.Error("cannot build the Discord client", "error", err)
		return exitFailure
	}
	// The gateway session Startup opens is the relay's one long-lived Discord
	// connection, so it is closed on every way out of this function.
	defer func() {
		if err := poster.Close(); err != nil {
			logger.Warn("closing the Discord session failed", "error", err)
		}
	}()

	if err := poster.Startup(streamCtx); err != nil {
		return stop(logger, store, streamCtx, fmt.Errorf("the Discord startup check failed: %w", err))
	}

	filters := killmail.Filters{
		ExcludeNPC:   cfg.ExcludeNPC,
		MinValue:     cfg.MinValue,
		FriendlyOnly: cfg.FriendlyOnly,
		MaxKillAge:   cfg.MaxKillAge,
		KSpace:       kspace(cfg.KSpace),
	}
	relayOpts := cardOptions(cfg, logger, o)
	clientOpts := []wanderer.Option{wanderer.WithLogger(logger)}

	// The topology is always followed, since it is where the chain aliases
	// in the System row come from; a home system only adds the
	// linked-system filter on top of it.
	var client *wanderer.Client
	graph := topology.New(cfg.HomeSystemID, topology.WithLogger(logger))
	if cfg.HomeSystemID != 0 {
		filters.Linked = graph.Linked
	}
	relayOpts = append(relayOpts, relay.WithTopology(graph), relay.WithAliases(graph))
	// The snapshot on connect runs before the connection's first event is
	// handled, so no kill is judged or named against a topology that has not
	// been loaded. Its errors are the client's to classify: a
	// *wanderer.FatalError ends the run, anything else reconnects.
	clientOpts = append(clientOpts,
		wanderer.WithEventTypes(append([]string{wanderer.EventMapKill}, topology.EventTypes...)...),
		wanderer.WithConnectHook(func(ctx context.Context) error {
			return graph.Resync(ctx, snapshotFetcher(client))
		}))
	relayOpts = append(relayOpts, relay.WithFilters(filters))

	// Corporation and alliance names never change in EVE, so every one the
	// resolver learns is kept in names.json and asked of ESI once, ever. The
	// file is a cache only: OpenNames moves a corrupt one aside and starts
	// empty, and it never touches state.json.
	names, err := state.OpenNames(cfg.StateDir, state.WithLogger(logger))
	if err != nil {
		logger.Error("the state directory is unusable", "dir", cfg.StateDir, "error", err)
		return exitFailure
	}
	resolver := newResolver(logger, o, universe.WithNameCache(names))
	// The resolver that names a kill's system also answers its security
	// status, from the same cached ESI request.
	relayOpts = append(relayOpts, relay.WithAffiliations(resolver), relay.WithSecurities(resolver))

	// Every posted kill is kept in last_kill.json, for a test build to replay
	// (see replay).
	lastKill, err := state.OpenLastKill(cfg.StateDir, state.WithLogger(logger))
	if err != nil {
		logger.Error("the state directory is unusable", "dir", cfg.StateDir, "error", err)
		return exitFailure
	}
	relayOpts = append(relayOpts, relay.WithLastKill(lastKill))

	relayer, err := relay.New(poster, resolver, store, relayOpts...)
	if err != nil {
		logger.Error("cannot build the relay", "error", err)
		return exitFailure
	}

	client, err = wanderer.New(cfg.WandererURL, cfg.WandererMap, cfg.WandererToken.Reveal(), clientOpts...)
	if err != nil {
		logger.Error("cannot build the Wanderer client", "error", err)
		return exitFailure
	}

	resyncCtx, stopResync := context.WithCancel(streamCtx)
	var resyncing sync.WaitGroup
	resyncing.Add(1)
	go func() {
		defer resyncing.Done()
		resync(resyncCtx, logger, graph, snapshotFetcher(client), cfg.TopologyResyncInterval)
	}()
	defer func() {
		stopResync()
		resyncing.Wait()
	}()

	// store.LastEventID is read before every connection attempt, so a
	// reconnect resumes from the newest event the relay has finished.
	return stop(logger, store, streamCtx, client.Run(streamCtx, store.LastEventID, relayer.Handler(postCtx)))
}

// replay posts the last kill a relay saved in the state directory to the
// replay channel, built the way this build builds a live kill's card, and
// reports how that went. It never subscribes to the stream, posts to the
// relay's channel or changes state.json, so it is safe beside a running relay
// on the same state directory.
func replay(ctx context.Context, logger *slog.Logger, cfg *config.Config, o options) int {
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	lastKill, err := state.OpenLastKill(cfg.StateDir, state.WithLogger(logger))
	if err != nil {
		logger.Error("the state directory is unusable", "dir", cfg.StateDir, "error", err)
		return exitFailure
	}
	saved, err := lastKill.Load()
	if err != nil {
		logger.Error("there is no kill to replay", "error", err)
		return exitFailure
	}
	// The relay needs a store, but Replay never reads or writes it.
	store, err := state.Open(cfg.StateDir, state.WithLogger(logger))
	if err != nil {
		logger.Error("the state directory is unusable", "dir", cfg.StateDir, "error", err)
		return exitFailure
	}
	names, err := state.OpenNames(cfg.StateDir, state.WithLogger(logger))
	if err != nil {
		logger.Error("the state directory is unusable", "dir", cfg.StateDir, "error", err)
		return exitFailure
	}

	poster, err := newPoster(cfg, cfg.ReplayChannelID, logger, o)
	if err != nil {
		logger.Error("cannot build the Discord client", "error", err)
		return exitFailure
	}
	defer func() {
		if err := poster.Close(); err != nil {
			logger.Warn("closing the Discord session failed", "error", err)
		}
	}()
	if err := poster.Startup(ctx); err != nil {
		logger.Error("the Discord startup check failed", "error", err)
		return exitFailure
	}

	// The chain alias comes from the map's current topology, loaded once. A
	// map that cannot be read only costs the alias.
	graph := topology.New(cfg.HomeSystemID, topology.WithLogger(logger))
	client, err := wanderer.New(cfg.WandererURL, cfg.WandererMap, cfg.WandererToken.Reveal(), wanderer.WithLogger(logger))
	if err != nil {
		logger.Error("cannot build the Wanderer client", "error", err)
		return exitFailure
	}
	if err := graph.Resync(ctx, snapshotFetcher(client)); err != nil {
		logger.Warn("loading the map topology failed, replaying without a chain alias", "error", err)
	}

	resolver := newResolver(logger, o, universe.WithNameCache(names))
	relayer, err := relay.New(poster, resolver, store, append(cardOptions(cfg, logger, o),
		relay.WithAliases(graph), relay.WithAffiliations(resolver))...)
	if err != nil {
		logger.Error("cannot build the relay", "error", err)
		return exitFailure
	}
	if err := relayer.Replay(ctx, saved.KillmailID, saved.Payload); err != nil {
		logger.Error("the replay failed", "error", err)
		return exitFailure
	}
	return exitOK
}

// kspace converts the configured security classes into the set the k-space
// filter reads.
func kspace(classes []config.SecurityClass) map[killmail.SecurityClass]bool {
	set := make(map[killmail.SecurityClass]bool, len(classes))
	for _, c := range classes {
		set[killmail.SecurityClass(c)] = true
	}
	return set
}

// cardOptions are the relay options that decide how a kill's card looks:
// its classification, its style, and for the strip style its images.
func cardOptions(cfg *config.Config, logger *slog.Logger, o options) []relay.Option {
	opts := []relay.Option{
		relay.WithFriendlies(killmail.NewFriendlies(cfg.FriendlyCorpIDs, cfg.FriendlyAllianceIDs)),
		relay.WithCardStyle(discord.CardStyle(cfg.CardStyle)),
		relay.WithLogger(logger),
	}
	if cfg.CardStyle == config.CardStyleStrip {
		opts = append(opts, relay.WithImages(newImages(logger, o)))
	}
	return opts
}

// snapshotFetcher loads the map's topology and chain aliases through client, in the shape the
// graph installs.
func snapshotFetcher(client *wanderer.Client) func(context.Context) (topology.Snapshot, error) {
	return func(ctx context.Context) (topology.Snapshot, error) {
		s, err := client.Snapshot(ctx)
		if err != nil {
			return topology.Snapshot{}, err
		}
		out := topology.Snapshot{
			Systems:     s.Systems,
			Connections: make([][2]int64, 0, len(s.Connections)),
			Aliases:     s.TemporaryNames,
		}
		for _, c := range s.Connections {
			out.Connections = append(out.Connections, [2]int64{c.Source, c.Target})
		}
		return out, nil
	}
}

// resync reloads the topology every interval until ctx ends, healing any
// change the stream's events missed. A failed reload, fatal or not, is only a
// warning: the graph keeps its current topology, and the next connection's
// snapshot is where a rejected token stops the relay.
func resync(ctx context.Context, logger *slog.Logger, graph *topology.Graph,
	fetch func(context.Context) (topology.Snapshot, error), interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := graph.Resync(ctx, fetch); err != nil && ctx.Err() == nil {
			logger.Warn("reloading the map topology failed, keeping the current one",
				"interval", interval, "error", err)
		}
	}
}

// stop reports how the run ended: a shutdown asked for from the outside is a
// success, anything else is a failure. Either way the state is written one last
// time, since a killmail posted during the final event is only recorded as
// posted in memory until it is.
func stop(logger *slog.Logger, store *state.Store, streamCtx context.Context, err error) int {
	code := exitFailure
	switch {
	case err == nil:
		// The stream loop only returns for a reason; reaching here means a
		// future change stopped it cleanly on its own.
		logger.Info("the Wanderer stream loop finished")
		code = exitOK
	case streamCtx.Err() != nil && errors.Is(err, streamCtx.Err()):
		logger.Info("shutting down", "last_event_id", store.LastEventID())
		code = exitOK
	default:
		logger.Error("the relay stopped", "error", err)
	}

	if err := store.Save(); err != nil {
		logger.Error("saving the relay state failed", "path", store.Path(), "error", err)
		code = exitFailure
	}
	return code
}

// grace cancels the posting context once the stream has been told to stop:
// at once when the caller cancelled ctx, since then nobody is waiting for the
// post to land, and after shutdownGrace when a signal arrived, which is the
// window the in-flight post gets to finish in.
//
// It returns as soon as the run is over, so a relay that stopped on its own
// does not keep a timer alive for ten seconds.
func grace(ctx, streamCtx context.Context, stopped <-chan struct{}, cancelPosts func(), logger *slog.Logger) {
	select {
	case <-stopped:
		return
	case <-streamCtx.Done():
	}
	if ctx.Err() != nil {
		cancelPosts()
		return
	}

	logger.Info("signal received, finishing the post in flight", "grace_period", shutdownGrace)
	timer := time.NewTimer(shutdownGrace)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-stopped:
	}
	cancelPosts()
}

// newLogger builds the process logger from the configured level and format.
// Everything goes to standard error, which keeps the logs out of a pipe
// reading the process' standard output.
func newLogger(w io.Writer, cfg *config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == config.LogFormatJSON {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

// newPoster builds the Discord poster that posts to channelID.
func newPoster(cfg *config.Config, channelID string, logger *slog.Logger, o options) (*discord.Poster, error) {
	// Only the strip style uploads a file, so only it needs Attach Files.
	opts := []discord.Option{
		discord.WithLogger(logger),
		discord.WithAttachFiles(cfg.CardStyle == config.CardStyleStrip),
	}
	if o.discordBaseURL != "" {
		opts = append(opts, discord.WithBaseURL(o.discordBaseURL))
	}
	return discord.New(cfg.DiscordToken.Reveal(), channelID, opts...)
}

// newResolver builds the ESI resolver the embeds' system, corporation and
// alliance names come from.
func newResolver(logger *slog.Logger, o options, extra ...universe.Option) *universe.Resolver {
	opts := append([]universe.Option{universe.WithLogger(logger)}, extra...)
	if o.esiBaseURL != "" {
		opts = append(opts, universe.WithBaseURL(o.esiBaseURL))
	}
	return universe.NewResolver(opts...)
}

// newImages builds the EVE image fetcher the strip's portraits and logos come
// from.
func newImages(logger *slog.Logger, o options) *eveimage.Fetcher {
	opts := []eveimage.Option{eveimage.WithLogger(logger)}
	if o.imageBaseURL != "" {
		opts = append(opts, eveimage.WithBaseURL(o.imageBaseURL))
	}
	return eveimage.New(opts...)
}
