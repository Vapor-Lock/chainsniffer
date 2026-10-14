package discord

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestNewRejectsIncompleteConfig(t *testing.T) {
	for _, tc := range []struct {
		name            string
		token, channel  string
		opts            []Option
		wantErrContains string
	}{
		{name: "no token", channel: testChannelID, wantErrContains: "token"},
		{name: "no channel", token: testToken, wantErrContains: "channel"},
		{
			name: "unparsable base URL", token: testToken, channel: testChannelID,
			opts:            []Option{WithBaseURL("http://[::1")},
			wantErrContains: "base URL",
		},
		{
			name: "base URL without a host", token: testToken, channel: testChannelID,
			opts:            []Option{WithBaseURL("127.0.0.1:8080/api")},
			wantErrContains: "base URL",
		},
		{
			name: "base URL that is only a path", token: testToken, channel: testChannelID,
			opts:            []Option{WithBaseURL("localhost/api")},
			wantErrContains: "scheme and a host",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.token, tc.channel, tc.opts...); err == nil ||
				!strings.Contains(err.Error(), tc.wantErrContains) {
				t.Errorf("New() = %v, want an error mentioning %q", err, tc.wantErrContains)
			}
		})
	}
}

func TestPostSucceeds(t *testing.T) {
	fake, url := newFakeDiscord(t)
	poster, hooks := newPoster(t, url)

	if err := poster.Post(context.Background(), testEmbed()); err != nil {
		t.Fatalf("Post() = %v", err)
	}

	posted := fake.posted()
	if len(posted) != 1 {
		t.Fatalf("fake received %d embeds, want 1", len(posted))
	}
	if got, want := posted[0].Title, testEmbed().Title; got != want {
		t.Errorf("embed title = %q, want %q", got, want)
	}
	if got := len(hooks.clock.slept); got != 0 {
		t.Errorf("Post slept %d times on a first-try success, want 0", got)
	}
}

func TestPostOrderIsPreserved(t *testing.T) {
	fake, url := newFakeDiscord(t)
	poster, _ := newPoster(t, url)

	for _, title := range []string{"first", "second", "third"} {
		if err := poster.Post(context.Background(), &discordgo.MessageEmbed{Title: title}); err != nil {
			t.Fatalf("Post(%q) = %v", title, err)
		}
	}

	var titles []string
	for _, embed := range fake.posted() {
		titles = append(titles, embed.Title)
	}
	if want := []string{"first", "second", "third"}; !slices.Equal(titles, want) {
		t.Errorf("posted titles = %v, want %v", titles, want)
	}
}

// discordgo retries a 502 itself, so the poster never sees it.
func TestPostRetriesBadGateway(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.postStatuses = []int{http.StatusBadGateway, http.StatusBadGateway, 0}
	poster, hooks := newPoster(t, url)

	if err := poster.Post(context.Background(), testEmbed()); err != nil {
		t.Fatalf("Post() = %v", err)
	}
	if got := len(fake.posted()); got != 1 {
		t.Errorf("fake accepted %d embeds, want 1", got)
	}
	if got := fake.count("/messages"); got != 3 {
		t.Errorf("fake served %d posts, want 3", got)
	}
	if got := len(hooks.clock.slept); got != 0 {
		t.Errorf("Post slept %d times, want 0: discordgo retries a 502 itself", got)
	}
}

// Statuses discordgo hands back are retried by the poster, with the delay
// doubling between attempts.
func TestPostRetriesServiceUnavailable(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.postStatuses = []int{http.StatusServiceUnavailable, http.StatusServiceUnavailable, 0}
	poster, hooks := newPoster(t, url)

	if err := poster.Post(context.Background(), testEmbed()); err != nil {
		t.Fatalf("Post() = %v", err)
	}
	if got := len(fake.posted()); got != 1 {
		t.Errorf("fake accepted %d embeds, want 1", got)
	}
	if want := []time.Duration{defaultBackoff, 2 * defaultBackoff}; !slices.Equal(hooks.clock.slept, want) {
		t.Errorf("Post slept %v, want %v", hooks.clock.slept, want)
	}
}

func TestPostGivesUpWhenBudgetIsSpent(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.postStatuses = []int{http.StatusServiceUnavailable}
	budget := 10 * time.Second
	poster, hooks := newPoster(t, url, WithRetryBudget(budget))

	err := poster.Post(context.Background(), testEmbed())

	var gaveUp *GaveUpError
	if !errors.As(err, &gaveUp) {
		t.Fatalf("Post() = %v, want a *GaveUpError", err)
	}
	if gaveUp.Attempts < 2 {
		t.Errorf("gave up after %d attempts, want more than one", gaveUp.Attempts)
	}
	if hooks.clock.total() > budget {
		t.Errorf("Post slept %v altogether, want at most the %v budget", hooks.clock.total(), budget)
	}
	if got := fake.count("/messages"); got != gaveUp.Attempts {
		t.Errorf("fake served %d posts, want %d, one per attempt", got, gaveUp.Attempts)
	}
	if got := len(fake.posted()); got != 0 {
		t.Errorf("fake accepted %d embeds, want 0", got)
	}
	var fatal *FatalError
	if errors.As(err, &fatal) {
		t.Error("giving up must not be fatal: the relay skips the kill and carries on")
	}
}

// 429s are discordgo's to wait out, from the retry_after it is given.
func TestPostWaitsOutRateLimit(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.postStatuses = []int{http.StatusTooManyRequests, 0}
	poster, hooks := newPoster(t, url)

	if err := poster.Post(context.Background(), testEmbed()); err != nil {
		t.Fatalf("Post() = %v", err)
	}
	if got := len(fake.posted()); got != 1 {
		t.Errorf("fake accepted %d embeds, want 1: a rate-limited post must not be dropped or doubled", got)
	}
	if got := fake.count("/messages"); got != 2 {
		t.Errorf("fake served %d posts, want 2", got)
	}
	if got := len(hooks.clock.slept); got != 0 {
		t.Errorf("Post slept %d times, want 0: discordgo waits out a 429 itself", got)
	}
}

func TestPostFatalStatuses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		wantHint string
	}{
		{name: "missing permissions", status: http.StatusForbidden, wantHint: "Embed Links"},
		{name: "unknown channel", status: http.StatusNotFound, wantHint: "Embed Links"},
		{name: "bad token", status: http.StatusUnauthorized, wantHint: "token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, url := newFakeDiscord(t)
			fake.postStatuses = []int{tc.status}
			poster, hooks := newPoster(t, url)

			err := poster.Post(context.Background(), testEmbed())

			var fatal *FatalError
			if !errors.As(err, &fatal) {
				t.Fatalf("Post() = %v, want a *FatalError", err)
			}
			if fatal.Status != tc.status {
				t.Errorf("FatalError.Status = %d, want %d", fatal.Status, tc.status)
			}
			if !strings.Contains(err.Error(), tc.wantHint) {
				t.Errorf("Post() = %q, want it to mention %q", err, tc.wantHint)
			}
			if got := fake.count("/messages"); got != 1 {
				t.Errorf("fake served %d posts, want 1: a fatal error must not be retried", got)
			}
			if got := len(hooks.clock.slept); got != 0 {
				t.Errorf("Post slept %d times before failing fatally, want 0", got)
			}
		})
	}
}

// A rejection that is neither fatal nor transient fails the one post.
func TestPostBadRequestIsNotRetried(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.postStatuses = []int{http.StatusBadRequest}
	poster, _ := newPoster(t, url)

	err := poster.Post(context.Background(), testEmbed())
	if err == nil {
		t.Fatal("Post() = nil, want an error")
	}
	var fatal *FatalError
	var gaveUp *GaveUpError
	if errors.As(err, &fatal) || errors.As(err, &gaveUp) {
		t.Errorf("Post() = %v, want a plain error", err)
	}
	if got := fake.count("/messages"); got != 1 {
		t.Errorf("fake served %d posts, want 1", got)
	}
}

func TestPostStopsOnCancelledContext(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.postStatuses = []int{http.StatusServiceUnavailable}
	poster, _ := newPoster(t, url)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := poster.Post(ctx, testEmbed())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Post() = %v, want context.Canceled", err)
	}
	if got := fake.count("/messages"); got > 1 {
		t.Errorf("fake served %d posts, want at most 1", got)
	}
}
