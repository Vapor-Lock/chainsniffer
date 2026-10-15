package discord

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// Startup checks that the relay can post before it subscribes to Wanderer, so
// a bad token or channel ID fails the run immediately instead of being
// discovered on the first kill. It
//
//  1. fetches the bot's own user, which Discord refuses with 401 for a bad
//     token;
//  2. fetches the configured channel, which answers 403 or 404 when the bot
//     cannot see it;
//  3. checks the bot's permissions on the channel when they can be resolved;
//  4. opens the gateway session.
//
// A rejection an operator has to act on is returned as a *FatalError. The
// gateway session is opened last: Discord requires a bot to have connected to
// the gateway before it may create messages, and it is what shows the bot as
// online. Call Close to end it.
func (p *Poster) Startup(ctx context.Context) error {
	self, err := p.session.User("@me", discordgo.WithContext(ctx))
	if err != nil {
		return p.startupError("token check", err)
	}

	channel, err := p.session.Channel(p.channelID, discordgo.WithContext(ctx))
	if err != nil {
		return p.startupError("channel check", err)
	}

	if err := p.checkPermissions(ctx, self.ID, channel); err != nil {
		return err
	}

	p.logger.Info("Discord checks passed",
		"bot", self.Username, "channel_id", p.channelID, "channel", channel.Name, "guild_id", channel.GuildID)

	if err := p.openGateway(); err != nil {
		return fmt.Errorf("discord: open gateway: %w", err)
	}
	p.mu.Lock()
	p.gatewayOpen = true
	p.mu.Unlock()
	return nil
}

// Close ends the gateway session Startup opened. It is safe to call when no
// session was opened, and safe to call twice.
func (p *Poster) Close() error {
	p.mu.Lock()
	open := p.gatewayOpen
	p.gatewayOpen = false
	p.mu.Unlock()
	if !open {
		return nil
	}
	if err := p.closeGateway(); err != nil {
		return fmt.Errorf("discord: close gateway: %w", err)
	}
	return nil
}

// checkPermissions fails the startup when the bot demonstrably lacks a
// permission it needs on channel.
//
// Resolving them takes the guild and the bot's member record, which a bot can
// be denied even when it may post. An unresolvable permission set is therefore
// only logged: the channel fetch above already proves the channel exists and
// is visible, and a genuinely missing permission surfaces as a fatal 403 on
// the first post.
func (p *Poster) checkPermissions(ctx context.Context, selfID string, channel *discordgo.Channel) error {
	if channel.GuildID == "" {
		// A DM channel has no permission overwrites to check.
		return nil
	}
	perms, err := p.session.UserChannelPermissions(selfID, p.channelID, discordgo.WithContext(ctx))
	if err != nil {
		p.logger.Debug("cannot resolve channel permissions, continuing",
			"channel_id", p.channelID, "error", err)
		return nil
	}
	if missing := missingPermissions(perms); len(missing) > 0 {
		return &FatalError{
			Op:     "channel check",
			Status: http.StatusForbidden,
			Hint:   hintFor(http.StatusForbidden),
			Err:    fmt.Errorf("missing %s on channel %s", strings.Join(missing, ", "), p.channelID),
		}
	}
	return nil
}

// missingPermissions names the required permissions that perms lacks.
func missingPermissions(perms int64) []string {
	var missing []string
	for _, required := range []struct {
		bit  int64
		name string
	}{
		{discordgo.PermissionViewChannel, "View Channel"},
		{discordgo.PermissionSendMessages, "Send Messages"},
		{discordgo.PermissionEmbedLinks, "Embed Links"},
	} {
		if perms&required.bit == 0 {
			missing = append(missing, required.name)
		}
	}
	return missing
}

// startupError wraps a failed startup call, as a *FatalError when the
// rejection names something an operator has to change.
func (p *Poster) startupError(op string, err error) error {
	if class, status := classify(err); class == classFatal {
		return &FatalError{Op: op, Status: status, Hint: hintFor(status), Err: err}
	}
	return fmt.Errorf("discord: %s: %w", op, err)
}
