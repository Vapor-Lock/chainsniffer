package discord

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	testToken     = "test-token"
	testChannelID = "123456789012345678"
	testGuildID   = "876543210987654321"
	testBotID     = "111111111111111111"
)

// apiPrefix is the path every discordgo REST call sits under.
var apiPrefix = "/api/v" + discordgo.APIVersion + "/"

// fakeDiscord stands in for the Discord REST API. Its behaviour fields are set
// before the Poster is used and not touched afterwards; everything it records
// is guarded, since discordgo serves requests on the caller's goroutine but
// the server runs its own.
type fakeDiscord struct {
	// postStatuses answers successive message posts. The last entry repeats
	// once the slice runs out, and 0 means "201 with a message".
	postStatuses []int
	// retryAfter is the retry_after, in seconds, sent with a 429.
	retryAfter float64
	// rateLimitHeaders adds Discord's X-RateLimit-* headers to every post
	// response: the bucket is exhausted on a 429 and has room otherwise, and
	// resets after retryAfter either way.
	rateLimitHeaders bool
	// tokenStatus and channelStatus, when non-zero, are the errors answered
	// to GET /users/@me and GET /channels/{id}.
	tokenStatus   int
	channelStatus int
	// permissions are the permissions the bot resolves to on the channel.
	permissions int64
	// guildStatus, when non-zero, is the error answered to the guild fetch,
	// which makes the permissions unresolvable.
	guildStatus int
	// channelGuildID is the channel's guild; empty makes it a DM channel.
	channelGuildID string

	mu       sync.Mutex
	attempts int
	posts    []*discordgo.MessageEmbed
	requests []string
}

// newFakeDiscord returns a fake that accepts everything, serving on a test
// server torn down with the test.
func newFakeDiscord(t *testing.T) (*fakeDiscord, string) {
	t.Helper()
	f := &fakeDiscord{
		permissions:    discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionEmbedLinks,
		channelGuildID: testGuildID,
		retryAfter:     0.01,
	}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	return f, server.URL
}

func (f *fakeDiscord) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	f.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, apiPrefix)
	switch {
	case r.Method == http.MethodGet && path == "users/@me":
		if f.tokenStatus != 0 {
			writeError(w, f.tokenStatus, "401: Unauthorized")
			return
		}
		writeJSON(w, http.StatusOK, &discordgo.User{ID: testBotID, Username: "chainsniffer", Bot: true})

	case r.Method == http.MethodGet && path == "channels/"+testChannelID:
		if f.channelStatus != 0 {
			writeError(w, f.channelStatus, "Unknown Channel")
			return
		}
		writeJSON(w, http.StatusOK, &discordgo.Channel{
			ID: testChannelID, Name: "killmails", GuildID: f.channelGuildID, Type: discordgo.ChannelTypeGuildText,
		})

	case r.Method == http.MethodPost && path == "channels/"+testChannelID+"/messages":
		f.handlePost(w, r)

	case r.Method == http.MethodGet && path == "guilds/"+testGuildID+"/members/"+testBotID:
		writeJSON(w, http.StatusOK, &discordgo.Member{GuildID: testGuildID, User: &discordgo.User{ID: testBotID}})

	case r.Method == http.MethodGet && path == "guilds/"+testGuildID:
		if f.guildStatus != 0 {
			writeError(w, f.guildStatus, "Missing Access")
			return
		}
		// The bot's permissions come from @everyone, the role whose ID is the
		// guild's. OwnerID is somebody else, or every permission would be
		// granted regardless.
		writeJSON(w, http.StatusOK, &discordgo.Guild{
			ID:      testGuildID,
			OwnerID: "999999999999999999",
			Roles:   []*discordgo.Role{{ID: testGuildID, Name: "@everyone", Permissions: f.permissions}},
		})

	default:
		writeError(w, http.StatusNotFound, "404: Not Found")
	}
}

// handlePost answers one message post with the next scripted status, recording
// the embed it carried.
func (f *fakeDiscord) handlePost(w http.ResponseWriter, r *http.Request) {
	var sent struct {
		Embeds []*discordgo.MessageEmbed `json:"embeds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
		writeError(w, http.StatusBadRequest, "undecodable body: "+err.Error())
		return
	}

	f.mu.Lock()
	attempt := f.attempts
	f.attempts++
	status := 0
	if n := len(f.postStatuses); n > 0 {
		status = f.postStatuses[min(attempt, n-1)]
	}
	if status == 0 || status == http.StatusCreated || status == http.StatusOK {
		f.posts = append(f.posts, sent.Embeds...)
	}
	f.mu.Unlock()

	if f.rateLimitHeaders {
		remaining := "1"
		if status == http.StatusTooManyRequests {
			remaining = "0"
		}
		h := w.Header()
		h.Set("X-RateLimit-Bucket", "fake-messages-bucket")
		h.Set("X-RateLimit-Limit", "5")
		h.Set("X-RateLimit-Remaining", remaining)
		h.Set("X-RateLimit-Reset-After", strconv.FormatFloat(f.retryAfter, 'f', -1, 64))
	}

	switch status {
	case 0, http.StatusCreated, http.StatusOK:
		writeJSON(w, http.StatusCreated, &discordgo.Message{ID: "999", ChannelID: testChannelID, Embeds: sent.Embeds})
	case http.StatusTooManyRequests:
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"message": "You are being rate limited.", "retry_after": f.retryAfter, "global": false,
		})
	case http.StatusForbidden:
		writeError(w, status, "Missing Permissions")
	default:
		writeError(w, status, http.StatusText(status))
	}
}

// posted returns the embeds the fake accepted, in order.
func (f *fakeDiscord) posted() []*discordgo.MessageEmbed {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*discordgo.MessageEmbed(nil), f.posts...)
}

// seen returns every request the fake served, as "METHOD path".
func (f *fakeDiscord) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// count returns how many requests the fake served whose path ends in suffix.
func (f *fakeDiscord) count(suffix string) int {
	n := 0
	for _, req := range f.seen() {
		if strings.HasSuffix(req, suffix) {
			n++
		}
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"message": message, "code": 0})
}

// fakeClock is the Poster's clock under test: it never waits, it only records
// what it was asked to wait for and moves its own time along.
type fakeClock struct {
	now   time.Time
	slept []time.Duration
}

func (c *fakeClock) sleep(_ context.Context, d time.Duration) error {
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
	return nil
}

// total is how long the clock was asked to wait for altogether.
func (c *fakeClock) total() time.Duration {
	var sum time.Duration
	for _, d := range c.slept {
		sum += d
	}
	return sum
}

// testHooks are the Poster's injected seams: its clock and its gateway.
type testHooks struct {
	clock   *fakeClock
	opens   int
	closes  int
	openErr error
}

// newPoster returns a Poster aimed at the fake, with a clock that does not
// wait and a gateway that is not a websocket.
func newPoster(t *testing.T, baseURL string, opts ...Option) (*Poster, *testHooks) {
	t.Helper()
	hooks := &testHooks{clock: &fakeClock{now: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}}
	opts = append([]Option{
		WithBaseURL(baseURL),
		WithLogger(slog.New(slog.DiscardHandler)),
	}, opts...)
	p, err := New(testToken, testChannelID, opts...)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	p.now = func() time.Time { return hooks.clock.now }
	p.sleep = hooks.clock.sleep
	p.openGateway = func() error { hooks.opens++; return hooks.openErr }
	p.closeGateway = func() error { hooks.closes++; return nil }
	return p, hooks
}

// testEmbed is the embed the posting tests send.
func testEmbed() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{Title: "Stratios — Pilot A", URL: "https://zkillboard.com/kill/123456789/"}
}
