package wanderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Vapor-Lock/chainsniffer/internal/wanderer"
)

const (
	testToken = "wanderer-public-api-key-ssshh"
	testMap   = "my-chain"
	testMapID = "f1b0d0c4-1f3a-4f6f-9a01-2c5d7e8b9f10"
)

func TestClientRequestsTheMapKillStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cursor    string
		wantQuery url.Values
	}{
		{
			name:      "without a cursor",
			wantQuery: url.Values{"events": {"map_kill"}},
		},
		{
			name:      "with a cursor",
			cursor:    "01JB",
			wantQuery: url.Values{"events": {"map_kill"}, "last_event_id": {"01JB"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server, recorder := newStreamServer(t, closeImmediately)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			stop := &sleeper{stopAfter: 1, cancel: cancel}
			client := newClient(t, server.URL, wanderer.WithSleep(stop.sleep))
			if err := client.Run(ctx, func() string { return test.cursor }, failOnEvent(t)); !errors.Is(err, context.Canceled) {
				t.Fatalf("Run() = %v, want context.Canceled", err)
			}

			requests := recorder.all()
			if len(requests) == 0 {
				t.Fatal("the stream endpoint was never requested")
			}
			got := requests[0]
			if want := "/api/maps/" + testMap + "/events/stream"; got.Path != want {
				t.Errorf("request path = %q, want %q", got.Path, want)
			}
			if fmt.Sprint(got.Query) != fmt.Sprint(test.wantQuery) {
				t.Errorf("request query = %v, want %v", got.Query, test.wantQuery)
			}
			if want := "Bearer " + testToken; got.Header.Get("Authorization") != want {
				t.Errorf("Authorization = %q, want %q", got.Header.Get("Authorization"), want)
			}
			if want := "text/event-stream"; got.Header.Get("Accept") != want {
				t.Errorf("Accept = %q, want %q", got.Header.Get("Accept"), want)
			}
		})
	}
}

func TestClientDeliversOnlyKillEventsInOrder(t *testing.T) {
	t.Parallel()

	stream := connectedFrame("01A") +
		": keepalive\n\n" +
		killFrame("01B", 128412345) +
		"id: 01C\nevent: map_kill\ndata: {not json}\n\n" +
		"id: 01D\nevent: map_kill\ndata: {\"id\":\"01D\",\"payload\":{}}\n\n" +
		killFrame("01E", 128412346) +
		"id: 01F\nevent: map_updated\ndata: {\"id\":\"01F\",\"type\":\"map_updated\",\"payload\":{}}\n\n"

	server, _ := newStreamServer(t, writeFrames(stream))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logs := &logBuffer{}
	var got []wanderer.Envelope
	stop := &sleeper{stopAfter: 1, cancel: cancel}
	client := newClient(t, server.URL, wanderer.WithSleep(stop.sleep), wanderer.WithLogger(logs.logger(slog.LevelDebug)))
	err := client.Run(ctx, nil, func(envelope wanderer.Envelope) error {
		got = append(got, envelope)
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v, want context.Canceled", err)
	}

	if len(got) != 2 {
		t.Fatalf("handler received %d events, want 2: %+v", len(got), got)
	}
	for i, want := range []struct {
		id       string
		killmail int64
	}{{"01B", 128412345}, {"01E", 128412346}} {
		if got[i].ID != want.id {
			t.Errorf("event %d ID = %q, want %q", i, got[i].ID, want.id)
		}
		if got[i].Type != wanderer.EventMapKill {
			t.Errorf("event %d Type = %q, want %q", i, got[i].Type, wanderer.EventMapKill)
		}
		if got[i].MapID != testMapID {
			t.Errorf("event %d MapID = %q, want %q", i, got[i].MapID, testMapID)
		}
		if got[i].Timestamp.IsZero() {
			t.Errorf("event %d Timestamp is zero, want the envelope's timestamp", i)
		}
		if id := killmailID(t, got[i].Payload); id != want.killmail {
			t.Errorf("event %d payload killmail_id = %d, want %d", i, id, want.killmail)
		}
	}

	if text := logs.String(); !strings.Contains(text, "subscribed to the Wanderer map kill stream") {
		t.Errorf("the connected event was not logged:\n%s", text)
	}
	if got, want := strings.Count(logs.String(), "skipping malformed Wanderer event"), 2; got != want {
		t.Errorf("logged %d malformed events, want %d:\n%s", got, want, logs.String())
	}
}

func TestClientReconnectsWithGrowingDelaysAndCursor(t *testing.T) {
	t.Parallel()

	server, recorder := newStreamServer(t,
		writeFrames(connectedFrame("01A")+killFrame("01B", 1)),
		closeImmediately,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cursor := ""
	stop := &sleeper{stopAfter: 3, cancel: cancel}
	client := newClient(t, server.URL,
		wanderer.WithSleep(stop.sleep),
		wanderer.WithBackoff(100*time.Millisecond, time.Second),
		wanderer.WithJitter(0),
	)
	err := client.Run(ctx, func() string { return cursor }, func(envelope wanderer.Envelope) error {
		cursor = envelope.ID
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v, want context.Canceled", err)
	}

	// The first connection delivered an event, so its drop starts the backoff
	// over at the minimum; the two empty connections after it double it.
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
	if got := stop.recorded(); !equalDurations(got, want) {
		t.Errorf("backoff delays = %v, want %v", got, want)
	}

	// Three connections, each followed by one of the recorded waits.
	requests := recorder.all()
	if len(requests) != 3 {
		t.Fatalf("made %d requests, want 3", len(requests))
	}
	if cursor, ok := requests[0].Query["last_event_id"]; ok {
		t.Errorf("the first request carried last_event_id=%v, want none", cursor)
	}
	for i, request := range requests[1:] {
		if got := request.Query.Get("last_event_id"); got != "01B" {
			t.Errorf("request %d last_event_id = %q, want %q", i+1, got, "01B")
		}
	}
}

func TestClientBackoffIsCapped(t *testing.T) {
	t.Parallel()

	server, _ := newStreamServer(t, closeImmediately)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := &sleeper{stopAfter: 5, cancel: cancel}
	client := newClient(t, server.URL,
		wanderer.WithSleep(stop.sleep),
		wanderer.WithBackoff(100*time.Millisecond, 250*time.Millisecond),
		wanderer.WithJitter(0),
	)
	if err := client.Run(ctx, nil, failOnEvent(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v, want context.Canceled", err)
	}

	want := []time.Duration{
		100 * time.Millisecond, 200 * time.Millisecond,
		250 * time.Millisecond, 250 * time.Millisecond, 250 * time.Millisecond,
	}
	if got := stop.recorded(); !equalDurations(got, want) {
		t.Errorf("backoff delays = %v, want %v", got, want)
	}
}

func TestClientBackoffIsJittered(t *testing.T) {
	t.Parallel()

	server, _ := newStreamServer(t, closeImmediately)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := &sleeper{stopAfter: 1, cancel: cancel}
	client := newClient(t, server.URL,
		wanderer.WithSleep(stop.sleep),
		wanderer.WithBackoff(time.Second, time.Minute),
	)
	if err := client.Run(ctx, nil, failOnEvent(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v, want context.Canceled", err)
	}

	delays := stop.recorded()
	if len(delays) != 1 {
		t.Fatalf("recorded %d delays, want 1", len(delays))
	}
	low, high := 800*time.Millisecond, 1200*time.Millisecond
	if delays[0] < low || delays[0] > high {
		t.Errorf("delay = %v, want it within [%v, %v]", delays[0], low, high)
	}
}

func TestClientBackoffResetsAfterAKeepalive(t *testing.T) {
	t.Parallel()

	server, _ := newStreamServer(t,
		writeFrames(": keepalive\n\n"),
		closeImmediately,
		writeFrames(": keepalive\n\n"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := &sleeper{stopAfter: 3, cancel: cancel}
	client := newClient(t, server.URL,
		wanderer.WithSleep(stop.sleep),
		wanderer.WithBackoff(100*time.Millisecond, time.Second),
		wanderer.WithJitter(0),
	)
	if err := client.Run(ctx, nil, failOnEvent(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v, want context.Canceled", err)
	}

	// A keepalive alone counts as a healthy connection, so the third wait is
	// back down to the minimum.
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 100 * time.Millisecond}
	if got := stop.recorded(); !equalDurations(got, want) {
		t.Errorf("backoff delays = %v, want %v", got, want)
	}
}

func TestClientReplacesASilentConnection(t *testing.T) {
	t.Parallel()

	server, recorder := newStreamServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flush(w)
		// Say nothing at all until the relay gives up on this connection.
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logs := &logBuffer{}
	stop := &sleeper{stopAfter: 2, cancel: cancel}
	client := newClient(t, server.URL,
		wanderer.WithSleep(stop.sleep),
		wanderer.WithStaleTimeout(50*time.Millisecond),
		wanderer.WithLogger(logs.logger(slog.LevelInfo)),
	)
	if err := client.Run(ctx, nil, failOnEvent(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v, want context.Canceled", err)
	}

	if got := recorder.count(); got < 2 {
		t.Errorf("made %d requests, want the silent connection to be retried at least once", got)
	}
	if text := logs.String(); !strings.Contains(text, "delivered nothing for 50ms") {
		t.Errorf("the stale connection was not reported:\n%s", text)
	}
}

func TestClientFatalResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		body     string
		wantCode string
		wantHint string
	}{
		{
			name:     "rejected token",
			status:   http.StatusUnauthorized,
			body:     `{"error":"Invalid or missing API key"}`,
			wantHint: "public API key",
		},
		{
			name:     "subscription required",
			status:   http.StatusPaymentRequired,
			body:     `{"code":"SUBSCRIPTION_REQUIRED","message":"Active subscription required"}`,
			wantCode: "SUBSCRIPTION_REQUIRED",
			wantHint: "subscription",
		},
		{
			name:     "sse disabled for the map",
			status:   http.StatusForbidden,
			body:     `{"error":{"code":"SSE_DISABLED_FOR_MAP","message":"SSE is not enabled for this map"}}`,
			wantCode: "SSE_DISABLED_FOR_MAP",
			wantHint: "map settings",
		},
		{
			name:     "forbidden without a code",
			status:   http.StatusForbidden,
			body:     "forbidden",
			wantHint: "not allowed to stream this map",
		},
		{
			name:     "unknown map",
			status:   http.StatusNotFound,
			body:     `{"error":"Map not found"}`,
			wantHint: "map ID or slug",
		},
		{
			name:     "sse disabled on the server, as plain text",
			status:   http.StatusServiceUnavailable,
			body:     "Server-Sent Events are disabled on this server",
			wantHint: "Server-Sent Events disabled",
		},
		{
			name:     "sse disabled on the server, as json",
			status:   http.StatusServiceUnavailable,
			body:     `{"code":"SSE_GLOBALLY_DISABLED","message":"Server-Sent Events are disabled"}`,
			wantCode: "SSE_GLOBALLY_DISABLED",
			wantHint: "Server-Sent Events disabled",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server, recorder := newStreamServer(t, respondWith(test.status, test.body))
			logs := &logBuffer{}
			client := newClient(t, server.URL,
				wanderer.WithSleep(failOnSleep(t)),
				wanderer.WithLogger(logs.logger(slog.LevelInfo)),
			)

			err := client.Run(context.Background(), nil, failOnEvent(t))
			if !wanderer.IsFatal(err) {
				t.Fatalf("Run() = %v, want a fatal error", err)
			}
			var fatal *wanderer.FatalError
			if !errors.As(err, &fatal) {
				t.Fatalf("Run() = %v, want a *wanderer.FatalError", err)
			}
			if fatal.StatusCode != test.status {
				t.Errorf("StatusCode = %d, want %d", fatal.StatusCode, test.status)
			}
			if fatal.Code != test.wantCode {
				t.Errorf("Code = %q, want %q", fatal.Code, test.wantCode)
			}
			if fatal.Message == "" {
				t.Error("Message is empty, want the server's error message")
			}
			if !strings.Contains(fatal.Hint, test.wantHint) {
				t.Errorf("Hint = %q, want it to mention %q", fatal.Hint, test.wantHint)
			}
			if !strings.Contains(err.Error(), fmt.Sprint(test.status)) {
				t.Errorf("Error() = %q, want it to report the status", err.Error())
			}
			if got := recorder.count(); got != 1 {
				t.Errorf("made %d requests, want exactly 1 with no retry", got)
			}
		})
	}
}

func TestClientRetriesTransientResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		body    string
		wantLog string
	}{
		{
			name:    "map connection limit",
			status:  http.StatusTooManyRequests,
			body:    `{"code":"MAP_CONNECTION_LIMIT","message":"Too many connections for this map"}`,
			wantLog: "MAP_CONNECTION_LIMIT",
		},
		{
			name:    "api key connection limit",
			status:  http.StatusTooManyRequests,
			body:    `{"error":{"code":"API_KEY_CONNECTION_LIMIT"}}`,
			wantLog: "API_KEY_CONNECTION_LIMIT",
		},
		{
			name:    "service unavailable for another reason",
			status:  http.StatusServiceUnavailable,
			body:    "upstream is restarting",
			wantLog: "503",
		},
		{
			name:    "internal server error",
			status:  http.StatusInternalServerError,
			body:    `{"error":"boom"}`,
			wantLog: "500",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server, recorder := newStreamServer(t, respondWith(test.status, test.body))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			logs := &logBuffer{}
			stop := &sleeper{stopAfter: 2, cancel: cancel}
			client := newClient(t, server.URL,
				wanderer.WithSleep(stop.sleep),
				wanderer.WithLogger(logs.logger(slog.LevelInfo)),
			)

			err := client.Run(ctx, nil, failOnEvent(t))
			if wanderer.IsFatal(err) {
				t.Fatalf("Run() = %v, want a retry instead of a fatal error", err)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Run() = %v, want context.Canceled", err)
			}
			if got := recorder.count(); got != 2 {
				t.Errorf("made %d requests, want 2", got)
			}
			if text := logs.String(); !strings.Contains(text, test.wantLog) {
				t.Errorf("the retry was not logged with %q:\n%s", test.wantLog, text)
			}
		})
	}
}

func TestClientKeepsTheTokenOutOfURLsAndLogs(t *testing.T) {
	t.Parallel()

	server, recorder := newStreamServer(t,
		writeFrames(connectedFrame("01A")+killFrame("01B", 1)),
		respondWith(http.StatusUnauthorized, `{"error":"Invalid API key"}`),
	)
	logs := &logBuffer{}
	client := newClient(t, server.URL,
		wanderer.WithSleep(func(context.Context, time.Duration) error { return nil }),
		wanderer.WithLogger(logs.logger(slog.LevelDebug)),
	)

	err := client.Run(context.Background(), func() string { return "01B" }, func(wanderer.Envelope) error { return nil })
	if !wanderer.IsFatal(err) {
		t.Fatalf("Run() = %v, want a fatal error", err)
	}

	if strings.Contains(err.Error(), testToken) {
		t.Error("the error message leaks the token")
	}
	if text := logs.String(); strings.Contains(text, testToken) {
		t.Errorf("the logs leak the token:\n%s", text)
	}
	for i, request := range recorder.all() {
		if strings.Contains(request.URI, testToken) {
			t.Errorf("request %d URI %q leaks the token", i, request.URI)
		}
	}
}

func TestClientStopsOnAHandlerError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("state is unwritable")
	server, recorder := newStreamServer(t, writeFrames(connectedFrame("01A")+killFrame("01B", 1)+killFrame("01C", 2)))
	client := newClient(t, server.URL, wanderer.WithSleep(failOnSleep(t)))

	calls := 0
	err := client.Run(context.Background(), nil, func(wanderer.Envelope) error {
		calls++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() = %v, want it to wrap %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), "01B") {
		t.Errorf("Run() = %q, want it to name the event", err.Error())
	}
	if calls != 1 {
		t.Errorf("handler was called %d times, want 1", calls)
	}
	if got := recorder.count(); got != 1 {
		t.Errorf("made %d requests, want no reconnect", got)
	}
}

func TestClientRunRequiresAHandler(t *testing.T) {
	t.Parallel()

	client := newClient(t, "https://wanderer.example")
	if err := client.Run(context.Background(), nil, nil); err == nil {
		t.Fatal("Run() with no handler succeeded, want an error")
	}
}

func TestNewRejectsIncompleteConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		base, id, token string
	}{
		{name: "no base URL", id: testMap, token: testToken},
		{name: "no map", base: "https://wanderer.example", token: testToken},
		{name: "no token", base: "https://wanderer.example", id: testMap},
		{name: "no scheme", base: "wanderer.example", id: testMap, token: testToken},
		{name: "no host", base: "https://", id: testMap, token: testToken},
		{name: "unsupported scheme", base: "ws://wanderer.example", id: testMap, token: testToken},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if _, err := wanderer.New(test.base, test.id, test.token); err == nil {
				t.Fatal("New() succeeded, want an error")
			}
		})
	}
}

func TestNewKeepsABasePath(t *testing.T) {
	t.Parallel()

	server, recorder := newStreamServer(t, closeImmediately)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := &sleeper{stopAfter: 1, cancel: cancel}
	client, err := wanderer.New(server.URL+"/wanderer/", testMap, testToken,
		wanderer.WithSleep(stop.sleep), wanderer.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if err := client.Run(ctx, nil, failOnEvent(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v, want context.Canceled", err)
	}

	requests := recorder.all()
	if len(requests) == 0 {
		t.Fatal("the stream endpoint was never requested")
	}
	if want := "/wanderer/api/maps/" + testMap + "/events/stream"; requests[0].Path != want {
		t.Errorf("request path = %q, want %q", requests[0].Path, want)
	}
}

// --- helpers ---

// newClient builds a client for the test server at base.
func newClient(t *testing.T, base string, opts ...wanderer.Option) *wanderer.Client {
	t.Helper()

	opts = append([]wanderer.Option{wanderer.WithLogger(slog.New(slog.DiscardHandler))}, opts...)
	client, err := wanderer.New(base, testMap, testToken, opts...)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	return client
}

// recordedRequest is what a test asserts about a stream request. The request
// itself does not outlive the handler, so the interesting parts are copied.
type recordedRequest struct {
	Path   string
	Query  url.Values
	Header http.Header
	URI    string
}

// recorder collects the requests a test server received.
type recorder struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (r *recorder) add(req *http.Request) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, recordedRequest{
		Path:   req.URL.Path,
		Query:  req.URL.Query(),
		Header: req.Header.Clone(),
		URI:    req.URL.RequestURI(),
	})
	return len(r.requests) - 1
}

func (r *recorder) all() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedRequest(nil), r.requests...)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// newStreamServer serves one handler per connection, in order, and repeats the
// last one for every further connection.
func newStreamServer(t *testing.T, handlers ...http.HandlerFunc) (*httptest.Server, *recorder) {
	t.Helper()

	rec := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers[min(rec.add(r), len(handlers)-1)](w, r)
	}))
	t.Cleanup(server.Close)
	return server, rec
}

// writeFrames answers with an event stream holding frames and then ends it.
func writeFrames(frames string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, frames)
		flush(w)
	}
}

// closeImmediately accepts the connection and ends the stream without sending
// anything, which is a connection the backoff must not treat as healthy.
func closeImmediately(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flush(w)
}

// respondWith answers with an error status and body.
func respondWith(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func flush(w http.ResponseWriter) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// sleeper stands in for the backoff wait: it records what would have been
// waited and cancels the run once stopAfter waits have happened.
type sleeper struct {
	stopAfter int
	cancel    context.CancelFunc

	mu     sync.Mutex
	delays []time.Duration
}

func (s *sleeper) sleep(_ context.Context, d time.Duration) error {
	s.mu.Lock()
	s.delays = append(s.delays, d)
	count := len(s.delays)
	s.mu.Unlock()

	if count >= s.stopAfter {
		s.cancel()
		return context.Canceled
	}
	return nil
}

func (s *sleeper) recorded() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.delays...)
}

// failOnSleep fails the test if the client waits to reconnect at all.
func failOnSleep(t *testing.T) func(context.Context, time.Duration) error {
	t.Helper()
	return func(_ context.Context, d time.Duration) error {
		t.Errorf("the client waited %v to reconnect, want no retry", d)
		return context.Canceled
	}
}

// failOnEvent fails the test if any event reaches the handler.
func failOnEvent(t *testing.T) func(wanderer.Envelope) error {
	t.Helper()
	return func(envelope wanderer.Envelope) error {
		t.Errorf("handler received event %q, want none", envelope.ID)
		return nil
	}
}

// logBuffer collects log output for assertions about what was reported.
type logBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (l *logBuffer) logger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(l, &slog.HandlerOptions{Level: level}))
}

// connectedFrame is the frame Wanderer opens every connection with.
func connectedFrame(id string) string {
	return frame(id, wanderer.EventConnected, fmt.Sprintf(
		`{"id":%q,"type":"connected","map_id":%q,"timestamp":"2026-10-14T12:00:00.123456Z","payload":{"server_time":"2026-10-14T12:00:00.123456Z","events":["map_kill"]}}`,
		id, testMapID))
}

// killFrame is a map_kill frame carrying one killmail, shaped like Wanderer's
// killmail_update payload.
func killFrame(id string, killmailID int64) string {
	return frame(id, wanderer.EventMapKill, fmt.Sprintf(
		`{"id":%q,"type":"map_kill","map_id":%q,"timestamp":"2026-10-14T12:00:31.004000Z","payload":{"type":"killmail_update","solar_system_id":31002604,"timestamp":"2026-10-14T12:00:30Z","killmails":[{"killmail_id":%d,"kill_time":"2026-10-14T11:59:58Z","solar_system_id":31002604,"victim_ship_type_id":670,"attacker_count":3,"total_value":412500000.0,"npc":false}]}}`,
		id, testMapID, killmailID))
}

func frame(id, eventType, data string) string {
	return fmt.Sprintf("id: %s\nevent: %s\ndata: %s\n\n", id, eventType, data)
}

// killmailID reads the single killmail ID out of a kill payload.
func killmailID(t *testing.T, payload json.RawMessage) int64 {
	t.Helper()

	var decoded struct {
		Killmails []struct {
			ID int64 `json:"killmail_id"`
		} `json:"killmails"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decoding the payload %s: %v", payload, err)
	}
	if len(decoded.Killmails) != 1 {
		t.Fatalf("payload %s holds %d killmails, want 1", payload, len(decoded.Killmails))
	}
	return decoded.Killmails[0].ID
}

func equalDurations(got, want []time.Duration) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
