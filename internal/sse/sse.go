// Package sse parses Server-Sent Event streams.
//
// The reader implements the parsing rules of the WHATWG event stream format
// (https://html.spec.whatwg.org/multipage/server-sent-events.html): a blank
// line dispatches an event, the "id", "event" and "data" fields are
// recognised, several "data" lines are joined with newlines, lines starting
// with a colon are comments, and CRLF, LF and a lone CR all end a line.
//
// Reconnecting is deliberately left out. Wanderer resumes a stream with a
// last_event_id query parameter rather than with the Last-Event-ID header the
// format prescribes, so internal/wanderer owns the connection loop and this
// package only does framing.
package sse

import (
	"bufio"
	"io"
	"strings"
)

// DefaultEventType is the type reported for a frame that carries no "event"
// field, as the event stream format prescribes.
const DefaultEventType = "message"

// Event is one dispatched event.
type Event struct {
	// ID is the most recent "id" field seen on the stream. The format makes
	// the id sticky: a frame that carries no id of its own reports the
	// previous one.
	ID string
	// Type is the "event" field, or DefaultEventType when the frame omits it.
	Type string
	// Data holds the frame's "data" fields joined with "\n".
	Data string
}

// Reader reads events from a stream. It is not safe for concurrent use.
type Reader struct {
	br *bufio.Reader

	data      []byte // the "data" values of the current frame, each followed by "\n"
	eventType string
	lastID    string
	atStart   bool // nothing has been parsed yet, so a byte order mark may follow
}

// NewReader returns a Reader that parses the event stream in r.
//
// activity, when non-nil, is called whenever bytes arrive from r. That
// includes comment lines such as Wanderer's ": keepalive" and the bytes of a
// frame that is not complete yet, so internal/wanderer can use it to tell a
// quiet stream from a dead one.
func NewReader(r io.Reader, activity func()) *Reader {
	if activity != nil {
		r = &activityReader{r: r, activity: activity}
	}
	return &Reader{br: bufio.NewReader(r), atStart: true}
}

// Next returns the next event on the stream. It blocks until a blank line
// completes a frame, and reports the underlying read error, io.EOF at the end
// of the stream, once no further event can be dispatched.
//
// A frame that the end of the stream cuts short is discarded. The relay only
// advances its cursor once it has processed an event, so Wanderer's backfill
// replays such a frame on the next connection.
func (r *Reader) Next() (Event, error) {
	for {
		line, err := r.readLine()
		if err != nil {
			r.data = r.data[:0]
			r.eventType = ""
			return Event{}, err
		}
		if r.atStart {
			r.atStart = false
			line = strings.TrimPrefix(line, "\ufeff")
		}
		switch {
		case line == "":
			if event, ok := r.dispatch(); ok {
				return event, nil
			}
		case strings.HasPrefix(line, ":"):
			// A comment. Wanderer sends ": keepalive" every ~30s; it only
			// matters as a sign of life, which activityReader already reported.
		default:
			r.field(splitField(line))
		}
	}
}

// dispatch ends the current frame. It reports whether the frame held any
// "data" field: a blank line on its own dispatches nothing.
func (r *Reader) dispatch() (Event, bool) {
	if len(r.data) == 0 {
		r.eventType = ""
		return Event{}, false
	}
	event := Event{
		ID: r.lastID,
		// The last "data" value contributed a trailing newline that is not
		// part of the data.
		Data: strings.TrimSuffix(string(r.data), "\n"),
		Type: r.eventType,
	}
	if event.Type == "" {
		event.Type = DefaultEventType
	}
	r.data = r.data[:0]
	r.eventType = ""
	return event, true
}

// field applies one parsed field to the frame being accumulated.
func (r *Reader) field(name, value string) {
	switch name {
	case "event":
		r.eventType = value
	case "data":
		r.data = append(r.data, value...)
		r.data = append(r.data, '\n')
	case "id":
		// An id holding a NUL is ignored by the format. Any other value,
		// including an empty one, replaces the sticky id.
		if !strings.ContainsRune(value, 0) {
			r.lastID = value
		}
	default:
		// "retry" lands here on purpose, as do unknown fields: internal/wanderer
		// owns its reconnect backoff and does not let the server set it.
	}
}

// splitField splits a field line into its name and value.
func splitField(line string) (name, value string) {
	name, value, _ = strings.Cut(line, ":")
	// A single space after the colon belongs to the framing, not to the value.
	return name, strings.TrimPrefix(value, " ")
}

// readLine returns the next line without its terminator. CRLF, LF and a lone
// CR each end a line, as the event stream format requires. A line that the end
// of the stream cuts short is returned together with the read error.
func (r *Reader) readLine() (string, error) {
	var line []byte
	for {
		b, err := r.br.ReadByte()
		if err != nil {
			return string(line), err
		}
		switch b {
		case '\n':
			return string(line), nil
		case '\r':
			// A CR may be the first half of a CRLF pair, so look at what
			// follows before treating it as a line ending of its own.
			if next, err := r.br.Peek(1); err == nil && next[0] == '\n' {
				_, _ = r.br.ReadByte()
			}
			return string(line), nil
		default:
			line = append(line, b)
		}
	}
}

// activityReader reports every read that delivered bytes, so that a caller can
// use arriving data as a liveness signal even when it never completes a frame.
type activityReader struct {
	r        io.Reader
	activity func()
}

func (a *activityReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if n > 0 {
		a.activity()
	}
	return n, err
}
