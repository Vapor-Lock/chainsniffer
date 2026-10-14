// Package discord renders killmails as embeds and posts them to a Discord
// channel.
package discord

// The poster, the embed renderer and the startup check land in later changes.
// The blank import keeps github.com/bwmarrin/discordgo in go.mod, go.sum and
// the vendorHash pinned in flake.nix from the start, so the beads that build
// on this one never have to touch those files.
import _ "github.com/bwmarrin/discordgo"
