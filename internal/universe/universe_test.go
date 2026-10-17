package universe_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Vapor-Lock/chainsniffer/internal/universe"
)

const (
	theraID   = int64(31000005)
	theraName = "Thera"
)

// newResolver builds a resolver aimed at srv with its warnings discarded, so a
// deliberate failure does not litter the test output.
func newResolver(t *testing.T, srv *httptest.Server, opts ...universe.Option) *universe.Resolver {
	t.Helper()
	base := []universe.Option{
		universe.WithBaseURL(srv.URL),
		universe.WithHTTPClient(srv.Client()),
		universe.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	return universe.NewResolver(append(base, opts...)...)
}

// systemHandler answers ESI's system endpoint for id with name, and records
// how many requests it saw.
func systemHandler(t *testing.T, requests *atomic.Int64) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got, want := r.URL.Path, "/universe/systems/31000005/"; got != want {
			t.Errorf("request path = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Accept"), "application/json"; got != want {
			t.Errorf("Accept header = %q, want %q", got, want)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("User-Agent header is empty, want the bot's identity")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"system_id":31000005,"name":"Thera","constellation_id":21000324,"security_status":-0.99}`))
	}
}

// "System name" / "Name resolved": a known ID resolves to its name.
func TestNameResolves(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(systemHandler(t, &requests))
	defer srv.Close()

	if got := newResolver(t, srv).Name(context.Background(), theraID); got != theraName {
		t.Errorf("Name() = %q, want %q", got, theraName)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("ESI requests = %d, want 1", got)
	}
}

// A resolved name is cached, so a second kill in the same system costs no
// request.
func TestNameCachesSuccess(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(systemHandler(t, &requests))
	defer srv.Close()

	r := newResolver(t, srv)
	for i := range 3 {
		if got := r.Name(context.Background(), theraID); got != theraName {
			t.Fatalf("call %d: Name() = %q, want %q", i+1, got, theraName)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("ESI requests = %d, want 1 (the rest should come from the cache)", got)
	}
}

// "System name" / "Lookup unavailable": a lookup that runs past the timeout
// falls back to the ID instead of delaying the post further.
func TestNameFallsBackOnTimeout(t *testing.T) {
	released := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hold the request until the resolver's timeout cancels it, so the
		// handler never outlives the test.
		select {
		case <-r.Context().Done():
		case <-released:
		}
	}))
	defer func() {
		close(released)
		srv.Close()
	}()

	r := newResolver(t, srv, universe.WithTimeout(50*time.Millisecond))
	start := time.Now()
	if got, want := r.Name(context.Background(), theraID), "31000005"; got != want {
		t.Errorf("Name() = %q, want the numeric ID %q", got, want)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Name() took %s, want it bounded by the lookup timeout", elapsed)
	}
}

// A system ESI does not know falls back to the ID, and the failure is not
// cached, so the next kill there tries again.
func TestNameFallsBackOn404AndDoesNotCache(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"Not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	r := newResolver(t, srv)
	for i := range 2 {
		if got, want := r.Name(context.Background(), theraID), "31000005"; got != want {
			t.Fatalf("call %d: Name() = %q, want the numeric ID %q", i+1, got, want)
		}
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("ESI requests = %d, want 2 (a failed lookup must not be cached)", got)
	}
}

// An answer that is not JSON, or carries no name, is a failure like any other.
func TestNameFallsBackOnUnusableAnswer(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "not JSON", body: "<html>gateway</html>"},
		{name: "no name field", body: `{"system_id":31000005}`},
		{name: "empty name", body: `{"name":""}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			if got, want := newResolver(t, srv).Name(context.Background(), theraID), "31000005"; got != want {
				t.Errorf("Name() = %q, want the numeric ID %q", got, want)
			}
		})
	}
}

// A payload with no solar_system_id decodes to 0, which there is nothing to
// look up: the embed then shows no system at all.
func TestNameWithoutSystemID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the resolver made a request for a non-positive system ID")
	}))
	defer srv.Close()

	r := newResolver(t, srv)
	for _, id := range []int64{0, -1} {
		if got := r.Name(context.Background(), id); got != "" {
			t.Errorf("Name(%d) = %q, want the empty string", id, got)
		}
	}
}

// A cancelled context cancels the lookup in flight rather than waiting out the
// timeout, which is what lets a shutdown proceed.
func TestNameHonoursContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, want := newResolver(t, srv).Name(ctx, theraID), "31000005"; got != want {
		t.Errorf("Name() = %q, want the numeric ID %q", got, want)
	}
}

const (
	corpID   = int64(98000001)
	allyID   = int64(99000001)
	corpBody = `{"name":"Vapor Lock Industries","ticker":"VLI","member_count":42}`
	allyBody = `{"name":"Vapor Lock Alliance","ticker":"VLA","executor_corporation_id":98000001}`
	charID   = int64(2112000001)
	charBody = `{"name":"Unlucky Miner","corporation_id":98000001,"birthday":"2015-03-24T11:37:00Z"}`
)

// entityHandler answers ESI's character, corporation and alliance endpoints, and records
// how many requests it saw.
func entityHandler(t *testing.T, requests *atomic.Int64) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got, want := r.Header.Get("Accept"), "application/json"; got != want {
			t.Errorf("Accept header = %q, want %q", got, want)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("User-Agent header is empty, want the bot's identity")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/corporations/98000001/":
			_, _ = w.Write([]byte(corpBody))
		case "/alliances/99000001/":
			_, _ = w.Write([]byte(allyBody))
		case "/characters/2112000001/":
			_, _ = w.Write([]byte(charBody))
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}
}

// lookups pairs each entity lookup with the ID and answer the handlers above
// serve for it. A character has a name and no ticker.
var lookups = []struct {
	kind      string
	id        int64
	want      universe.Entity
	hasTicker bool
	call      func(*universe.Resolver, context.Context, int64) (universe.Entity, bool)
}{
	{
		kind: universe.KindCorporation, id: corpID,
		want:      universe.Entity{Name: "Vapor Lock Industries", Ticker: "VLI"},
		hasTicker: true,
		call:      (*universe.Resolver).Corporation,
	},
	{
		kind: universe.KindAlliance, id: allyID,
		want:      universe.Entity{Name: "Vapor Lock Alliance", Ticker: "VLA"},
		hasTicker: true,
		call:      (*universe.Resolver).Alliance,
	},
	{
		kind: universe.KindCharacter, id: charID,
		want: universe.Entity{Name: "Unlucky Miner"},
		call: (*universe.Resolver).Character,
	},
}

// fakeNames is a NameCache that records what it was given.
type fakeNames struct {
	mu      sync.Mutex
	entries map[string]universe.Entity
	puts    int
}

func newFakeNames() *fakeNames {
	return &fakeNames{entries: make(map[string]universe.Entity)}
}

func key(kind string, id int64) string { return fmt.Sprintf("%s/%d", kind, id) }

func (f *fakeNames) Get(kind string, id int64) (string, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entries[key(kind, id)]
	return e.Name, e.Ticker, ok
}

func (f *fakeNames) Put(kind string, id int64, name, ticker string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	f.entries[key(kind, id)] = universe.Entity{Name: name, Ticker: ticker}
}

// A known corporation or alliance resolves to its name and ticker, which is
// put in the name cache; a second lookup is answered from the cache.
func TestEntityResolvesAndCaches(t *testing.T) {
	for _, tc := range lookups {
		t.Run(tc.kind, func(t *testing.T) {
			var requests atomic.Int64
			srv := httptest.NewServer(entityHandler(t, &requests))
			defer srv.Close()

			names := newFakeNames()
			r := newResolver(t, srv, universe.WithNameCache(names))
			for i := range 3 {
				got, ok := tc.call(r, context.Background(), tc.id)
				if !ok || got != tc.want {
					t.Fatalf("call %d: got (%+v, %t), want (%+v, true)", i+1, got, ok, tc.want)
				}
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("ESI requests = %d, want 1 (the rest should come from the cache)", got)
			}
			if name, ticker, ok := names.Get(tc.kind, tc.id); !ok || name != tc.want.Name || ticker != tc.want.Ticker {
				t.Errorf("cache holds (%q, %q, %t), want (%q, %q, true)", name, ticker, ok, tc.want.Name, tc.want.Ticker)
			}
		})
	}
}

// The default in-memory cache also spares the second request.
func TestEntityDefaultCache(t *testing.T) {
	for _, tc := range lookups {
		t.Run(tc.kind, func(t *testing.T) {
			var requests atomic.Int64
			srv := httptest.NewServer(entityHandler(t, &requests))
			defer srv.Close()

			r := newResolver(t, srv)
			for i := range 2 {
				if got, ok := tc.call(r, context.Background(), tc.id); !ok || got != tc.want {
					t.Fatalf("call %d: got (%+v, %t), want (%+v, true)", i+1, got, ok, tc.want)
				}
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("ESI requests = %d, want 1", got)
			}
		})
	}
}

// A name cache filled before the resolver starts, as names.json is after a
// restart, answers without a request.
func TestEntityPrefilledCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the resolver asked ESI for %q despite a cached entry", r.URL.Path)
	}))
	defer srv.Close()

	names := newFakeNames()
	names.Put(universe.KindCorporation, corpID, "Cached Corp", "CC")
	names.Put(universe.KindAlliance, allyID, "Cached Alliance", "CA")
	names.Put(universe.KindCharacter, charID, "Cached Pilot", "")
	r := newResolver(t, srv, universe.WithNameCache(names))

	if got, ok := r.Corporation(context.Background(), corpID); !ok || got != (universe.Entity{Name: "Cached Corp", Ticker: "CC"}) {
		t.Errorf("Corporation() = (%+v, %t), want the cached entry", got, ok)
	}
	if got, ok := r.Alliance(context.Background(), allyID); !ok || got != (universe.Entity{Name: "Cached Alliance", Ticker: "CA"}) {
		t.Errorf("Alliance() = (%+v, %t), want the cached entry", got, ok)
	}
	if got, ok := r.Character(context.Background(), charID); !ok || got != (universe.Entity{Name: "Cached Pilot"}) {
		t.Errorf("Character() = (%+v, %t), want the cached entry", got, ok)
	}
}

// A failed lookup returns false and is not put in the cache, so the next kill
// carrying that ID tries again.
func TestEntityFailures(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		timeout bool
		ticker  bool // a failure only for a lookup that needs a ticker
	}{
		{name: "not found", status: http.StatusNotFound, body: `{"error":"Not found"}`},
		{name: "server error", status: http.StatusBadGateway, body: `{"error":"Bad gateway"}`},
		{name: "not JSON", status: http.StatusOK, body: "<html>gateway</html>"},
		{name: "no name", status: http.StatusOK, body: `{"ticker":"VLI"}`},
		{name: "no ticker", status: http.StatusOK, body: `{"name":"Vapor Lock Industries"}`, ticker: true},
		{name: "timeout", timeout: true},
	}
	for _, lk := range lookups {
		for _, tc := range tests {
			if tc.ticker && !lk.hasTicker {
				continue
			}
			t.Run(lk.kind+"/"+tc.name, func(t *testing.T) {
				var requests atomic.Int64
				released := make(chan struct{})
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if tc.timeout {
						select {
						case <-r.Context().Done():
						case <-released:
						}
						return
					}
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer func() {
					close(released)
					srv.Close()
				}()

				names := newFakeNames()
				r := newResolver(t, srv, universe.WithNameCache(names), universe.WithTimeout(50*time.Millisecond))
				for i := range 2 {
					start := time.Now()
					if got, ok := lk.call(r, context.Background(), lk.id); ok || got != (universe.Entity{}) {
						t.Fatalf("call %d: got (%+v, %t), want a zero Entity and false", i+1, got, ok)
					}
					if elapsed := time.Since(start); elapsed > time.Second {
						t.Errorf("call %d took %s, want it bounded by the lookup timeout", i+1, elapsed)
					}
				}
				if got := requests.Load(); got != 2 {
					t.Errorf("ESI requests = %d, want 2 (a failed lookup must not be cached)", got)
				}
				if names.puts != 0 {
					t.Errorf("cache Puts = %d, want 0", names.puts)
				}
			})
		}
	}
}

// An ID of zero or less, which is what a payload without one decodes to, is
// not looked up.
func TestEntityWithoutID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the resolver made a request for a non-positive ID: %q", r.URL.Path)
	}))
	defer srv.Close()

	r := newResolver(t, srv)
	for _, lk := range lookups {
		for _, id := range []int64{0, -1} {
			if got, ok := lk.call(r, context.Background(), id); ok || got != (universe.Entity{}) {
				t.Errorf("%s(%d) = (%+v, %t), want a zero Entity and false", lk.kind, id, got, ok)
			}
		}
	}
}
