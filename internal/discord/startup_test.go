package discord

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestStartupChecksTokenChannelAndOpensGateway(t *testing.T) {
	fake, url := newFakeDiscord(t)
	poster, hooks := newPoster(t, url)

	if err := poster.Startup(context.Background()); err != nil {
		t.Fatalf("Startup() = %v", err)
	}
	if hooks.opens != 1 {
		t.Errorf("gateway opened %d times, want 1", hooks.opens)
	}
	for _, want := range []string{"GET " + apiPrefix + "users/@me", "GET " + apiPrefix + "channels/" + testChannelID} {
		if !containsRequest(fake.seen(), want) {
			t.Errorf("fake never served %q, saw %v", want, fake.seen())
		}
	}
	if got := fake.count("/messages"); got != 0 {
		t.Errorf("fake served %d posts during the startup check, want 0", got)
	}

	if err := poster.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if hooks.closes != 1 {
		t.Errorf("gateway closed %d times, want 1", hooks.closes)
	}
	// A second Close is the shutdown path running twice; it must not close a
	// session that is already gone.
	if err := poster.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	if hooks.closes != 1 {
		t.Errorf("gateway closed %d times after a second Close, want 1", hooks.closes)
	}
}

func TestStartupFatalRejections(t *testing.T) {
	for _, tc := range []struct {
		name     string
		setup    func(*fakeDiscord)
		wantOp   string
		wantCode int
	}{
		{
			name:     "bad token",
			setup:    func(f *fakeDiscord) { f.tokenStatus = http.StatusUnauthorized },
			wantOp:   "token check",
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "unknown channel",
			setup:    func(f *fakeDiscord) { f.channelStatus = http.StatusNotFound },
			wantOp:   "channel check",
			wantCode: http.StatusNotFound,
		},
		{
			name:     "channel not visible",
			setup:    func(f *fakeDiscord) { f.channelStatus = http.StatusForbidden },
			wantOp:   "channel check",
			wantCode: http.StatusForbidden,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, url := newFakeDiscord(t)
			tc.setup(fake)
			poster, hooks := newPoster(t, url)

			err := poster.Startup(context.Background())

			var fatal *FatalError
			if !errors.As(err, &fatal) {
				t.Fatalf("Startup() = %v, want a *FatalError", err)
			}
			if fatal.Op != tc.wantOp {
				t.Errorf("FatalError.Op = %q, want %q", fatal.Op, tc.wantOp)
			}
			if fatal.Status != tc.wantCode {
				t.Errorf("FatalError.Status = %d, want %d", fatal.Status, tc.wantCode)
			}
			if fatal.Hint == "" {
				t.Error("FatalError.Hint is empty, want it to say what to change")
			}
			if got := fake.count("/messages"); got != 0 {
				t.Errorf("fake served %d posts, want 0: the check must fail before any post", got)
			}
			if hooks.opens != 0 {
				t.Errorf("gateway opened %d times, want 0 after a failed check", hooks.opens)
			}
			if err := poster.Close(); err != nil {
				t.Errorf("Close() = %v, want nil when no session was opened", err)
			}
			if hooks.closes != 0 {
				t.Errorf("gateway closed %d times, want 0", hooks.closes)
			}
		})
	}
}

func TestStartupRejectsMissingChannelPermissions(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.permissions = discordgo.PermissionViewChannel | discordgo.PermissionSendMessages
	poster, hooks := newPoster(t, url)

	err := poster.Startup(context.Background())

	var fatal *FatalError
	if !errors.As(err, &fatal) {
		t.Fatalf("Startup() = %v, want a *FatalError", err)
	}
	if !strings.Contains(err.Error(), "Embed Links") {
		t.Errorf("Startup() = %q, want it to name the missing Embed Links permission", err)
	}
	if hooks.opens != 0 {
		t.Errorf("gateway opened %d times, want 0", hooks.opens)
	}
}

// Permissions a bot cannot read are not permissions it lacks: the channel
// fetch already proved the channel is there, and a real refusal shows up as a
// fatal 403 on the first post.
func TestStartupContinuesWhenPermissionsAreUnresolvable(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.guildStatus = http.StatusForbidden
	poster, hooks := newPoster(t, url)

	if err := poster.Startup(context.Background()); err != nil {
		t.Fatalf("Startup() = %v, want nil", err)
	}
	if hooks.opens != 1 {
		t.Errorf("gateway opened %d times, want 1", hooks.opens)
	}
}

func TestStartupSkipsPermissionsWithoutAGuild(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.channelGuildID = ""
	poster, hooks := newPoster(t, url)

	if err := poster.Startup(context.Background()); err != nil {
		t.Fatalf("Startup() = %v", err)
	}
	if got := fake.count("guilds/" + testGuildID); got != 0 {
		t.Errorf("fake served %d guild fetches, want 0 for a channel with no guild", got)
	}
	if hooks.opens != 1 {
		t.Errorf("gateway opened %d times, want 1", hooks.opens)
	}
}

func TestStartupReportsAGatewayThatWillNotOpen(t *testing.T) {
	_, url := newFakeDiscord(t)
	poster, hooks := newPoster(t, url)
	hooks.openErr = errors.New("websocket: bad handshake")

	err := poster.Startup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "gateway") {
		t.Fatalf("Startup() = %v, want an error mentioning the gateway", err)
	}
	if err := poster.Close(); err != nil {
		t.Errorf("Close() = %v, want nil after a gateway that never opened", err)
	}
	if hooks.closes != 0 {
		t.Errorf("gateway closed %d times, want 0", hooks.closes)
	}
}

// A non-fatal Discord failure still stops the run, but not as a *FatalError:
// nothing in the relay's configuration is wrong.
func TestStartupTransientFailureIsNotFatal(t *testing.T) {
	fake, url := newFakeDiscord(t)
	fake.tokenStatus = http.StatusServiceUnavailable
	poster, _ := newPoster(t, url)

	err := poster.Startup(context.Background())
	if err == nil {
		t.Fatal("Startup() = nil, want an error")
	}
	var fatal *FatalError
	if errors.As(err, &fatal) {
		t.Errorf("Startup() = %v, want a plain error", err)
	}
}

func containsRequest(requests []string, want string) bool {
	for _, req := range requests {
		if req == want {
			return true
		}
	}
	return false
}
