package universe_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
