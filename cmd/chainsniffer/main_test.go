package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Vapor-Lock/chainsniffer/internal/config"
	"github.com/Vapor-Lock/chainsniffer/internal/discord"
	"github.com/Vapor-Lock/chainsniffer/internal/killmail"
	"github.com/Vapor-Lock/chainsniffer/internal/state"
	"github.com/bwmarrin/discordgo"
)

// The identities the fakes expect. The tokens are checked, so a relay that
// sent the wrong one would fail rather than quietly pass.
const (
	testMap           = "my-chain"
	testMapID         = "f1b0d0c4-1f3a-4f6f-9a01-2c5d7e8b9f10"
	testWandererToken = "wanderer-public-api-key-ssshh"
	testDiscordToken  = "discord-bot-token-ssshh"
	testChannelID     = "123456789012345678"
	testGuildID       = "876543210987654321"
	testBotID         = "111111111111111111"

	// testCorpID is the victim's corporation, configured as friendly, which
	// makes every posted kill a loss.
	testCorpID = 98000001

	testSystemID   = 31002604
	testSystemName = "J151337"
)

// The killmails the stream carries. The NPC kill is the one the default
// filters drop.
const (
	postedKillID = 128412345
	npcKillID    = 128412346
)

// The event IDs of the two runs' events, in the ULID shape Wanderer uses.
const (
	firstEventID  = "01JBQ8H0000000000000000001"
	secondEventID = "01JBQ8H0000000000000000002"
)

// apiPrefix is the path every discordgo REST call sits under.
var apiPrefix = "/api/v" + discordgo.APIVersion + "/"

// gatewayPath is where the fake Discord answers the gateway websocket.
const gatewayPath = "/gateway-ws/"

// TestRelayPostsFiltersAndResumes is the end-to-end run: a relay wired to
// nothing but fakes posts the kills worth posting, drops the ones the filters
// reject, records what it did, and on a restart neither reposts a backfilled
// killmail nor loses its place.
//
// It covers the relay-runtime spec's "Structured logging", "Persistent relay
// state" and "Secrets are never logged", and the stream resume of
// wanderer-event-stream's "Resuming after a disconnect".
func TestRelayPostsFiltersAndResumes(t *testing.T) {
	dir := t.TempDir()
	esi := newFakeESI(t)
	discordFake, discordURL := newFakeDiscord(t)

	// One event carrying two killmails: an NPC kill, which the default
	// filters drop, and a real one, which is posted.
	first, firstURL := newFakeWanderer(t, killFrame(firstEventID,
		killmailJSON(npcKillID, true), killmailJSON(postedKillID, false)))

	logs, stop := startRelay(t, testEnv(dir, firstURL), withDiscordBaseURL(discordURL), withESIBaseURL(esi))
	waitFor(t, "the first event to be recorded", func() bool {
		return readState(t, dir).LastEventID == firstEventID
	})
	if code := stop(); code != exitOK {
		t.Errorf("run() = %d, want %d (a cancelled run is a clean shutdown); logs:\n%s", code, exitOK, logs)
	}

	posts := discordFake.posted()
	if len(posts) != 1 {
		t.Fatalf("Discord received %d posts, want 1 (the NPC kill is filtered); logs:\n%s", len(posts), logs)
	}
	if want := fmt.Sprintf("https://zkillboard.com/kill/%d/", postedKillID); posts[0].URL != want {
		t.Errorf("posted embed URL = %q, want %q", posts[0].URL, want)
	}
	if want := discord.ColorLoss; posts[0].Color != want {
		t.Errorf("posted embed color = %#x, want %#x: the victim corp is configured as friendly", posts[0].Color, want)
	}
	if !hasField(posts[0], testSystemName) {
		t.Errorf("posted embed has no field naming system %q: %+v", testSystemName, posts[0].Fields)
	}

	// The state is what makes the restart below safe: the cursor to resume
	// from, and the killmail that must not be posted twice.
	saved := readState(t, dir)
	if want := []int64{postedKillID}; !equalIDs(saved.Posted, want) {
		t.Errorf("state records posted killmails %v, want %v: only an accepted post counts", saved.Posted, want)
	}

	// Every log line is one JSON object, the configuration summary is
	// redacted, and the posted kill is reported with its classification.
	assertJSONLogs(t, logs.String())
	if strings.Contains(logs.String(), testDiscordToken) || strings.Contains(logs.String(), testWandererToken) {
		t.Error("a token reached the logs")
	}
	assertLogged(t, logs.String(), map[string]any{"msg": "starting chainsniffer"})
	assertLogged(t, logs.String(), map[string]any{
		"msg": "subscribed to the Wanderer map event stream", "map": testMap,
	})
	assertLogged(t, logs.String(), map[string]any{
		"msg": "posted killmail", "killmail_id": float64(postedKillID),
		"classification": string(killmail.Loss), "solar_system": testSystemName,
	})

	// The restart: the same state directory, and a stream that replays the
	// killmail the first run already posted.
	second, secondURL := newFakeWanderer(t, killFrame(secondEventID, killmailJSON(postedKillID, false)))

	logs, stop = startRelay(t, testEnv(dir, secondURL), withDiscordBaseURL(discordURL), withESIBaseURL(esi))
	waitFor(t, "the second event to be recorded", func() bool {
		return readState(t, dir).LastEventID == secondEventID
	})
	if code := stop(); code != exitOK {
		t.Errorf("second run() = %d, want %d; logs:\n%s", code, exitOK, logs)
	}

	if got := len(discordFake.posted()); got != 1 {
		t.Errorf("Discord received %d posts in total, want 1: a backfilled duplicate must not be reposted", got)
	}
	if got := first.queries(); len(got) == 0 || got[0].Get("last_event_id") != "" {
		t.Errorf("the first run asked for last_event_id=%q, want none", got[0].Get("last_event_id"))
	}
	if got := second.queries(); len(got) == 0 || got[0].Get("last_event_id") != firstEventID {
		t.Errorf("the second run asked for last_event_id=%q, want %q",
			got[0].Get("last_event_id"), firstEventID)
	}

	// relay-runtime's "Home system unset": no topology requests, and a
	// subscription to kills only.
	for _, w := range []*fakeWanderer{first, second} {
		if got := w.snapshotRequests(); len(got) != 0 {
			t.Errorf("without a home system the relay requested /systems %d times, want never", len(got))
		}
		if got := w.queries(); len(got) == 0 || got[0].Get("events") != "map_kill" {
			t.Errorf("without a home system the stream asked for events=%q, want %q", got[0].Get("events"), "map_kill")
		}
	}
}

// The systems of the home-chain test's map: home, linked to testSystemID
// (where the fake ESI knows a name, so a kill there can be posted), and an
// island of two systems linked to neither.
const (
	homeSystemID    = 31000727
	islandSystemID  = 31000100
	island2SystemID = 31000101
)

// TestRelayFiltersToTheHomeChain covers the end-to-end path of the linked-system
// filter: chain-topology's "Snapshot on every connection" and "Topology
// events", and relay-runtime's "Home system set". A kill in an island is
// dropped, one in a linked system is posted, and once a connection_removed
// cuts that system off, the next kill there is dropped too.
func TestRelayFiltersToTheHomeChain(t *testing.T) {
	const (
		islandKillID  = 128412400
		linkedKillID  = 128412401
		cutOffKillID  = 128412402
		islandEventID = "01JBQ8H0000000000000000011"
		linkedEventID = "01JBQ8H0000000000000000012"
		removeEventID = "01JBQ8H0000000000000000013"
		cutOffEventID = "01JBQ8H0000000000000000014"
	)

	dir := t.TempDir()
	esi := newFakeESI(t)
	discordFake, discordURL := newFakeDiscord(t)
	wandererFake, wandererURL := newFakeWanderer(t,
		killFrameIn(islandEventID, islandSystemID, killmailJSONIn(islandKillID, islandSystemID, false))+
			killFrame(linkedEventID, killmailJSON(linkedKillID, false))+
			connectionRemovedFrame(removeEventID, testSystemID, homeSystemID)+
			killFrame(cutOffEventID, killmailJSON(cutOffKillID, false)))
	wandererFake.snapshot = snapshotJSON(
		[]int64{homeSystemID, testSystemID, islandSystemID, island2SystemID},
		[2]int64{homeSystemID, testSystemID}, [2]int64{islandSystemID, island2SystemID})

	vars := testEnv(dir, wandererURL)
	vars[config.EnvHomeSystemID] = fmt.Sprint(homeSystemID)
	logs, stop := startRelay(t, vars, withDiscordBaseURL(discordURL), withESIBaseURL(esi))
	waitFor(t, "the last event to be recorded", func() bool {
		return readState(t, dir).LastEventID == cutOffEventID
	})
	if code := stop(); code != exitOK {
		t.Errorf("run() = %d, want %d; logs:\n%s", code, exitOK, logs)
	}

	posts := discordFake.posted()
	if len(posts) != 1 {
		t.Fatalf("Discord received %d posts, want 1 (only the kill in the linked system); logs:\n%s", len(posts), logs)
	}
	if want := fmt.Sprintf("https://zkillboard.com/kill/%d/", linkedKillID); posts[0].URL != want {
		t.Errorf("posted embed URL = %q, want %q", posts[0].URL, want)
	}
	for _, id := range []int64{islandKillID, cutOffKillID} {
		assertLogged(t, logs.String(), map[string]any{
			"msg": "dropping killmail", "killmail_id": float64(id), "filter": killmail.FilterUnlinkedSystem,
		})
	}
	assertLogged(t, logs.String(), map[string]any{
		"msg": "loaded map topology", "systems": float64(4), "linked_systems": float64(2),
	})

	snapshots := wandererFake.snapshotRequests()
	if len(snapshots) == 0 {
		t.Fatal("the relay never requested /systems")
	}
	if got, want := snapshots[0].Get("Authorization"), "Bearer "+testWandererToken; got != want {
		t.Errorf("the snapshot request's Authorization = %q, want %q", got, want)
	}
	if got, want := snapshots[0].Get("Accept"), "application/json"; got != want {
		t.Errorf("the snapshot request's Accept = %q, want %q", got, want)
	}
	if got := wandererFake.queries(); len(got) == 0 ||
		got[0].Get("events") != "map_kill,connection_added,connection_removed,deleted_system" {
		t.Errorf("the stream asked for events=%q, want map_kill and the three topology types", got[0].Get("events"))
	}
}

// TestRelayExitsZeroOnSIGTERM covers relay-runtime's "Graceful shutdown": the
// signal a container runtime sends stops the relay, which saves its state and
// exits 0.
func TestRelayExitsZeroOnSIGTERM(t *testing.T) {
	dir := t.TempDir()
	esi := newFakeESI(t)
	discordFake, discordURL := newFakeDiscord(t)
	_, wandererURL := newFakeWanderer(t, killFrame(firstEventID, killmailJSON(postedKillID, false)))

	logs, codes := runRelay(t, testEnv(dir, wandererURL), withDiscordBaseURL(discordURL), withESIBaseURL(esi))

	// Waiting for the post proves the relay is past the point where it
	// installs its signal handler, so the signal cannot reach a process that
	// would still die of it.
	waitFor(t, "the killmail to be posted", func() bool { return len(discordFake.posted()) == 1 })
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("sending SIGTERM to the test process: %v", err)
	}

	code := wait(t, codes)
	if code != exitOK {
		t.Errorf("run() = %d after SIGTERM, want %d; logs:\n%s", code, exitOK, logs)
	}
	if saved := readState(t, dir); !equalIDs(saved.Posted, []int64{postedKillID}) {
		t.Errorf("state records posted killmails %v, want [%d]: the shutdown must persist the state",
			saved.Posted, postedKillID)
	}
}

// TestRunRejectsBadConfiguration covers relay-runtime's "Missing required
// variable" and "Invalid value": the relay names everything that is wrong and
// exits 2 without touching the network or the state directory.
func TestRunRejectsBadConfiguration(t *testing.T) {
	dir := t.TempDir()
	vars := testEnv(dir, "http://wanderer.invalid")
	delete(vars, config.EnvDiscordChannelID)
	vars[config.EnvMinValue] = "ten"

	var out bytes.Buffer
	if code := run(context.Background(), &out, lookup(vars)); code != exitConfig {
		t.Fatalf("run() = %d, want %d; output:\n%s", code, exitConfig, out.String())
	}
	for _, want := range []string{config.EnvDiscordChannelID, config.EnvMinValue} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the error does not name %s:\n%s", want, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, state.FileName)); !os.IsNotExist(err) {
		t.Errorf("os.Stat(%s) = %v, want it never to have been created", state.FileName, err)
	}
}

// TestRunExitsOneOnFatalFailures covers the failures an operator has to fix:
// the relay reports them and exits 1 instead of retrying forever.
func TestRunExitsOneOnFatalFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T, vars map[string]string, d *fakeDiscord, w *fakeWanderer)
		wantLog string
	}{
		{
			name: "Discord rejects the bot token",
			setup: func(_ *testing.T, _ map[string]string, d *fakeDiscord, _ *fakeWanderer) {
				d.tokenStatus = http.StatusUnauthorized
			},
			wantLog: "the Discord startup check failed",
		},
		{
			name: "Wanderer refuses the stream",
			setup: func(_ *testing.T, _ map[string]string, _ *fakeDiscord, w *fakeWanderer) {
				w.status, w.body = http.StatusForbidden, `{"error":{"code":"SSE_DISABLED_FOR_MAP"}}`
			},
			wantLog: "the relay stopped",
		},
		{
			// chain-topology's "Fatal snapshot errors".
			name: "Wanderer rejects the token on /systems",
			setup: func(_ *testing.T, vars map[string]string, _ *fakeDiscord, w *fakeWanderer) {
				vars[config.EnvHomeSystemID] = fmt.Sprint(homeSystemID)
				w.snapshotStatus = http.StatusUnauthorized
			},
			wantLog: "the map systems request",
		},
		{
			name: "the state directory cannot be created",
			setup: func(t *testing.T, vars map[string]string, _ *fakeDiscord, _ *fakeWanderer) {
				blocked := filepath.Join(t.TempDir(), "state")
				if err := os.WriteFile(blocked, nil, 0o600); err != nil {
					t.Fatalf("writing %s: %v", blocked, err)
				}
				vars[config.EnvStateDir] = filepath.Join(blocked, "dir")
			},
			wantLog: "the state directory is unusable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			esi := newFakeESI(t)
			discordFake, discordURL := newFakeDiscord(t)
			wandererFake, wandererURL := newFakeWanderer(t, "")
			vars := testEnv(t.TempDir(), wandererURL)
			tc.setup(t, vars, discordFake, wandererFake)

			logs, codes := runRelay(t, vars, withDiscordBaseURL(discordURL), withESIBaseURL(esi))
			if code := wait(t, codes); code != exitFailure {
				t.Errorf("run() = %d, want %d; logs:\n%s", code, exitFailure, logs)
			}
			if !strings.Contains(logs.String(), tc.wantLog) {
				t.Errorf("the logs do not report %q:\n%s", tc.wantLog, logs)
			}
		})
	}
}

// TestRelayResyncsTheTopology covers chain-topology's "Periodic
// resynchronisation": while the stream stays connected the topology is
// reloaded every interval, and a reload that fails is a warning that neither
// drops the stream nor stops the relay.
func TestRelayResyncsTheTopology(t *testing.T) {
	dir := t.TempDir()
	esi := newFakeESI(t)
	_, discordURL := newFakeDiscord(t)
	wandererFake, wandererURL := newFakeWanderer(t, "")
	wandererFake.snapshot = snapshotJSON([]int64{homeSystemID, testSystemID}, [2]int64{homeSystemID, testSystemID})
	wandererFake.snapshotStatus, wandererFake.snapshotOKs = http.StatusServiceUnavailable, 2

	vars := testEnv(dir, wandererURL)
	vars[config.EnvHomeSystemID] = fmt.Sprint(homeSystemID)
	vars[config.EnvTopologyResyncInterval] = "20ms"
	logs, stop := startRelay(t, vars, withDiscordBaseURL(discordURL), withESIBaseURL(esi))
	waitFor(t, "a periodic reload to fail", func() bool {
		return strings.Contains(logs.String(), "reloading the map topology failed")
	})
	if code := stop(); code != exitOK {
		t.Errorf("run() = %d, want %d: a failed periodic reload must not stop the relay; logs:\n%s",
			code, exitOK, logs)
	}

	if got := len(wandererFake.queries()); got != 1 {
		t.Errorf("the relay opened the stream %d times, want 1: a periodic reload never reconnects", got)
	}
	if got := len(wandererFake.snapshotRequests()); got < 3 {
		t.Errorf("the relay requested /systems %d times, want the connect snapshot and periodic reloads", got)
	}
}

// testEnv is the relay's environment: the fakes' addresses, the credentials
// they expect, and the friendly corporation that makes a kill a loss. Nothing
// else is set, so the run exercises the documented defaults — NPC kills
// filtered, kills older than an hour dropped, state in CHAINSNIFFER_STATE_DIR.
func testEnv(stateDir, wandererURL string) map[string]string {
	return map[string]string{
		config.EnvWandererURL:      wandererURL,
		config.EnvWandererMap:      testMap,
		config.EnvWandererToken:    testWandererToken,
		config.EnvDiscordToken:     testDiscordToken,
		config.EnvDiscordChannelID: testChannelID,
		// The space is deliberate: an operator's list is read as written.
		config.EnvFriendlyCorpIDs: fmt.Sprintf("%d, 98000002", testCorpID),
		config.EnvStateDir:        stateDir,
		config.EnvLogLevel:        "debug",
		config.EnvLogFormat:       "json",
	}
}

// lookup turns an environment map into what config.Load reads.
func lookup(vars map[string]string) config.LookupFunc {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

// runRelay starts the relay in the background and returns its logs and the
// channel its exit status arrives on. The run is cancelled when the test ends,
// so a test that fails early does not leave it running.
func runRelay(t *testing.T, vars map[string]string, opts ...option) (*syncBuffer, <-chan int) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	logs := &syncBuffer{}
	codes := make(chan int, 1)
	go func() { codes <- run(ctx, logs, lookup(vars), opts...) }()
	return logs, codes
}

// startRelay starts the relay and returns its logs together with a function
// that shuts it down and reports its exit status.
func startRelay(t *testing.T, vars map[string]string, opts ...option) (*syncBuffer, func() int) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	logs := &syncBuffer{}
	codes := make(chan int, 1)
	go func() { codes <- run(ctx, logs, lookup(vars), opts...) }()

	return logs, func() int {
		t.Helper()
		cancel()
		return wait(t, codes)
	}
}

// wait reads the run's exit status, failing the test if it never comes.
func wait(t *testing.T, codes <-chan int) int {
	t.Helper()

	select {
	case code := <-codes:
		return code
	case <-time.After(30 * time.Second):
		t.Fatal("the relay did not exit within 30s")
		return -1
	}
}

// waitFor polls cond until it holds, failing the test if it never does. The
// fakes and the relay run in their own goroutines, so every assertion about
// what the relay has done so far waits for it here first.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// contents is the state file as the tests read it, which is the shape
// internal/state writes.
type contents struct {
	LastEventID string  `json:"last_event_id"`
	Posted      []int64 `json:"posted"`
}

// readState reads the state file, returning the zero value while it does not
// exist yet.
func readState(t *testing.T, dir string) contents {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, state.FileName))
	if os.IsNotExist(err) {
		return contents{}
	}
	if err != nil {
		t.Fatalf("reading the state file: %v", err)
	}
	var c contents
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("decoding the state file %s: %v", data, err)
	}
	return c
}

func equalIDs(got, want []int64) bool {
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

// hasField reports whether one of the embed's fields carries value.
func hasField(embed *discordgo.MessageEmbed, value string) bool {
	for _, f := range embed.Fields {
		if f.Value == value {
			return true
		}
	}
	return false
}

// assertJSONLogs checks that every line of the log is one JSON object, which
// is what CHAINSNIFFER_LOG_FORMAT=json promises.
func assertJSONLogs(t *testing.T, logs string) {
	t.Helper()

	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		if line == "" {
			continue
		}
		var object map[string]any
		if err := json.Unmarshal([]byte(line), &object); err != nil {
			t.Fatalf("log line %q is not one JSON object: %v", line, err)
		}
		if _, ok := object["msg"]; !ok {
			t.Errorf("log line %q carries no message", line)
		}
	}
}

// assertLogged checks that one log line carries every one of the given fields.
func assertLogged(t *testing.T, logs string, want map[string]any) {
	t.Helper()

	for _, line := range strings.Split(logs, "\n") {
		var got map[string]any
		if line == "" || json.Unmarshal([]byte(line), &got) != nil {
			continue
		}
		matched := true
		for key, value := range want {
			if got[key] != value {
				matched = false
				break
			}
		}
		if matched {
			return
		}
	}
	t.Errorf("no log line carries %v:\n%s", want, logs)
}

// syncBuffer is the relay's standard error. slog serialises its own writes,
// but the test reads the buffer while the relay is still running.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeWanderer serves one map's kill stream. Every connection gets the
// "connected" event, then the scripted frames, and is then held open until the
// relay goes away, so the client has no reason to reconnect mid-test.
type fakeWanderer struct {
	frames string
	// status and body, when status is non-zero, are the refusal the stream
	// endpoint answers with instead.
	status int
	body   string
	// snapshot is the body /systems answers with, and snapshotStatus, when
	// non-zero, the refusal it answers with instead — from the first request
	// on, or once snapshotOKs requests have been answered.
	snapshot       string
	snapshotStatus int
	snapshotOKs    int

	mu        sync.Mutex
	queried   []url.Values
	snapshots []http.Header
}

// newFakeWanderer returns a fake serving frames, and the base URL to configure
// the relay with.
func newFakeWanderer(t *testing.T, frames string) (*fakeWanderer, string) {
	t.Helper()

	f := &fakeWanderer{frames: frames}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	return f, server.URL
}

func (f *fakeWanderer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/maps/"+testMap+"/systems" {
		f.serveSnapshot(w, r)
		return
	}

	f.mu.Lock()
	f.queried = append(f.queried, r.URL.Query())
	f.mu.Unlock()

	if want := "/api/maps/" + testMap + "/events/stream"; r.URL.Path != want {
		http.Error(w, "unexpected path "+r.URL.Path+", want "+want, http.StatusNotFound)
		return
	}
	if want := "Bearer " + testWandererToken; r.Header.Get("Authorization") != want {
		http.Error(w, "unexpected Authorization header", http.StatusUnauthorized)
		return
	}
	if f.status != 0 {
		http.Error(w, f.body, f.status)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, connectedFrame()+f.frames)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	<-r.Context().Done()
}

// serveSnapshot answers GET /api/maps/{map}/systems, the topology snapshot.
func (f *fakeWanderer) serveSnapshot(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.snapshots = append(f.snapshots, r.Header.Clone())
	served := len(f.snapshots) - 1
	f.mu.Unlock()

	if want := "Bearer " + testWandererToken; r.Header.Get("Authorization") != want {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if f.snapshotStatus != 0 && served >= f.snapshotOKs {
		writeJSON(w, f.snapshotStatus, map[string]any{"error": "Invalid token"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, f.snapshot)
}

// snapshotRequests returns the headers of every snapshot request the fake
// served, in order.
func (f *fakeWanderer) snapshotRequests() []http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]http.Header(nil), f.snapshots...)
}

// queries returns the query of every stream request the fake served, in order.
func (f *fakeWanderer) queries() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.queried...)
}

// connectedFrame is the first event on every Wanderer connection.
func connectedFrame() string {
	return frame("01JBQ8H0000000000000000000", "connected", fmt.Sprintf(
		`{"id":"01JBQ8H0000000000000000000","type":"connected","map_id":%q,"timestamp":%q,`+
			`"payload":{"server_time":%q,"events":["map_kill"]}}`,
		testMapID, stamp(), stamp()))
}

// killFrame is a map_kill frame carrying killmails in the killmail_update
// payload shape Wanderer's message handler sends.
func killFrame(eventID string, killmails ...string) string {
	return killFrameIn(eventID, testSystemID, killmails...)
}

// killFrameIn is killFrame for a kill in another system.
func killFrameIn(eventID string, systemID int64, killmails ...string) string {
	payload := fmt.Sprintf(`{"type":"killmail_update","solar_system_id":%d,"timestamp":%q,"killmails":[%s]}`,
		systemID, stamp(), strings.Join(killmails, ","))
	return frame(eventID, "map_kill", fmt.Sprintf(
		`{"id":%q,"type":"map_kill","map_id":%q,"timestamp":%q,"payload":%s}`,
		eventID, testMapID, stamp(), payload))
}

// killmailJSON is one entry of a killmail_update payload, flat as Wanderer
// sends it. The kill time is now, so the default one-hour age filter keeps it.
func killmailJSON(id int64, npc bool) string {
	return killmailJSONIn(id, testSystemID, npc)
}

// killmailJSONIn is killmailJSON for a kill in another system.
func killmailJSONIn(id, systemID int64, npc bool) string {
	return fmt.Sprintf(`{"killmail_id":%d,"kill_time":%q,"solar_system_id":%d,`+
		`"victim_char_id":2112000001,"victim_char_name":"Pilot %d",`+
		`"victim_corp_id":%d,"victim_corp_ticker":"CORP","victim_corp_name":"A Corporation",`+
		`"victim_ship_type_id":670,"victim_ship_name":"Capsule",`+
		`"final_blow_char_name":"Shooter","final_blow_corp_ticker":"ENMY","final_blow_ship_name":"Loki",`+
		`"attacker_count":3,"total_value":412500000.0,"npc":%t}`,
		id, stamp(), systemID, id, testCorpID, npc)
}

// connectionRemovedFrame is a connection_removed frame in the payload shape
// Wanderer's maybe_remove_connection broadcasts.
func connectionRemovedFrame(eventID string, source, target int64) string {
	payload := fmt.Sprintf(`{"connection_id":"c0ffee00-0000-4000-8000-000000000001",`+
		`"solar_system_source_id":%d,"solar_system_target_id":%d}`, source, target)
	return frame(eventID, "connection_removed", fmt.Sprintf(
		`{"id":%q,"type":"connection_removed","map_id":%q,"timestamp":%q,"payload":%s}`,
		eventID, testMapID, stamp(), payload))
}

// snapshotJSON is a /systems answer in the shape of Wanderer's
// MapSystemAPIController.index, listing systems and the connections between
// the given pairs.
func snapshotJSON(systems []int64, connections ...[2]int64) string {
	var ss, cs []string
	for _, id := range systems {
		ss = append(ss, fmt.Sprintf(`{"solar_system_id":%d,"name":"S%d","visible":true}`, id, id))
	}
	for _, c := range connections {
		cs = append(cs, fmt.Sprintf(`{"solar_system_source":%d,"solar_system_target":%d,"type":0}`, c[0], c[1]))
	}
	return fmt.Sprintf(`{"data":{"systems":[%s],"connections":[%s]}}`,
		strings.Join(ss, ","), strings.Join(cs, ","))
}

func frame(id, eventType, data string) string {
	return fmt.Sprintf("id: %s\nevent: %s\ndata: %s\n\n", id, eventType, data)
}

// stamp is the timestamp the fixtures carry, which has to be recent: the
// default filters drop a kill older than an hour.
func stamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// newFakeESI serves ESI's solar-system endpoint, and returns its base URL.
func newFakeESI(t *testing.T) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := fmt.Sprintf("/universe/systems/%d/", testSystemID); r.URL.Path != want {
			http.Error(w, "unknown system", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"system_id": testSystemID, "name": testSystemName})
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// fakeDiscord stands in for the Discord API: the REST calls the startup check
// and the posts go through, and the gateway session the relay opens.
type fakeDiscord struct {
	// tokenStatus, when non-zero, is the error answered to GET /users/@me.
	tokenStatus int
	// gatewayURL is the websocket address GET /gateway hands out, which is
	// this fake's own gateway handler.
	gatewayURL string

	mu    sync.Mutex
	posts []*discordgo.MessageEmbed
	conns []net.Conn
}

// newFakeDiscord returns a fake that accepts everything, and the API root to
// point the relay's Discord client at.
func newFakeDiscord(t *testing.T) (*fakeDiscord, string) {
	t.Helper()

	f := &fakeDiscord{}
	server := httptest.NewServer(f)
	f.gatewayURL = "ws://" + server.Listener.Addr().String() + gatewayPath
	t.Cleanup(func() {
		server.Close()
		// A hijacked connection is no longer the server's to close, and the
		// gateway handler sits reading it until it goes away.
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, conn := range f.conns {
			_ = conn.Close()
		}
	})
	return f, server.URL
}

func (f *fakeDiscord) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, gatewayPath) {
		f.serveGateway(w, r)
		return
	}
	if want := "Bot " + testDiscordToken; r.Header.Get("Authorization") != want {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "401: Unauthorized"})
		return
	}

	switch path := strings.TrimPrefix(r.URL.Path, apiPrefix); {
	case r.Method == http.MethodGet && path == "gateway":
		writeJSON(w, http.StatusOK, map[string]any{"url": f.gatewayURL})

	case r.Method == http.MethodGet && path == "users/@me":
		if f.tokenStatus != 0 {
			writeJSON(w, f.tokenStatus, map[string]any{"message": "401: Unauthorized"})
			return
		}
		writeJSON(w, http.StatusOK, &discordgo.User{ID: testBotID, Username: "chainsniffer", Bot: true})

	case r.Method == http.MethodGet && path == "channels/"+testChannelID:
		writeJSON(w, http.StatusOK, &discordgo.Channel{
			ID: testChannelID, Name: "killmails", GuildID: testGuildID, Type: discordgo.ChannelTypeGuildText,
		})

	case r.Method == http.MethodGet && path == "guilds/"+testGuildID:
		// The bot's permissions come from @everyone, the role whose ID is the
		// guild's.
		writeJSON(w, http.StatusOK, &discordgo.Guild{
			ID:      testGuildID,
			OwnerID: "999999999999999999",
			Roles: []*discordgo.Role{{ID: testGuildID, Name: "@everyone", Permissions: discordgo.PermissionViewChannel |
				discordgo.PermissionSendMessages | discordgo.PermissionEmbedLinks}},
		})

	case r.Method == http.MethodGet && path == "guilds/"+testGuildID+"/members/"+testBotID:
		writeJSON(w, http.StatusOK, &discordgo.Member{GuildID: testGuildID, User: &discordgo.User{ID: testBotID}})

	case r.Method == http.MethodPost && path == "channels/"+testChannelID+"/messages":
		f.handlePost(w, r)

	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "404: Not Found"})
	}
}

// handlePost accepts one message, recording the embed it carried.
func (f *fakeDiscord) handlePost(w http.ResponseWriter, r *http.Request) {
	var sent struct {
		Embeds []*discordgo.MessageEmbed `json:"embeds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "undecodable body"})
		return
	}
	f.mu.Lock()
	f.posts = append(f.posts, sent.Embeds...)
	f.mu.Unlock()
	writeJSON(w, http.StatusCreated, &discordgo.Message{ID: "999", ChannelID: testChannelID, Embeds: sent.Embeds})
}

// posted returns the embeds the fake accepted, in order.
func (f *fakeDiscord) posted() []*discordgo.MessageEmbed {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*discordgo.MessageEmbed(nil), f.posts...)
}

// websocketGUID is the constant a websocket handshake's accept header is
// derived from (RFC 6455, section 1.3).
const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// serveGateway plays the Discord gateway far enough for discordgo to open a
// session: it completes the websocket handshake, sends the Hello frame and the
// frame where a READY would be, and then reads whatever the client sends — its
// identify, and the close frame on shutdown — until the connection ends.
//
// The handshake and the two frames are written by hand so that discordgo's
// websocket library does not become a direct dependency of this module for the
// sake of one test. Frames from a server are unmasked and these two are short,
// which is the whole of the framing the client needs from us.
func (f *fakeDiscord) serveGateway(w http.ResponseWriter, r *http.Request) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "the gateway needs a hijackable connection", http.StatusInternalServerError)
		return
	}
	conn, buf, err := hijacker.Hijack()
	if err != nil {
		return
	}
	f.mu.Lock()
	f.conns = append(f.conns, conn)
	f.mu.Unlock()
	defer func() { _ = conn.Close() }()

	accept := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + websocketGUID))
	_, _ = fmt.Fprintf(buf, "HTTP/1.1 101 Switching Protocols\r\n"+
		"Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
		base64.StdEncoding.EncodeToString(accept[:]))
	// A heartbeat interval longer than any test keeps the client from sending
	// anything we would have to answer.
	writeFrame(buf, `{"op":10,"d":{"heartbeat_interval":600000}}`)
	writeFrame(buf, `{"op":0,"s":1,"t":"READY","d":{"v":9,"session_id":"test-session",`+
		`"user":{"id":"`+testBotID+`","username":"chainsniffer","bot":true},`+
		`"guilds":[],"private_channels":[]}}`)
	if err := buf.Flush(); err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, conn)
}

// writeFrame writes payload as one unmasked text frame.
func writeFrame(w io.Writer, payload string) {
	header := []byte{0x81}
	if n := len(payload); n < 126 {
		header = append(header, byte(n))
	} else {
		header = append(header, 126, byte(n>>8), byte(n))
	}
	_, _ = w.Write(header)
	_, _ = io.WriteString(w, payload)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
