package sse_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Vapor-Lock/chainsniffer/internal/sse"
)

func TestReaderFraming(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []sse.Event
	}{
		{
			name:  "complete frame",
			input: "id: 01JB\nevent: map_kill\ndata: {\"a\":1}\n\n",
			want:  []sse.Event{{ID: "01JB", Type: "map_kill", Data: `{"a":1}`}},
		},
		{
			name:  "frame without event field uses the default type",
			input: "data: hello\n\n",
			want:  []sse.Event{{Type: sse.DefaultEventType, Data: "hello"}},
		},
		{
			name:  "several data fields are joined with newlines",
			input: "event: ping\ndata: first\ndata: second\ndata: third\n\n",
			want:  []sse.Event{{Type: "ping", Data: "first\nsecond\nthird"}},
		},
		{
			name:  "empty data field keeps its line",
			input: "data: a\ndata\ndata: b\n\n",
			want:  []sse.Event{{Type: sse.DefaultEventType, Data: "a\n\nb"}},
		},
		{
			name:  "a lone data field dispatches an empty event",
			input: "data\n\n",
			want:  []sse.Event{{Type: sse.DefaultEventType, Data: ""}},
		},
		{
			name:  "comment lines are ignored",
			input: ": keepalive\ndata: a\n: trailing comment\n\n",
			want:  []sse.Event{{Type: sse.DefaultEventType, Data: "a"}},
		},
		{
			name:  "only the first space after the colon is stripped",
			input: "data:  padded \n\n",
			want:  []sse.Event{{Type: sse.DefaultEventType, Data: " padded "}},
		},
		{
			name:  "a value needs no space after the colon",
			input: "data:a\n\n",
			want:  []sse.Event{{Type: sse.DefaultEventType, Data: "a"}},
		},
		{
			name:  "a value may contain colons",
			input: "data: 12:30:00\n\n",
			want:  []sse.Event{{Type: sse.DefaultEventType, Data: "12:30:00"}},
		},
		{
			name:  "CRLF ends a line",
			input: "id: 1\r\nevent: ping\r\ndata: a\r\n\r\n",
			want:  []sse.Event{{ID: "1", Type: "ping", Data: "a"}},
		},
		{
			name:  "a lone CR ends a line",
			input: "id: 1\revent: ping\rdata: a\r\r",
			want:  []sse.Event{{ID: "1", Type: "ping", Data: "a"}},
		},
		{
			name:  "the id sticks to later frames",
			input: "id: 01JB\ndata: a\n\ndata: b\n\n",
			want: []sse.Event{
				{ID: "01JB", Type: sse.DefaultEventType, Data: "a"},
				{ID: "01JB", Type: sse.DefaultEventType, Data: "b"},
			},
		},
		{
			name:  "an empty id field clears the id",
			input: "id: 01JB\ndata: a\n\nid\ndata: b\n\n",
			want: []sse.Event{
				{ID: "01JB", Type: sse.DefaultEventType, Data: "a"},
				{Type: sse.DefaultEventType, Data: "b"},
			},
		},
		{
			name:  "an id holding a NUL is ignored",
			input: "id: 01JB\ndata: a\n\nid: bad\x00id\ndata: b\n\n",
			want: []sse.Event{
				{ID: "01JB", Type: sse.DefaultEventType, Data: "a"},
				{ID: "01JB", Type: sse.DefaultEventType, Data: "b"},
			},
		},
		{
			name:  "a blank line without data dispatches nothing",
			input: "\n\nevent: ping\n\ndata: a\n\n",
			want:  []sse.Event{{Type: sse.DefaultEventType, Data: "a"}},
		},
		{
			name:  "retry and unknown fields are ignored",
			input: "retry: 5000\nfoo: bar\ndata: a\n\n",
			want:  []sse.Event{{Type: sse.DefaultEventType, Data: "a"}},
		},
		{
			name:  "a leading byte order mark is ignored",
			input: "\ufeffdata: a\n\n",
			want:  []sse.Event{{Type: sse.DefaultEventType, Data: "a"}},
		},
		{
			name:  "a frame cut short by the end of the stream is discarded",
			input: "data: a\n\nevent: ping\ndata: b\n",
			want:  []sse.Event{{Type: sse.DefaultEventType, Data: "a"}},
		},
		{
			name:  "an empty stream yields nothing",
			input: "",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collect(t, sse.NewReader(strings.NewReader(tt.input), nil))
			if !slices.Equal(got, tt.want) {
				t.Errorf("events = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// A stream that only keepalives still has to look alive to the watchdog in
// internal/wanderer, which is the whole point of the activity callback.
func TestReaderReportsActivityForKeepalivesOnly(t *testing.T) {
	calls := 0
	reader := sse.NewReader(strings.NewReader(": keepalive\n\n: keepalive\n\n"), func() { calls++ })

	if got := collect(t, reader); len(got) != 0 {
		t.Errorf("events = %+v, want none", got)
	}
	if calls == 0 {
		t.Error("activity callback was never called")
	}
}

func TestReaderReportsActivityBeforeAFrameCompletes(t *testing.T) {
	calls := 0
	// An unterminated frame produces no event, so the callback is the only
	// evidence that the connection delivered anything.
	reader := sse.NewReader(strings.NewReader("id: 01JB\nevent: map_kill\ndata: {\"a\":1}\n"), func() { calls++ })

	if got := collect(t, reader); len(got) != 0 {
		t.Errorf("events = %+v, want none", got)
	}
	if calls == 0 {
		t.Error("activity callback was never called")
	}
}

// TestReaderOnWandererStream parses a fixture in the shape Wanderer's
// format_sse_event writes: a "connected" event, a keepalive comment and a
// "map_kill" event whose data is the event envelope.
func TestReaderOnWandererStream(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "wanderer_stream.sse"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	calls := 0
	events := collect(t, sse.NewReader(bytes.NewReader(fixture), func() { calls++ }))
	if calls == 0 {
		t.Error("activity callback was never called")
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (the keepalive produces none): %+v", len(events), events)
	}

	type envelope struct {
		ID        string          `json:"id"`
		Type      string          `json:"type"`
		MapID     string          `json:"map_id"`
		Timestamp string          `json:"timestamp"`
		Payload   json.RawMessage `json:"payload"`
	}

	wantTypes := []string{"connected", "map_kill"}
	for i, event := range events {
		if event.Type != wantTypes[i] {
			t.Errorf("events[%d].Type = %q, want %q", i, event.Type, wantTypes[i])
		}
		var decoded envelope
		if err := json.Unmarshal([]byte(event.Data), &decoded); err != nil {
			t.Fatalf("events[%d].Data is not a JSON envelope: %v", i, err)
		}
		// The envelope repeats the frame's id and event type, so a mismatch
		// means the framing dropped or mixed up a line.
		if decoded.ID != event.ID {
			t.Errorf("events[%d]: envelope id %q, frame id %q", i, decoded.ID, event.ID)
		}
		if decoded.Type != event.Type {
			t.Errorf("events[%d]: envelope type %q, frame type %q", i, decoded.Type, event.Type)
		}
		if decoded.MapID == "" || decoded.Timestamp == "" || len(decoded.Payload) == 0 {
			t.Errorf("events[%d]: incomplete envelope %+v", i, decoded)
		}
	}

	if !strings.Contains(events[1].Data, `"killmail_update"`) {
		t.Errorf("map_kill data lost its payload: %s", events[1].Data)
	}
}

// collect reads events until the stream ends.
func collect(t *testing.T, reader *sse.Reader) []sse.Event {
	t.Helper()

	var events []sse.Event
	for {
		event, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return events
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		events = append(events, event)
	}
}
