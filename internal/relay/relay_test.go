package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Vapor-Lock/chainsniffer/internal/cardimage"
	"github.com/Vapor-Lock/chainsniffer/internal/discord"
	"github.com/Vapor-Lock/chainsniffer/internal/killmail"
	"github.com/Vapor-Lock/chainsniffer/internal/universe"
	"github.com/Vapor-Lock/chainsniffer/internal/wanderer"
	"github.com/bwmarrin/discordgo"
)

// The system every test killmail happens in, and the name the fake resolver
// answers with for it.
const (
	systemID   = 31000123
	systemName = "J123456"
)

// fakePoster records the messages it accepted, and their embeds, in order.
type fakePoster struct {
	messages []discord.Message
	embeds   []*discordgo.MessageEmbed

	// fail is consulted for every call, numbered from one. A non-nil error it
	// returns is what Post returns, and the embed is not recorded.
	fail  func(call int) error
	calls int
}

func (p *fakePoster) Post(_ context.Context, msg discord.Message) error {
	p.calls++
	if p.fail != nil {
		if err := p.fail(p.calls); err != nil {
			return err
		}
	}
	p.messages = append(p.messages, msg)
	p.embeds = append(p.embeds, msg.Embed)
	return nil
}

// ids reads the killmail IDs back out of the embeds the poster accepted, so a
// test can assert on what was posted without reaching into the embed layout.
func (p *fakePoster) ids(t *testing.T) []int64 {
	t.Helper()
	ids := make([]int64, 0, len(p.embeds))
	for _, embed := range p.embeds {
		path := strings.Trim(strings.TrimPrefix(embed.URL, "https://zkillboard.com/kill/"), "/")
		id, err := strconv.ParseInt(path, 10, 64)
		if err != nil {
			t.Fatalf("embed URL %q does not name a killmail ID: %v", embed.URL, err)
		}
		ids = append(ids, id)
	}
	return ids
}

// fakeResolver answers from a fixed table, falling back the way
// internal/universe does.
type fakeResolver struct {
	names map[int64]string
	asked []int64
}

func (r *fakeResolver) Name(_ context.Context, id int64) string {
	r.asked = append(r.asked, id)
	if name, ok := r.names[id]; ok {
		return name
	}
	return strconv.FormatInt(id, 10)
}

// savedState is one snapshot of what a Save call would have written.
type savedState struct {
	lastEventID string
	posted      []int64
}

// fakeStore keeps the relay's progress in memory and records every Save.
type fakeStore struct {
	posted      []int64
	seen        map[int64]struct{}
	lastEventID string
	saves       []savedState
	saveErr     error
}

func newFakeStore(posted ...int64) *fakeStore {
	s := &fakeStore{seen: make(map[int64]struct{})}
	for _, id := range posted {
		s.MarkPosted(id)
	}
	return s
}

func (s *fakeStore) Posted(id int64) bool {
	_, ok := s.seen[id]
	return ok
}

func (s *fakeStore) MarkPosted(id int64) {
	if _, ok := s.seen[id]; ok {
		return
	}
	s.seen[id] = struct{}{}
	s.posted = append(s.posted, id)
}

func (s *fakeStore) SetLastEventID(id string) { s.lastEventID = id }

func (s *fakeStore) Save() error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saves = append(s.saves, savedState{lastEventID: s.lastEventID, posted: slices.Clone(s.posted)})
	return nil
}

// fixture is a relay wired to fakes, with the fakes kept for assertions.
type fixture struct {
	relay    *Relay
	poster   *fakePoster
	resolver *fakeResolver
	store    *fakeStore
	logs     *bytes.Buffer
}

// newFixture builds a relay posting to a fake Discord, resolving one known
// system and logging at debug level into a buffer.
func newFixture(t *testing.T, opts ...Option) *fixture {
	t.Helper()

	f := &fixture{
		poster:   &fakePoster{},
		resolver: &fakeResolver{names: map[int64]string{systemID: systemName}},
		store:    newFakeStore(),
		logs:     &bytes.Buffer{},
	}
	logger := slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	relay, err := New(f.poster, f.resolver, f.store, append([]Option{WithLogger(logger)}, opts...)...)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	f.relay = relay
	return f
}

// handle runs one event through the relay, failing the test when it stops it.
func (f *fixture) handle(t *testing.T, env wanderer.Envelope) {
	t.Helper()
	if err := f.relay.Handle(context.Background(), env); err != nil {
		t.Fatalf("Handle(%q) = %v, want nil", env.ID, err)
	}
}

// envelope builds a map_kill event whose payload carries kills in that order.
func envelope(t *testing.T, id string, kills ...map[string]any) wanderer.Envelope {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"type":            killmail.PayloadKillmailUpdate,
		"solar_system_id": systemID,
		"killmails":       kills,
	})
	if err != nil {
		t.Fatalf("marshalling the payload = %v", err)
	}
	return wanderer.Envelope{ID: id, Type: killmail.EventMapKill, Payload: payload}
}

// kill is a killmail entry that passes the relay's configured filters, with
// each field applied on top.
func kill(id int64, fields ...map[string]any) map[string]any {
	entry := map[string]any{
		"killmail_id":      id,
		"kill_time":        time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
		"solar_system_id":  systemID,
		"victim_char_name": fmt.Sprintf("Pilot %d", id),
		"victim_ship_name": "Loki",
		"total_value":      1_000_000.0,
	}
	for _, extra := range fields {
		for k, v := range extra {
			entry[k] = v
		}
	}
	return entry
}

func wantPosted(t *testing.T, f *fixture, want ...int64) {
	t.Helper()
	if got := f.poster.ids(t); !slices.Equal(got, want) {
		t.Errorf("posted killmails = %v, want %v", got, want)
	}
}

// systemRow returns the value of the System row in the embed's monospace
// block, or "" when it has none.
func systemRow(embed *discordgo.MessageEmbed) string {
	for _, line := range strings.Split(embed.Description, "\n") {
		if value, ok := strings.CutPrefix(line, "System     "); ok {
			return value
		}
	}
	return ""
}

func TestHandlePostsEveryKillmailInOrder(t *testing.T) {
	f := newFixture(t)

	f.handle(t, envelope(t, "01JB", kill(1), kill(2), kill(3)))

	wantPosted(t, f, 1, 2, 3)
	if !slices.Equal(f.store.posted, []int64{1, 2, 3}) {
		t.Errorf("marked as posted = %v, want [1 2 3]", f.store.posted)
	}
	// The embed is rendered from the resolved system name, not the raw ID.
	if got := systemRow(f.poster.embeds[0]); got != systemName {
		t.Errorf("System row = %q, want %q", got, systemName)
	}
	if got := f.resolver.asked; !slices.Equal(got, []int64{systemID, systemID, systemID}) {
		t.Errorf("resolver asked for %v, want the system of all three kills", got)
	}
	if !strings.Contains(f.logs.String(), "posted killmail") {
		t.Error("a posted killmail is not reported at info level")
	}
}

func TestHandleAdvancesTheCursorOncePerEvent(t *testing.T) {
	f := newFixture(t)

	f.handle(t, envelope(t, "01JB", kill(1), kill(2)))

	// One write per event, with the cursor already at the event just handled
	// and both kills in it.
	want := []savedState{{lastEventID: "01JB", posted: []int64{1, 2}}}
	if len(f.store.saves) != 1 || f.store.saves[0].lastEventID != want[0].lastEventID ||
		!slices.Equal(f.store.saves[0].posted, want[0].posted) {
		t.Errorf("saves = %+v, want %+v", f.store.saves, want)
	}

	f.handle(t, envelope(t, "01JC", kill(3)))

	if len(f.store.saves) != 2 {
		t.Fatalf("saves after the second event = %d, want 2", len(f.store.saves))
	}
	if got := f.store.saves[1]; got.lastEventID != "01JC" || !slices.Equal(got.posted, []int64{1, 2, 3}) {
		t.Errorf("second save = %+v, want cursor 01JC and kills [1 2 3]", got)
	}
}

func TestHandlePostsADuplicateOnlyOnce(t *testing.T) {
	// Killmail 7 is in the persisted record from an earlier run, the kind of
	// state a restart with a backfilled event starts from.
	f := newFixture(t)
	f.store.MarkPosted(7)

	// 1 appears twice in one event, and 7 was posted before this run.
	f.handle(t, envelope(t, "01JB", kill(1), kill(7), kill(1)))
	// A reconnect backfills the same event, then a later event repeats 1.
	f.handle(t, envelope(t, "01JB", kill(1), kill(7)))
	f.handle(t, envelope(t, "01JC", kill(1), kill(2)))

	wantPosted(t, f, 1, 2)
	if got := strings.Count(f.logs.String(), "filter="+FilterDuplicate); got != 5 {
		t.Errorf("duplicate drops logged = %d, want 5", got)
	}
}

func TestHandleAppliesTheFiltersAndLogsTheDecision(t *testing.T) {
	friendlies := killmail.NewFriendlies([]int64{98000001}, nil)
	filters := killmail.Filters{
		ExcludeNPC:   true,
		MinValue:     10_000_000,
		FriendlyOnly: true,
		MaxKillAge:   time.Hour,
	}
	f := newFixture(t, WithFriendlies(friendlies), WithFilters(filters))

	friendly := map[string]any{"victim_corp_id": 98000001}
	rich := map[string]any{"total_value": 50_000_000.0}
	f.handle(t, envelope(t, "01JB",
		kill(1, friendly, rich),
		kill(2, friendly, rich, map[string]any{"npc": true}),
		kill(3, friendly),
		kill(4, rich),
		kill(5, friendly, rich, map[string]any{
			"kill_time": time.Now().UTC().Add(-5 * time.Hour).Format(time.RFC3339),
		}),
	))

	wantPosted(t, f, 1)
	for _, want := range []struct {
		id     int64
		filter string
	}{
		{2, killmail.FilterNPC},
		{3, killmail.FilterMinValue},
		{4, killmail.FilterFriendlyOnly},
		{5, killmail.FilterMaxKillAge},
	} {
		line := fmt.Sprintf("killmail_id=%d filter=%s", want.id, want.filter)
		if !strings.Contains(f.logs.String(), line) {
			t.Errorf("logs do not name the filter that dropped killmail %d (want %q)", want.id, line)
		}
	}
	// A dropped killmail costs nothing but its log line: the cursor still
	// moves past the event.
	if f.store.lastEventID != "01JB" {
		t.Errorf("cursor = %q, want 01JB", f.store.lastEventID)
	}
}

func TestHandleRendersTheClassification(t *testing.T) {
	f := newFixture(t, WithFriendlies(killmail.NewFriendlies([]int64{98000001}, nil)))

	f.handle(t, envelope(t, "01JB",
		kill(1, map[string]any{"victim_corp_id": 98000001}),
		kill(2, map[string]any{"final_blow_corp_id": 98000001}),
		kill(3),
	))

	for i, want := range []int{discord.ColorLoss, discord.ColorKill, discord.ColorNeutral} {
		if got := f.poster.embeds[i].Color; got != want {
			t.Errorf("embed %d color = %#x, want %#x", i+1, got, want)
		}
	}
	if !strings.Contains(f.logs.String(), "classification="+string(killmail.Loss)) {
		t.Error("the posted-killmail log line does not name the classification")
	}
}

func TestHandleSkipsAKillmailDiscordRefused(t *testing.T) {
	f := newFixture(t)
	// The poster gave up on the first kill after exhausting its retry budget.
	exhausted := &discord.GaveUpError{
		Attempts: 7,
		Elapsed:  5 * time.Minute,
		Err:      errors.New("502 Bad Gateway"),
	}
	f.poster.fail = func(call int) error {
		if call == 1 {
			return exhausted
		}
		return nil
	}

	f.handle(t, envelope(t, "01JB", kill(1), kill(2)))

	wantPosted(t, f, 2)
	// Unmarked, so a replay of the event gets another chance at it, and the
	// error log is the record that it was lost.
	if f.store.Posted(1) {
		t.Error("Posted(1) = true, want the refused killmail left unmarked")
	}
	if !strings.Contains(f.logs.String(), "level=ERROR") ||
		!strings.Contains(f.logs.String(), "killmail_id=1") {
		t.Errorf("the refused killmail is not reported at error level with its ID: %s", f.logs)
	}
	// One undeliverable kill must not wedge the stream.
	if f.store.lastEventID != "01JB" {
		t.Errorf("cursor = %q, want 01JB", f.store.lastEventID)
	}
}

func TestHandleStopsOnAFatalPostError(t *testing.T) {
	f := newFixture(t)
	fatal := &discord.FatalError{
		Op:     "post",
		Status: http.StatusForbidden,
		Hint:   "grant the bot View Channel, Send Messages and Embed Links",
		Err:    errors.New("403 Missing Permissions"),
	}
	f.poster.fail = func(call int) error {
		if call == 2 {
			return fatal
		}
		return nil
	}

	err := f.relay.Handle(context.Background(), envelope(t, "01JB", kill(1), kill(2), kill(3)))

	if !errors.Is(err, fatal) {
		t.Fatalf("Handle() = %v, want the fatal post error", err)
	}
	if !strings.Contains(err.Error(), "killmail 2") {
		t.Errorf("Handle() = %q, want the killmail named", err)
	}
	// The third kill is never attempted, and the event is left unprocessed, so
	// the next run replays it.
	wantPosted(t, f, 1)
	if f.store.lastEventID != "" {
		t.Errorf("cursor = %q, want it left behind on an unprocessed event", f.store.lastEventID)
	}
	if len(f.store.saves) != 0 {
		t.Errorf("saves = %+v, want none before the event completes", f.store.saves)
	}
}

func TestHandleStopsOnShutdown(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A post interrupted by SIGTERM: the context is done and the post failed
	// because of it, not because Discord refused the kill.
	f.poster.fail = func(call int) error {
		if call == 2 {
			cancel()
			return context.Canceled
		}
		return nil
	}

	err := f.relay.Handle(ctx, envelope(t, "01JB", kill(1), kill(2), kill(3)))

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Handle() = %v, want context.Canceled", err)
	}
	wantPosted(t, f, 1)
	if f.store.Posted(2) {
		t.Error("Posted(2) = true, want an interrupted post left unmarked")
	}
	if f.store.lastEventID != "" {
		t.Errorf("cursor = %q, want it left behind on an unprocessed event", f.store.lastEventID)
	}
}

func TestHandleAdvancesPastEventsWithoutKillmails(t *testing.T) {
	countPayload, err := json.Marshal(map[string]any{
		"type":            killmail.PayloadKillCount,
		"solar_system_id": systemID,
		"count":           3,
	})
	if err != nil {
		t.Fatalf("marshalling the payload = %v", err)
	}

	for _, tc := range []struct {
		name string
		env  wanderer.Envelope
	}{
		{"kill count", wanderer.Envelope{ID: "01JB", Type: killmail.EventMapKill, Payload: countPayload}},
		{"another event type", wanderer.Envelope{ID: "01JB", Type: "connected", Payload: []byte(`{}`)}},
		{"unparseable payload", wanderer.Envelope{ID: "01JB", Type: killmail.EventMapKill, Payload: []byte(`"nope"`)}},
		{"entry without an ID", envelope(t, "01JB", map[string]any{"victim_char_name": "Pilot"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)

			f.handle(t, tc.env)

			if f.poster.calls != 0 {
				t.Errorf("Post called %d times, want none", f.poster.calls)
			}
			if f.store.lastEventID != "01JB" {
				t.Errorf("cursor = %q, want 01JB", f.store.lastEventID)
			}
		})
	}
}

func TestHandleKeepsTheCursorWhenAnEventHasNoID(t *testing.T) {
	f := newFixture(t)
	f.handle(t, envelope(t, "01JB", kill(1)))

	noID := envelope(t, "", kill(2))
	f.handle(t, noID)

	wantPosted(t, f, 1, 2)
	// Resuming from an empty cursor would replay Wanderer's whole backfill
	// window, so the last known good ID stays.
	if f.store.lastEventID != "01JB" {
		t.Errorf("cursor = %q, want it kept at 01JB", f.store.lastEventID)
	}
}

func TestHandleKeepsGoingWhenTheStateCannotBeSaved(t *testing.T) {
	f := newFixture(t)
	f.store.saveErr = errors.New("no space left on device")

	f.handle(t, envelope(t, "01JB", kill(1)))
	// Dedupe still holds for as long as the process lives.
	f.handle(t, envelope(t, "01JC", kill(1), kill(2)))

	wantPosted(t, f, 1, 2)
	if !strings.Contains(f.logs.String(), "saving the relay state failed") {
		t.Errorf("a failed state write is not reported: %s", f.logs)
	}
}

// runWith stands in for wanderer.Client.Run, so that Handler's result is
// checked against the signature the stream client calls it with.
func runWith(handle func(wanderer.Envelope) error, env wanderer.Envelope) error {
	return handle(env)
}

func TestHandlerBindsTheContext(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := runWith(f.relay.Handler(ctx), envelope(t, "01JB", kill(1))); !errors.Is(err, context.Canceled) {
		t.Fatalf("Handler(cancelled ctx) = %v, want context.Canceled", err)
	}
	if f.poster.calls != 0 {
		t.Errorf("Post called %d times after shutdown, want none", f.poster.calls)
	}
}

func TestNewRequiresEveryDependency(t *testing.T) {
	poster, resolver, store := &fakePoster{}, &fakeResolver{}, newFakeStore()

	for _, tc := range []struct {
		name     string
		poster   Poster
		resolver Resolver
		store    Store
	}{
		{"no poster", nil, resolver, store},
		{"no resolver", poster, nil, store},
		{"no store", poster, resolver, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.poster, tc.resolver, tc.store); err == nil {
				t.Error("New() = nil error, want one naming the missing dependency")
			}
		})
	}
}

func TestIsFatalPost(t *testing.T) {
	fatal := &discord.FatalError{Op: "post", Status: http.StatusUnauthorized, Err: errors.New("401")}
	gaveUp := &discord.GaveUpError{Attempts: 7, Elapsed: 5 * time.Minute, Err: errors.New("502")}

	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"a plain error", errors.New("502 Bad Gateway"), false},
		{"an exhausted retry budget", gaveUp, false},
		{"a fatal error", fatal, true},
		{"a wrapped fatal error", fmt.Errorf("posting: %w", fatal), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsFatalPost(tc.err); got != tc.want {
				t.Errorf("IsFatalPost(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}

func TestWithFatalPostReplacesTheClassification(t *testing.T) {
	// cmd/chainsniffer passes internal/discord's classifier, which recognises
	// errors this package knows nothing about.
	refused := errors.New("discord: 403 Missing Permissions")
	f := newFixture(t, WithFatalPost(func(err error) bool { return errors.Is(err, refused) }))
	f.poster.fail = func(int) error { return refused }

	if err := f.relay.Handle(context.Background(), envelope(t, "01JB", kill(1))); !errors.Is(err, refused) {
		t.Fatalf("Handle() = %v, want the error the classifier called fatal", err)
	}
}

// fakeTopology records the events applied to it, and the cursor the store held
// at the time, so a test can tell the event was applied before the cursor
// moved past it.
type fakeTopology struct {
	store   *fakeStore
	applied []appliedEvent
}

type appliedEvent struct {
	eventType    string
	payload      string
	cursorAtCall string
}

func (g *fakeTopology) Apply(eventType string, payload json.RawMessage) {
	g.applied = append(g.applied, appliedEvent{eventType, string(payload), g.store.lastEventID})
}

func TestHandleAppliesTopologyEventsAndAdvancesTheCursor(t *testing.T) {
	for _, eventType := range []string{EventDeletedSystem, EventConnectionAdded, EventConnectionRemoved, EventSystemMetadataChanged} {
		t.Run(eventType, func(t *testing.T) {
			graph := &fakeTopology{}
			f := newFixture(t, WithTopology(graph))
			graph.store = f.store
			f.store.lastEventID = "01JB"

			payload := `{"solar_system_source_id":31000001,"solar_system_target_id":31000002}`
			f.handle(t, wanderer.Envelope{ID: "01JC", Type: eventType, Payload: []byte(payload)})

			want := []appliedEvent{{eventType, payload, "01JB"}}
			if !slices.Equal(graph.applied, want) {
				t.Errorf("applied = %+v, want %+v", graph.applied, want)
			}
			if len(f.store.saves) != 1 || f.store.saves[0].lastEventID != "01JC" {
				t.Errorf("saves = %+v, want one with cursor 01JC", f.store.saves)
			}
			if f.poster.calls != 0 {
				t.Errorf("Post called %d times, want none", f.poster.calls)
			}
		})
	}
}

func TestHandleIgnoresTopologyEventsWithoutATopology(t *testing.T) {
	f := newFixture(t)

	f.handle(t, wanderer.Envelope{ID: "01JC", Type: EventConnectionRemoved, Payload: []byte(`{}`)})

	if len(f.store.saves) != 1 || f.store.saves[0].lastEventID != "01JC" {
		t.Errorf("saves = %+v, want one with cursor 01JC", f.store.saves)
	}
	if !strings.Contains(f.logs.String(), "event_type="+EventConnectionRemoved) {
		t.Error("an ignored topology event is not logged")
	}
}

// A topology never sees map_kill events, and kills go through the pipeline as
// they do without one.
func TestHandleKeepsKillsOffTheTopology(t *testing.T) {
	graph := &fakeTopology{}
	f := newFixture(t, WithTopology(graph))
	graph.store = f.store

	f.handle(t, envelope(t, "01JB", kill(1)))

	wantPosted(t, f, 1)
	if len(graph.applied) != 0 {
		t.Errorf("applied = %+v, want nothing", graph.applied)
	}
	if f.store.lastEventID != "01JB" {
		t.Errorf("cursor = %q, want 01JB", f.store.lastEventID)
	}
}

// With the linked-system filter configured, the relay drops a kill in an
// island and logs the filter's reason.
func TestHandleDropsKillsInUnlinkedSystems(t *testing.T) {
	const island = 31000099
	f := newFixture(t, WithFilters(killmail.Filters{Linked: func(id int64) bool { return id == systemID }}))

	f.handle(t, envelope(t, "01JB", kill(1), kill(2, map[string]any{"solar_system_id": island})))

	wantPosted(t, f, 1)
	if !strings.Contains(f.logs.String(), "killmail_id=2 filter="+killmail.FilterUnlinkedSystem) {
		t.Errorf("the unlinked drop is not logged:\n%s", f.logs.String())
	}
	if f.store.lastEventID != "01JB" {
		t.Errorf("cursor = %q, want 01JB", f.store.lastEventID)
	}
}

// fakeAliases answers chain aliases from a fixed table.
type fakeAliases map[int64]string

func (a fakeAliases) Alias(id int64) string { return a[id] }

// "Metadata events advance the cursor": a system_metadata_changed event goes
// to the topology, and the cursor moves past it once it is handled.
func TestHandleAppliesMetadataEventsAndAdvancesTheCursor(t *testing.T) {
	graph := &fakeTopology{}
	f := newFixture(t, WithTopology(graph))
	graph.store = f.store

	payload := `{"solar_system_id":31000123,"temporary_name":"HOTEL"}`
	f.handle(t, wanderer.Envelope{ID: "01JD", Type: EventSystemMetadataChanged, Payload: []byte(payload)})

	want := []appliedEvent{{EventSystemMetadataChanged, payload, ""}}
	if !slices.Equal(graph.applied, want) {
		t.Errorf("applied = %+v, want %+v", graph.applied, want)
	}
	if f.store.lastEventID != "01JD" {
		t.Errorf("cursor = %q, want 01JD", f.store.lastEventID)
	}
}

// "Chain alias": a kill in a system the map names shows the alias after the
// system name, and the posted-killmail log line carries it.
func TestHandleRendersTheChainAlias(t *testing.T) {
	f := newFixture(t, WithAliases(fakeAliases{systemID: "HOTEL"}))

	f.handle(t, envelope(t, "01JB", kill(1)))

	if got, want := systemRow(f.poster.embeds[0]), systemName+" (HOTEL)"; got != want {
		t.Errorf("System row = %q, want %q", got, want)
	}
	if !strings.Contains(f.logs.String(), "chain_alias=HOTEL") {
		t.Errorf("the posted-killmail log line does not name the alias:\n%s", f.logs)
	}
}

// Without an alias, from no Aliases at all or for a system it does not name,
// the System row is the name alone and the log line has no chain_alias.
func TestHandleRendersTheNameAloneWithoutAnAlias(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []Option
	}{
		{"no aliases", nil},
		{"system without an alias", []Option{WithAliases(fakeAliases{31000999: "HOTEL"})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.opts...)

			f.handle(t, envelope(t, "01JB", kill(1)))

			if got := systemRow(f.poster.embeds[0]); got != systemName {
				t.Errorf("System row = %q, want %q", got, systemName)
			}
			if strings.Contains(f.logs.String(), "chain_alias") {
				t.Errorf("the log names a chain alias for a system without one:\n%s", f.logs)
			}
		})
	}
}

// "Card style configuration": the relay posts every card in the style it is
// given, and in the block style without one.
func TestHandleRendersTheCardStyle(t *testing.T) {
	for _, tc := range []struct {
		name       string
		opts       []Option
		wantFields bool
	}{
		{"default", nil, false},
		{"block", []Option{WithCardStyle(discord.CardStyleBlock)}, false},
		{"fields", []Option{WithCardStyle(discord.CardStyleFields)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.opts...)

			f.handle(t, envelope(t, "01JB", kill(1)))

			embed := f.poster.embeds[0]
			if tc.wantFields {
				if embed.Description != "" || len(embed.Fields) == 0 {
					t.Errorf("fields card: description %q, %d fields; want no description and fields", embed.Description, len(embed.Fields))
				}
				return
			}
			if embed.Description == "" || len(embed.Fields) != 0 {
				t.Errorf("block card: description %q, %d fields; want a description and no fields", embed.Description, len(embed.Fields))
			}
		})
	}
}

// fakeAffiliations answers characters, corporations and alliances from fixed tables,
// failing for an ID it does not know, and records every ID it was asked for.
type fakeAffiliations struct {
	corporations map[int64]universe.Entity
	alliances    map[int64]universe.Entity
	characters   map[int64]universe.Entity

	mu    sync.Mutex
	asked []string
}

func (a *fakeAffiliations) Corporation(_ context.Context, id int64) (universe.Entity, bool) {
	return a.lookup("corporation", a.corporations, id)
}

func (a *fakeAffiliations) Alliance(_ context.Context, id int64) (universe.Entity, bool) {
	return a.lookup("alliance", a.alliances, id)
}

func (a *fakeAffiliations) Character(_ context.Context, id int64) (universe.Entity, bool) {
	return a.lookup("character", a.characters, id)
}

func (a *fakeAffiliations) lookup(kind string, table map[int64]universe.Entity, id int64) (universe.Entity, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked = append(a.asked, fmt.Sprintf("%s %d", kind, id))
	e, ok := table[id]
	return e, ok
}

// lookups returns the IDs asked for, sorted, since they are asked concurrently.
func (a *fakeAffiliations) lookups() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Sorted(slices.Values(a.asked))
}

func newFakeAffiliations() *fakeAffiliations {
	return &fakeAffiliations{
		corporations: map[int64]universe.Entity{
			98735003: {Name: "Final Corporation", Ticker: "FNLC"},
			98736396: {Name: "Victim Corporation", Ticker: "VCTC"},
		},
		alliances: map[int64]universe.Entity{
			99014747: {Name: "Final Alliance", Ticker: "FNLA"},
		},
		characters: map[int64]universe.Entity{
			2124740911: {Name: "Victim Pilot"},
			96437707:   {Name: "Final Pilot"},
		},
	}
}

// "Names missing from the payload": a party with corporation and alliance IDs
// but no names or tickers shows the resolved ones.
func TestHandleFillsMissingAffiliationNames(t *testing.T) {
	affils := newFakeAffiliations()
	f := newFixture(t, WithAffiliations(affils))

	f.handle(t, envelope(t, "01JB", kill(1, map[string]any{
		"victim_corp_id":         98736396,
		"final_blow_char_name":   "Pilot B",
		"final_blow_ship_name":   "Loki",
		"final_blow_corp_id":     98735003,
		"final_blow_alliance_id": 99014747,
	})))

	desc := f.poster.embeds[0].Description
	for _, want := range []string{"[VCTC] Victim Corporation", "[FNLC] Final Corporation", "<FNLA> Final Alliance"} {
		if !strings.Contains(desc, want) {
			t.Errorf("embed lacks %q:\n%s", want, desc)
		}
	}
	want := []string{"alliance 99014747", "corporation 98735003", "corporation 98736396"}
	if got := affils.lookups(); !slices.Equal(got, want) {
		t.Errorf("lookups = %v, want %v", got, want)
	}
}

// A party with a character ID but no character name shows the resolved name,
// as Wanderer sends for some kills; a payload name is kept and not looked up.
func TestHandleFillsMissingCharacterNames(t *testing.T) {
	affils := newFakeAffiliations()
	f := newFixture(t, WithAffiliations(affils))

	f.handle(t, envelope(t, "01JB", kill(1, map[string]any{
		"victim_char_id":       2124740911,
		"victim_char_name":     "",
		"victim_corp_id":       98736396,
		"victim_corp_name":     "Victim Corporation",
		"victim_corp_ticker":   "VCTC",
		"final_blow_char_id":   96437707,
		"final_blow_char_name": "Payload Pilot",
		"final_blow_ship_name": "Loki",
	})))

	desc := f.poster.embeds[0].Description
	for _, want := range []string{"Victim Pilot", "Payload Pilot"} {
		if !strings.Contains(desc, want) {
			t.Errorf("embed lacks %q:\n%s", want, desc)
		}
	}
	if strings.Contains(desc, "Final Pilot") {
		t.Errorf("embed replaced the payload's final blow name:\n%s", desc)
	}
	if got, want := affils.lookups(), []string{"character 2124740911"}; !slices.Equal(got, want) {
		t.Errorf("lookups = %v, want %v", got, want)
	}
}

// "No alliance in the payload": without an alliance ID none is looked up or
// shown, even though the corporation is known.
func TestHandleInfersNoAllianceWithoutAnAllianceID(t *testing.T) {
	affils := newFakeAffiliations()
	f := newFixture(t, WithAffiliations(affils))

	f.handle(t, envelope(t, "01JB", kill(1, map[string]any{"victim_corp_id": 98736396})))

	if desc := f.poster.embeds[0].Description; strings.Contains(desc, "<") {
		t.Errorf("embed shows an alliance for a victim without one:\n%s", desc)
	}
	if got, want := affils.lookups(), []string{"corporation 98736396"}; !slices.Equal(got, want) {
		t.Errorf("lookups = %v, want %v", got, want)
	}
}

// "Payload already carries the names": the payload's names are shown and
// nothing is looked up; a party missing only its ticker has the ticker filled
// and keeps the payload's name.
func TestHandleKeepsPayloadAffiliationNames(t *testing.T) {
	affils := newFakeAffiliations()
	f := newFixture(t, WithAffiliations(affils))

	f.handle(t, envelope(t, "01JB", kill(1, map[string]any{
		"victim_corp_id":          98736396,
		"victim_corp_name":        "Payload Corporation",
		"victim_corp_ticker":      "PAYC",
		"victim_alliance_id":      99014747,
		"victim_alliance_name":    "Payload Alliance",
		"victim_alliance_ticker":  "PAYA",
		"final_blow_char_name":    "Pilot B",
		"final_blow_corp_id":      98735003,
		"final_blow_corp_name":    "Renamed Corporation",
		"final_blow_alliance_id":  0,
		"final_blow_ship_name":    "Loki",
		"final_blow_ship_type_id": 29990,
	})))

	desc := f.poster.embeds[0].Description
	for _, want := range []string{"[PAYC] Payload Corporation", "<PAYA> Payload Alliance", "[FNLC] Renamed Corporation"} {
		if !strings.Contains(desc, want) {
			t.Errorf("embed lacks %q:\n%s", want, desc)
		}
	}
	if got, want := affils.lookups(), []string{"corporation 98735003"}; !slices.Equal(got, want) {
		t.Errorf("lookups = %v, want %v", got, want)
	}
}

// "Lookup unavailable": a corporation that cannot be resolved is left out,
// the rest is filled, and the kill is still posted.
func TestHandlePostsWhenAnAffiliationLookupFails(t *testing.T) {
	f := newFixture(t, WithAffiliations(newFakeAffiliations()))

	f.handle(t, envelope(t, "01JB", kill(1, map[string]any{
		"victim_corp_id":         98000001,
		"final_blow_char_name":   "Pilot B",
		"final_blow_corp_id":     98735003,
		"final_blow_alliance_id": 99014747,
	})))

	wantPosted(t, f, 1)
	desc := f.poster.embeds[0].Description
	if !strings.Contains(desc, "Victim     Pilot 1\nFinal blow") {
		t.Errorf("embed's Victim row has more than the pilot for an unresolved corporation:\n%s", desc)
	}
	if !strings.Contains(desc, "[FNLC] Final Corporation") {
		t.Errorf("embed lacks the resolved final blow corporation:\n%s", desc)
	}
}

// Without Affiliations the payload is rendered as it came.
func TestHandleLeavesAffiliationsAloneWithoutAResolver(t *testing.T) {
	f := newFixture(t)

	f.handle(t, envelope(t, "01JB", kill(1, map[string]any{"victim_corp_id": 98736396})))

	if desc := f.poster.embeds[0].Description; !strings.Contains(desc, "Victim     Pilot 1\n```") {
		t.Errorf("embed's Victim row has more than the pilot without a resolver:\n%s", desc)
	}
}

// fakeImages serves a blank image for every URL except the ones in fail, and
// records every URL it was asked to fetch. URLs are "kind/id/size", or "" for
// an ID of zero or less, as internal/eveimage's are.
type fakeImages struct {
	mu      sync.Mutex
	fail    map[string]bool
	fetched []string
}

func (i *fakeImages) url(kind string, id int64, size int) string {
	if id <= 0 {
		return ""
	}
	return fmt.Sprintf("%s/%d/%d", kind, id, size)
}

func (i *fakeImages) CharacterPortrait(id int64, size int) string {
	return i.url("character", id, size)
}
func (i *fakeImages) CorporationLogo(id int64, size int) string {
	return i.url("corporation", id, size)
}
func (i *fakeImages) AllianceLogo(id int64, size int) string { return i.url("alliance", id, size) }

func (i *fakeImages) Fetch(_ context.Context, url string) (image.Image, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.fetched = append(i.fetched, url)
	if url == "" || i.fail[url] {
		return nil, false
	}
	return image.NewRGBA(image.Rect(0, 0, 8, 8)), true
}

// partyIDs gives the victim and the final blow a character and a
// corporation, and the victim an alliance.
var partyIDs = map[string]any{
	"victim_char_id":         90000001,
	"victim_corp_id":         98000001,
	"victim_alliance_id":     99000001,
	"final_blow_char_id":     90000002,
	"final_blow_corp_id":     98000002,
	"final_blow_char_name":   "Killer",
	"final_blow_ship_name":   "Sabre",
	"final_blow_alliance_id": 0,
}

// wantStrip checks that msg is the strip card: the block embed showing a PNG
// attached as kill.png.
func wantStrip(t *testing.T, msg discord.Message) {
	t.Helper()
	if msg.Embed == nil || msg.Embed.Description == "" {
		t.Fatalf("strip card embed = %+v, want the block embed", msg.Embed)
	}
	if msg.Embed.Image == nil || msg.Embed.Image.URL != "attachment://kill.png" {
		t.Errorf("strip card image = %+v, want attachment://kill.png", msg.Embed.Image)
	}
	if len(msg.Files) != 1 || msg.Files[0].Name != "kill.png" {
		t.Fatalf("strip card files = %+v, want kill.png alone", msg.Files)
	}
	if _, err := png.Decode(bytes.NewReader(msg.Files[0].Data)); err != nil {
		t.Errorf("kill.png does not decode as a PNG: %v", err)
	}
}

func TestHandlePostsTheStripWithItsImages(t *testing.T) {
	images := &fakeImages{}
	f := newFixture(t, WithCardStyle(discord.CardStyleStrip), WithImages(images))

	f.handle(t, envelope(t, "01JB", kill(1, partyIDs)))

	wantPosted(t, f, 1)
	wantStrip(t, f.poster.messages[0])
	slices.Sort(images.fetched)
	want := []string{
		"alliance/99000001/64",
		"character/90000001/128", "character/90000002/128",
		"corporation/98000001/64", "corporation/98000002/64",
	}
	if !slices.Equal(images.fetched, want) {
		t.Errorf("fetched %q, want %q (only the IDs the payload gives)", images.fetched, want)
	}
}

func TestHandlePostsTheStripWhenImagesFail(t *testing.T) {
	images := &fakeImages{fail: map[string]bool{
		"character/90000001/128": true, "corporation/98000002/64": true,
	}}
	f := newFixture(t, WithCardStyle(discord.CardStyleStrip), WithImages(images))

	f.handle(t, envelope(t, "01JB", kill(1, partyIDs)))

	wantPosted(t, f, 1)
	wantStrip(t, f.poster.messages[0])
}

func TestHandlePostsTheStripWithoutImages(t *testing.T) {
	f := newFixture(t, WithCardStyle(discord.CardStyleStrip))

	f.handle(t, envelope(t, "01JB", kill(1, partyIDs)))

	wantPosted(t, f, 1)
	wantStrip(t, f.poster.messages[0])
}

func TestHandleFallsBackToTheBlockCardWhenTheStripFails(t *testing.T) {
	f := newFixture(t, WithCardStyle(discord.CardStyleStrip), WithImages(&fakeImages{}))
	f.relay.drawStrip = func(cardimage.StripInput) ([]byte, error) { return nil, errors.New("no fonts") }

	f.handle(t, envelope(t, "01JB", kill(1, partyIDs)))

	wantPosted(t, f, 1)
	msg := f.poster.messages[0]
	if msg.Embed.Description == "" || msg.Embed.Image != nil || len(msg.Files) != 0 {
		t.Errorf("fallback card = embed %+v, %d files; want the block embed with no image or files", msg.Embed, len(msg.Files))
	}
	if !strings.Contains(f.logs.String(), "level=WARN") || !strings.Contains(f.logs.String(), "no fonts") {
		t.Errorf("logs do not warn about the strip:\n%s", f.logs)
	}
}

func TestHandleAppliesTheStripTextFallbacks(t *testing.T) {
	var got cardimage.StripInput
	f := newFixture(t, WithCardStyle(discord.CardStyleStrip))
	f.relay.drawStrip = func(in cardimage.StripInput) ([]byte, error) {
		got = in
		return cardimage.Strip(in)
	}

	f.handle(t, envelope(t, "01JB", kill(1, map[string]any{
		"victim_char_name": "", "victim_ship_name": "", "final_blow_ship_name": "Guristas Eradicator",
	})))

	want := cardimage.StripInput{
		Victim:   cardimage.Party{Name: "Unknown pilot", Hull: "Unknown ship"},
		Attacker: cardimage.Party{Name: "Guristas Eradicator", Hull: "Guristas Eradicator"},
	}
	if got != want {
		t.Errorf("strip input = %+v, want %+v", got, want)
	}
}

func TestHandlePostsComponents(t *testing.T) {
	f := newFixture(t, WithCardStyle(discord.CardStyleComponents), WithImages(&fakeImages{}))

	f.handle(t, envelope(t, "01JB", kill(1, partyIDs)))

	if len(f.poster.messages) != 1 {
		t.Fatalf("posted %d messages, want 1", len(f.poster.messages))
	}
	msg := f.poster.messages[0]
	if msg.Embed != nil || len(msg.Files) != 0 || len(msg.Components) == 0 {
		t.Errorf("components card = embed %+v, %d files, %d components; want components alone",
			msg.Embed, len(msg.Files), len(msg.Components))
	}
	if !f.store.Posted(1) {
		t.Error("the components card was not recorded as posted")
	}
}
