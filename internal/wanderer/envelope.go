package wanderer

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Vapor-Lock/chainsniffer/internal/sse"
)

// Envelope is the JSON object a Wanderer event carries in its data field. The
// payload is left raw: internal/killmail owns the kill payload shapes, which
// are an internal map on Wanderer's side rather than a versioned schema.
type Envelope struct {
	// ID is the event's ULID, the value to resume the stream from.
	ID string
	// Type is the event type, "map_kill" for the events Run delivers.
	Type string
	// MapID is the UUID of the map the event belongs to.
	MapID string
	// Timestamp is when Wanderer emitted the event. It is the zero time when
	// the server sent none, or sent one this build cannot parse.
	Timestamp time.Time
	// Payload is the event's payload object, exactly as it arrived.
	Payload json.RawMessage
}

// wireEnvelope is the envelope as it arrives. The timestamp stays a string so
// that an unexpected format costs the event its timestamp and not the event
// itself.
type wireEnvelope struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	MapID     string          `json:"map_id"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// decodeEnvelope decodes one event's data. It fails for data that is not a
// JSON object or that names no event type, which is what the caller reports as
// a malformed event.
func decodeEnvelope(event sse.Event) (Envelope, error) {
	var wire wireEnvelope
	if err := json.Unmarshal([]byte(event.Data), &wire); err != nil {
		return Envelope{}, fmt.Errorf("decoding the event envelope: %w", err)
	}
	if wire.Type == "" {
		return Envelope{}, errors.New("the event envelope names no type")
	}

	envelope := Envelope{
		ID:      event.ID,
		Type:    wire.Type,
		MapID:   wire.MapID,
		Payload: wire.Payload,
	}
	// The framed id is what Wanderer's backfill relay is keyed by, so it wins
	// over the copy inside the envelope.
	if envelope.ID == "" {
		envelope.ID = wire.ID
	}
	if timestamp, err := time.Parse(time.RFC3339, wire.Timestamp); err == nil {
		envelope.Timestamp = timestamp
	}
	return envelope, nil
}
