// Package topology keeps the Wanderer map's connection graph, the set of
// solar systems linked to the configured home system, and each system's chain
// alias (its Wanderer temporary name).
//
// A system is linked when the map's connections form a path from home to it.
// Connections are undirected and keyed by their unordered pair of systems, not
// by Wanderer's connection ID: the snapshot and the events both carry the two
// system IDs, Wanderer holds at most one connection per pair, and the
// connections a system deletion takes with it are never reported by ID (see
// openspec/changes/archive/2026-10-16-add-linked-system-filter/design.md).
//
// The graph changes two ways. Apply handles the stream's topology events, and
// a resync (BeginResync, then Install) replaces the whole graph with a REST
// snapshot. Every connection change re-runs a breadth-first search from home,
// which on a map of at most a few hundred systems costs microseconds. A graph
// built without a home system (New(0)) keeps the connections and the aliases
// but links nothing.
//
// Event payloads are internal Elixir maps rather than a versioned schema, so
// they are decoded leniently: an event without the integer system IDs it needs
// is skipped with a warning, and unknown event types are ignored.
package topology

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// The stream event types Apply handles. Wanderer's add_system is deliberately
// not among them: a new system arrives without connections or a temporary
// name, so it changes nothing until a connection_added or a
// system_metadata_changed follows.
const (
	EventConnectionAdded       = "connection_added"
	EventConnectionRemoved     = "connection_removed"
	EventDeletedSystem         = "deleted_system"
	EventSystemMetadataChanged = "system_metadata_changed"
)

// EventTypes lists the event types Apply handles, for the stream subscription.
var EventTypes = []string{
	EventConnectionAdded, EventConnectionRemoved, EventDeletedSystem, EventSystemMetadataChanged,
}

// Snapshot is the map's whole topology as the REST API reports it.
type Snapshot struct {
	// Systems are the solar system IDs on the map.
	Systems []int64
	// Connections are the pairs of systems each connection joins, in either
	// order.
	Connections [][2]int64
	// Aliases are the systems' temporary names. Install trims them and drops
	// the blank ones.
	Aliases map[int64]string
}

// opKind is one of the set operations every change reduces to.
type opKind int

const (
	opAddPair opKind = iota
	opRemovePair
	// opRemoveSystem removes the system's connections and its alias.
	opRemoveSystem
	opSetAlias
	opClearAlias
)

// op is a change Apply made, kept so Install can replay it on a snapshot. Each
// is an absolute set operation, so replaying one the snapshot already reflects
// changes nothing.
type op struct {
	kind  opKind
	a, b  int64
	alias string
}

// Graph is the map's topology seen from a home system, or from none when home
// is 0. It is safe for concurrent use. The zero value is not usable; call New.
type Graph struct {
	home   int64
	logger *slog.Logger

	// resyncMu serializes Resync calls, so a connect snapshot and a periodic
	// one never overlap.
	resyncMu sync.Mutex

	mu  sync.RWMutex
	adj map[int64]map[int64]struct{}
	// linked is replaced, never mutated, on every recompute. It stays empty
	// without a home system.
	linked  map[int64]struct{}
	aliases map[int64]string
	// journal records the ops Apply makes while a resync is in flight; it is
	// nil when none is.
	journal []op
}

type options struct {
	logger *slog.Logger
}

// An Option configures a Graph at New time.
type Option func(*options)

// WithLogger sets the logger topology changes are reported on. A nil logger
// keeps slog.Default.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// New returns an empty graph in which only home is linked. A home of 0 builds
// a graph without a home system, in which nothing is linked: it tracks the
// connections and aliases but skips the linked-set search.
func New(home int64, opts ...Option) *Graph {
	o := options{logger: slog.Default()}
	for _, opt := range opts {
		opt(&o)
	}
	g := &Graph{
		home:    home,
		logger:  o.logger,
		adj:     make(map[int64]map[int64]struct{}),
		aliases: make(map[int64]string),
	}
	g.linked = g.reachable()
	return g
}

// Alias returns the system's chain alias, or "" when it has none.
func (g *Graph) Alias(id int64) string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.aliases[id]
}

// Linked reports whether the system is linked to home. Home itself always is,
// even without connections or when it is missing from the map; ID 0, what a
// kill without a known system carries, never is.
func (g *Graph) Linked(id int64) bool {
	if id == 0 {
		return false
	}
	if id == g.home {
		return true
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.linked[id]
	return ok
}

// Apply updates the graph from one stream event. connection_added and
// connection_removed add and remove the connection between
// `solar_system_source_id` and `solar_system_target_id`; deleted_system
// removes every connection touching `solar_system_id`, because Wanderer's
// cascade emits no connection_removed for them, and its alias.
// system_metadata_changed sets the alias of `solar_system_id` to its trimmed
// `temporary_name`, or clears it when that is null, missing or blank. Any
// other type is ignored.
func (g *Graph) Apply(eventType string, payload json.RawMessage) {
	var o op
	switch eventType {
	case EventConnectionAdded, EventConnectionRemoved:
		fields, ok := g.fields(eventType, payload)
		if !ok {
			return
		}
		src, ok1 := fieldID(fields, "solar_system_source_id")
		dst, ok2 := fieldID(fields, "solar_system_target_id")
		if !ok1 || !ok2 {
			g.logger.Warn("ignoring topology event without integer system IDs", "type", eventType,
				"solar_system_source_id", string(fields["solar_system_source_id"]),
				"solar_system_target_id", string(fields["solar_system_target_id"]))
			return
		}
		o = op{kind: opAddPair, a: src, b: dst}
		if eventType == EventConnectionRemoved {
			o.kind = opRemovePair
		}
	case EventDeletedSystem:
		fields, ok := g.fields(eventType, payload)
		if !ok {
			return
		}
		id, ok := fieldID(fields, "solar_system_id")
		if !ok {
			g.logger.Warn("ignoring topology event without an integer system ID", "type", eventType,
				"solar_system_id", string(fields["solar_system_id"]))
			return
		}
		o = op{kind: opRemoveSystem, a: id}
	case EventSystemMetadataChanged:
		fields, ok := g.fields(eventType, payload)
		if !ok {
			return
		}
		id, ok := fieldID(fields, "solar_system_id")
		if !ok {
			g.logger.Warn("ignoring topology event without an integer system ID", "type", eventType,
				"solar_system_id", string(fields["solar_system_id"]))
			return
		}
		var name *string
		if raw, present := fields["temporary_name"]; present {
			if err := json.Unmarshal(raw, &name); err != nil {
				g.logger.Warn("ignoring topology event whose temporary_name is not a string",
					"type", eventType, "solar_system_id", id, "temporary_name", string(raw))
				return
			}
		}
		o = op{kind: opClearAlias, a: id}
		if name != nil {
			if alias := strings.TrimSpace(*name); alias != "" {
				o = op{kind: opSetAlias, a: id, alias: alias}
			}
		}
	default:
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	oldAlias := g.aliases[o.a]
	g.apply(o)
	if g.journal != nil {
		g.journal = append(g.journal, o)
	}
	if alias := g.aliases[o.a]; alias != oldAlias {
		g.logger.Debug("chain alias changed", "type", eventType, "solar_system_id", o.a,
			"alias", alias)
	}
	if o.kind == opSetAlias || o.kind == opClearAlias || g.home == 0 {
		return
	}
	old := g.linked
	g.linked = g.reachable()
	g.logDiff(eventType, old, g.linked)
}

// BeginResync starts journalling the changes Apply makes, so the next Install
// can replay them on top of its snapshot. Prefer Resync, which also keeps
// resyncs from overlapping.
func (g *Graph) BeginResync() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.journal == nil {
		g.journal = make([]op, 0)
	}
}

// AbortResync stops journalling without changing the graph, for a snapshot
// that failed to load.
func (g *Graph) AbortResync() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.journal = nil
}

// Install replaces the graph and the aliases with the snapshot's, replays
// every change Apply made since BeginResync, and recomputes the linked
// systems. It logs the system, linked and alias counts, and warns when home is
// not on the map.
func (g *Graph) Install(s Snapshot) {
	adj := make(map[int64]map[int64]struct{})
	for _, c := range s.Connections {
		addPair(adj, c[0], c[1])
	}
	aliases := make(map[int64]string, len(s.Aliases))
	for id, name := range s.Aliases {
		if alias := strings.TrimSpace(name); alias != "" {
			aliases[id] = alias
		}
	}
	systems := make(map[int64]struct{}, len(s.Systems))
	for _, id := range s.Systems {
		systems[id] = struct{}{}
	}

	g.mu.Lock()
	g.adj = adj
	g.aliases = aliases
	replayed := len(g.journal)
	for _, o := range g.journal {
		g.apply(o)
	}
	g.journal = nil
	g.linked = g.reachable()
	linked := len(g.linked)
	aliased := len(g.aliases)
	g.mu.Unlock()

	attrs := []any{"systems", len(systems)}
	if g.home != 0 {
		if _, ok := systems[g.home]; !ok {
			g.logger.Warn("home system is not on the map; only kills in it will pass",
				"home_system_id", g.home)
		}
		attrs = append(attrs, "linked_systems", linked)
	}
	attrs = append(attrs, "aliases", aliased, "replayed_events", replayed)
	g.logger.Info("loaded map topology", attrs...)
}

// Resync loads a snapshot with fetch and installs it, without losing the
// events applied while fetch runs. Concurrent calls run one at a time. When
// fetch fails, the graph is left as it was and its error is returned.
func (g *Graph) Resync(ctx context.Context, fetch func(context.Context) (Snapshot, error)) error {
	g.resyncMu.Lock()
	defer g.resyncMu.Unlock()

	g.BeginResync()
	s, err := fetch(ctx)
	if err != nil {
		g.AbortResync()
		return err
	}
	g.Install(s)
	return nil
}

// apply performs one op on g.adj and g.aliases. The caller holds g.mu.
func (g *Graph) apply(o op) {
	switch o.kind {
	case opAddPair:
		addPair(g.adj, o.a, o.b)
	case opRemovePair:
		removePair(g.adj, o.a, o.b)
	case opRemoveSystem:
		for n := range g.adj[o.a] {
			removePair(g.adj, o.a, n)
		}
		delete(g.adj, o.a)
		delete(g.aliases, o.a)
	case opSetAlias:
		g.aliases[o.a] = o.alias
	case opClearAlias:
		delete(g.aliases, o.a)
	}
}

// reachable returns the systems a breadth-first search from home reaches,
// home included, or none without a home system. The caller holds g.mu, or is
// New.
func (g *Graph) reachable() map[int64]struct{} {
	if g.home == 0 {
		return map[int64]struct{}{}
	}
	seen := map[int64]struct{}{g.home: {}}
	queue := []int64{g.home}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for n := range g.adj[id] {
			if _, ok := seen[n]; !ok {
				seen[n] = struct{}{}
				queue = append(queue, n)
			}
		}
	}
	return seen
}

// logDiff logs at debug level the systems that became linked or unlinked.
func (g *Graph) logDiff(eventType string, old, cur map[int64]struct{}) {
	gained, lost := diff(old, cur), diff(cur, old)
	if len(gained) == 0 && len(lost) == 0 {
		return
	}
	g.logger.Debug("linked systems changed", "type", eventType, "linked", gained, "unlinked", lost)
}

// fields decodes a payload into its top-level fields, warning when it is not a
// JSON object.
func (g *Graph) fields(eventType string, payload json.RawMessage) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
		g.logger.Warn("ignoring topology event whose payload is not a JSON object", "type", eventType,
			"error", err)
		return nil, false
	}
	return fields, true
}

// diff returns, sorted, the IDs in b that are not in a.
func diff(a, b map[int64]struct{}) []int64 {
	var out []int64
	for id := range b {
		if _, ok := a[id]; !ok {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

func addPair(adj map[int64]map[int64]struct{}, a, b int64) {
	if a == b {
		return
	}
	for _, e := range [][2]int64{{a, b}, {b, a}} {
		if adj[e[0]] == nil {
			adj[e[0]] = make(map[int64]struct{})
		}
		adj[e[0]][e[1]] = struct{}{}
	}
}

func removePair(adj map[int64]map[int64]struct{}, a, b int64) {
	for _, e := range [][2]int64{{a, b}, {b, a}} {
		delete(adj[e[0]], e[1])
		if len(adj[e[0]]) == 0 {
			delete(adj, e[0])
		}
	}
}

// fieldID reads a positive integer system ID. Like the kill payload decoder,
// it accepts a JSON number or a string holding one, and an integral float.
func fieldID(fields map[string]json.RawMessage, key string) (int64, bool) {
	var n json.Number
	if err := json.Unmarshal(fields[key], &n); err != nil || n == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(n.String(), 10, 64)
	if err != nil {
		f, ferr := strconv.ParseFloat(n.String(), 64)
		if ferr != nil || f != float64(int64(f)) {
			return 0, false
		}
		id = int64(f)
	}
	return id, id > 0
}
