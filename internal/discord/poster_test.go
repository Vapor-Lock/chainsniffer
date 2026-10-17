package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	if err := poster.Post(context.Background(), testMessage()); err != nil {
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
		if err := poster.Post(context.Background(), Message{Embed: &discordgo.MessageEmbed{Title: title}}); err != nil {
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

	if err := poster.Post(context.Background(), testMessage()); err != nil {
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

	if err := poster.Post(context.Background(), testMessage()); err != nil {
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

	err := poster.Post(context.Background(), testMessage())

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

	if err := poster.Post(context.Background(), testMessage()); err != nil {
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

// A burst of kills is posted one after another while Discord rate-limits part
// of them: every embed must arrive, exactly once and in order.
func TestPostBurstUnderRateLimit(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.rateLimitHeaders = true
	fake.retryAfter = 0.005

	const burst = 20
	var statuses []int
	limited := 0
	for i := range burst {
		switch {
		case i%3 == 1:
			statuses = append(statuses, http.StatusTooManyRequests)
			limited++
		case i == 10:
			statuses = append(statuses, http.StatusTooManyRequests, http.StatusTooManyRequests)
			limited += 2
		}
		statuses = append(statuses, 0)
	}
	fake.postStatuses = statuses
	poster, hooks := newPoster(t, url)

	var want []string
	for i := range burst {
		title := fmt.Sprintf("kill %02d", i)
		want = append(want, title)
		if err := poster.Post(context.Background(), Message{Embed: &discordgo.MessageEmbed{Title: title}}); err != nil {
			t.Fatalf("Post(%q) = %v", title, err)
		}
	}

	var titles []string
	for _, embed := range fake.posted() {
		titles = append(titles, embed.Title)
	}
	if !slices.Equal(titles, want) {
		t.Errorf("posted titles = %v, want %v", titles, want)
	}
	if got := fake.count("/messages"); got != burst+limited {
		t.Errorf("fake served %d posts, want %d: one per embed plus one per 429", got, burst+limited)
	}
	if got := len(hooks.clock.slept); got != 0 {
		t.Errorf("Post slept %d times, want 0: discordgo waits out a 429 itself", got)
	}
}

// When discordgo is told not to wait out a 429 it hands back a
// *discordgo.RateLimitError, which the poster retries within its budget.
func TestPostRetriesRateLimitDiscordgoDeclined(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.postStatuses = []int{http.StatusTooManyRequests, http.StatusTooManyRequests, 0}
	poster, hooks := newPoster(t, url)
	poster.session.ShouldRetryOnRateLimit = false

	if err := poster.Post(context.Background(), testMessage()); err != nil {
		t.Fatalf("Post() = %v", err)
	}
	if got := len(fake.posted()); got != 1 {
		t.Errorf("fake accepted %d embeds, want 1", got)
	}
	if want := []time.Duration{defaultBackoff, 2 * defaultBackoff}; !slices.Equal(hooks.clock.slept, want) {
		t.Errorf("Post slept %v, want %v", hooks.clock.slept, want)
	}
}

func TestClassify(t *testing.T) {
	restErr := func(status int) error {
		return &discordgo.RESTError{Response: &http.Response{StatusCode: status}}
	}
	for _, tc := range []struct {
		name       string
		err        error
		wantClass  class
		wantStatus int
	}{
		{name: "rest 429", err: restErr(http.StatusTooManyRequests), wantClass: classTransient, wantStatus: 429},
		{name: "wrapped rest 429", err: fmt.Errorf("post: %w", restErr(http.StatusTooManyRequests)),
			wantClass: classTransient, wantStatus: 429},
		{name: "rate limit error", err: &discordgo.RateLimitError{RateLimit: &discordgo.RateLimit{
			TooManyRequests: &discordgo.TooManyRequests{}}}, wantClass: classTransient},
		{name: "rest 503", err: restErr(http.StatusServiceUnavailable), wantClass: classTransient, wantStatus: 503},
		{name: "rest 401", err: restErr(http.StatusUnauthorized), wantClass: classFatal, wantStatus: 401},
		{name: "rest 403", err: restErr(http.StatusForbidden), wantClass: classFatal, wantStatus: 403},
		{name: "rest 404", err: restErr(http.StatusNotFound), wantClass: classFatal, wantStatus: 404},
		{name: "rest 400", err: restErr(http.StatusBadRequest), wantClass: classPermanent, wantStatus: 400},
		{name: "transport error", err: errors.New("dial tcp: connection refused"), wantClass: classTransient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotClass, gotStatus := classify(tc.err)
			if gotClass != tc.wantClass || gotStatus != tc.wantStatus {
				t.Errorf("classify() = %d, %d; want %d, %d", gotClass, gotStatus, tc.wantClass, tc.wantStatus)
			}
		})
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

			err := poster.Post(context.Background(), testMessage())

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

	err := poster.Post(context.Background(), testMessage())
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

	err := poster.Post(ctx, testMessage())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Post() = %v, want context.Canceled", err)
	}
	if got := fake.count("/messages"); got > 1 {
		t.Errorf("fake served %d posts, want at most 1", got)
	}
}

// testStrip is an embed card with a PNG attached, as the strip style posts it.
func testStrip() Message {
	embed := testEmbed()
	embed.Image = &discordgo.MessageEmbedImage{URL: "attachment://kill.png"}
	return Message{
		Embed: embed,
		Files: []File{{Name: "kill.png", ContentType: "image/png", Data: []byte("\x89PNG\r\n\x1a\nfake image")}},
	}
}

// testComponents is a components card.
func testComponents() Message {
	return Message{Components: []Component{
		Container(0x2ECC71, TextDisplay("### Stratios — Pilot A"), Separator(), TextDisplay("-# Kill")),
	}}
}

func TestPostUploadsFiles(t *testing.T) {
	fake, url := newFakeDiscord(t)
	poster, _ := newPoster(t, url)
	msg := testStrip()

	if err := poster.Post(context.Background(), msg); err != nil {
		t.Fatalf("Post() = %v", err)
	}

	received := fake.received()
	if len(received) != 1 {
		t.Fatalf("fake served %d posts, want 1", len(received))
	}
	sent := received[0]
	if !strings.HasPrefix(sent.ContentType, "multipart/form-data") {
		t.Errorf("Content-Type = %q, want a multipart upload", sent.ContentType)
	}
	if len(sent.Embeds) != 1 || sent.Embeds[0].Image == nil || sent.Embeds[0].Image.URL != "attachment://kill.png" {
		t.Errorf("embeds = %+v, want the one embed showing attachment://kill.png", sent.Embeds)
	}
	want := msg.Files[0]
	if len(sent.Files) != 1 {
		t.Fatalf("upload carried %d files, want 1", len(sent.Files))
	}
	if got := sent.Files[0]; got.Name != want.Name || got.ContentType != want.ContentType || string(got.Data) != string(want.Data) {
		t.Errorf("uploaded file = %q (%s, %q), want %q (%s, %q)",
			got.Name, got.ContentType, got.Data, want.Name, want.ContentType, want.Data)
	}
}

// A retried upload sends the whole file again, not a reader drained by the
// first attempt.
func TestPostUploadsFilesAgainOnRetry(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.postStatuses = []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, 0}
	poster, _ := newPoster(t, url)
	msg := testStrip()

	if err := poster.Post(context.Background(), msg); err != nil {
		t.Fatalf("Post() = %v", err)
	}

	received := fake.received()
	if len(received) != 3 {
		t.Fatalf("fake served %d posts, want 3", len(received))
	}
	for i, sent := range received {
		if len(sent.Files) != 1 || string(sent.Files[0].Data) != string(msg.Files[0].Data) {
			t.Errorf("attempt %d carried files %+v, want the whole %q", i+1, sent.Files, msg.Files[0].Name)
		}
	}
	if got := len(fake.posted()); got != 1 {
		t.Errorf("fake accepted %d embeds, want 1", got)
	}
}

func TestPostComponents(t *testing.T) {
	fake, url := newFakeDiscord(t)
	poster, _ := newPoster(t, url)
	msg := testComponents()

	if err := poster.Post(context.Background(), msg); err != nil {
		t.Fatalf("Post() = %v", err)
	}

	received := fake.received()
	if len(received) != 1 {
		t.Fatalf("fake served %d posts, want 1", len(received))
	}
	sent := received[0]
	if sent.Flags != 32768 {
		t.Errorf("flags = %d, want 32768 (IS_COMPONENTS_V2)", sent.Flags)
	}
	if len(sent.Embeds) != 0 {
		t.Errorf("embeds = %+v, want none in a components message", sent.Embeds)
	}
	want, err := json.Marshal(msg.Components)
	if err != nil {
		t.Fatalf("marshalling the components: %v", err)
	}
	if string(sent.Components) != string(want) {
		t.Errorf("components = %s, want %s", sent.Components, want)
	}
	if len(sent.Files) != 0 || !strings.HasPrefix(sent.ContentType, "application/json") {
		t.Errorf("Content-Type = %q with %d files, want a plain JSON body", sent.ContentType, len(sent.Files))
	}
}

// The strip card is a components message with its PNG: one multipart upload
// carrying the flags, the components and the file, sent whole again on retry.
func TestPostComponentsWithFiles(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.postStatuses = []int{http.StatusServiceUnavailable, 0}
	poster, _ := newPoster(t, url)
	msg := testComponents()
	msg.Components[0].Components = append(msg.Components[0].Components, MediaGallery("attachment://kill.png"))
	msg.Files = testStrip().Files

	if err := poster.Post(context.Background(), msg); err != nil {
		t.Fatalf("Post() = %v", err)
	}

	received := fake.received()
	if len(received) != 2 {
		t.Fatalf("fake served %d posts, want 2", len(received))
	}
	want, err := json.Marshal(msg.Components)
	if err != nil {
		t.Fatalf("marshalling the components: %v", err)
	}
	for i, sent := range received {
		if !strings.HasPrefix(sent.ContentType, "multipart/form-data") {
			t.Errorf("attempt %d: Content-Type = %q, want a multipart upload", i+1, sent.ContentType)
		}
		if sent.Flags != 32768 || len(sent.Embeds) != 0 {
			t.Errorf("attempt %d: flags = %d with %d embeds, want 32768 (IS_COMPONENTS_V2) and none", i+1, sent.Flags, len(sent.Embeds))
		}
		if string(sent.Components) != string(want) {
			t.Errorf("attempt %d: components = %s, want %s", i+1, sent.Components, want)
		}
		if len(sent.Files) != 1 || sent.Files[0].Name != "kill.png" || string(sent.Files[0].Data) != string(msg.Files[0].Data) {
			t.Errorf("attempt %d: files = %+v, want the whole kill.png", i+1, sent.Files)
		}
	}
}

// A components post is rate limited and retried like an embed.
func TestPostComponentsRetries(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.postStatuses = []int{http.StatusTooManyRequests, http.StatusServiceUnavailable, 0}
	poster, hooks := newPoster(t, url)

	if err := poster.Post(context.Background(), testComponents()); err != nil {
		t.Fatalf("Post() = %v", err)
	}
	if got := fake.count("/messages"); got != 3 {
		t.Errorf("fake served %d posts, want 3", got)
	}
	if want := []time.Duration{defaultBackoff}; !slices.Equal(hooks.clock.slept, want) {
		t.Errorf("Post slept %v, want %v: discordgo waits out the 429, the poster the 503", hooks.clock.slept, want)
	}
}

func TestPostComponentsForbiddenIsFatal(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.postStatuses = []int{http.StatusForbidden}
	poster, _ := newPoster(t, url)

	err := poster.Post(context.Background(), testComponents())

	var fatal *FatalError
	if !errors.As(err, &fatal) || fatal.Status != http.StatusForbidden {
		t.Fatalf("Post() = %v, want a *FatalError for 403", err)
	}
	if got := fake.count("/messages"); got != 1 {
		t.Errorf("fake served %d posts, want 1: a fatal error must not be retried", got)
	}
}

// The 403/404 hint names Attach Files only when the cards upload files.
func TestPostFatalHintNamesAttachFiles(t *testing.T) {
	for _, tc := range []struct {
		name        string
		attachFiles bool
	}{
		{name: "embeds only", attachFiles: false},
		{name: "with files", attachFiles: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, url := newFakeDiscord(t)
			fake.postStatuses = []int{http.StatusForbidden}
			poster, _ := newPoster(t, url, WithAttachFiles(tc.attachFiles))

			err := poster.Post(context.Background(), testMessage())

			var fatal *FatalError
			if !errors.As(err, &fatal) {
				t.Fatalf("Post() = %v, want a *FatalError", err)
			}
			if !strings.Contains(fatal.Hint, "View Channel, Send Messages, Embed Links") {
				t.Errorf("hint = %q, want it to name the embed permissions", fatal.Hint)
			}
			if got := strings.Contains(fatal.Hint, "Attach Files"); got != tc.attachFiles {
				t.Errorf("hint = %q, names Attach Files = %t, want %t", fatal.Hint, got, tc.attachFiles)
			}
		})
	}
}

func TestPostRejectsMalformedMessages(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  Message
	}{
		{name: "empty", msg: Message{}},
		{name: "embed and components", msg: Message{Embed: testEmbed(), Components: testComponents().Components}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, url := newFakeDiscord(t)
			poster, _ := newPoster(t, url)

			err := poster.Post(context.Background(), tc.msg)
			if err == nil {
				t.Fatal("Post() = nil, want an error")
			}
			var fatal *FatalError
			if errors.As(err, &fatal) {
				t.Errorf("Post() = %v, want a plain error: a malformed card is not the operator's fault", err)
			}
			if got := fake.count("/messages"); got != 0 {
				t.Errorf("fake served %d posts, want 0", got)
			}
		})
	}
}
