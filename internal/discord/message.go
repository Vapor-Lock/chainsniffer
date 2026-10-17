package discord

import (
	"bytes"
	"errors"

	"github.com/bwmarrin/discordgo"
)

// flagComponentsV2 is the message flag IS_COMPONENTS_V2. A message carrying it
// is laid out by its components alone: it cannot also carry content or embeds.
const flagComponentsV2 = 1 << 15

// A Message is one card as Post sends it: either an embed, optionally with
// files attached to it, or a Components V2 layout.
type Message struct {
	// Embed is the card of the block, fields and strip styles.
	Embed *discordgo.MessageEmbed
	// Files are uploaded with Embed, which refers to them as
	// "attachment://<Name>". The strip style attaches its PNG here.
	Files []File
	// Components is the card of the components style, sent as a Components V2
	// message. It excludes Embed and Files.
	Components []Component
}

// A File is an attachment uploaded with a message.
type File struct {
	Name        string
	ContentType string
	Data        []byte
}

// validate rejects a Message Post cannot send.
func (m Message) validate() error {
	switch {
	case m.Embed == nil && len(m.Components) == 0:
		return errors.New("message has neither an embed nor components")
	case m.Embed != nil && len(m.Components) > 0:
		return errors.New("a components message cannot also carry an embed")
	case len(m.Components) > 0 && len(m.Files) > 0:
		return errors.New("a components message cannot carry files")
	}
	return nil
}

// send returns the embed message's ChannelMessageSendComplex payload. Each
// call wraps every file in a fresh reader, so a retry uploads it whole again.
func (m Message) send() *discordgo.MessageSend {
	data := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{m.Embed}}
	for _, f := range m.Files {
		data.Files = append(data.Files, &discordgo.File{
			Name:        f.Name,
			ContentType: f.ContentType,
			Reader:      bytes.NewReader(f.Data),
		})
	}
	return data
}

// componentsBody is the JSON body of a Components V2 message.
type componentsBody struct {
	Flags      int         `json:"flags"`
	Components []Component `json:"components"`
}

// Component types of Discord's Components V2 layout.
const (
	ComponentSection     = 9
	ComponentTextDisplay = 10
	ComponentThumbnail   = 11
	ComponentSeparator   = 14
	ComponentContainer   = 17
)

// A Component is one node of a Components V2 layout. discordgo v0.29.0 has no
// types for these, so they are marshalled as is; which fields apply depends on
// Type, and the constructors below set only those.
type Component struct {
	Type int `json:"type"`

	// Components are a container's children, or a section's text displays.
	Components []Component `json:"components,omitempty"`
	// Accessory is a section's thumbnail, drawn to the right of its text.
	Accessory *Component `json:"accessory,omitempty"`
	// Content is a text display's markdown.
	Content string `json:"content,omitempty"`
	// Media is a thumbnail's image.
	Media *UnfurledMedia `json:"media,omitempty"`
	// AccentColor is a container's stripe colour, as 0xRRGGBB.
	AccentColor *int `json:"accent_color,omitempty"`
}

// UnfurledMedia names an image by URL, which Discord fetches itself.
type UnfurledMedia struct {
	URL string `json:"url"`
}

// Container returns a container with an accent colour around children.
func Container(accent int, children ...Component) Component {
	return Component{Type: ComponentContainer, AccentColor: &accent, Components: children}
}

// Section returns a section of up to three text displays with accessory on
// their right. Discord requires a section to have an accessory.
func Section(accessory Component, texts ...Component) Component {
	return Component{Type: ComponentSection, Components: texts, Accessory: &accessory}
}

// TextDisplay returns a block of markdown.
func TextDisplay(markdown string) Component {
	return Component{Type: ComponentTextDisplay, Content: markdown}
}

// Thumbnail returns the image at url, as a section's accessory.
func Thumbnail(url string) Component {
	return Component{Type: ComponentThumbnail, Media: &UnfurledMedia{URL: url}}
}

// Separator returns a divider line with Discord's default spacing.
func Separator() Component {
	return Component{Type: ComponentSeparator}
}
