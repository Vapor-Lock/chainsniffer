// Package discord posts killmails to a Discord channel as a bot: it renders
// them as embeds and sends them, absorbing Discord's rate limits and transient
// failures while treating a bad token or an unreachable channel as fatal.
//
// Render turns a classified killmail into an embed, and Poster owns the
// Discord side of the relay: Startup proves the bot can post before the stream
// is subscribed, and Post delivers one embed, retrying within a budget so a
// Discord outage costs kills rather than stalling the stream.
package discord
