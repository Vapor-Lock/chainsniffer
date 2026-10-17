package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testLastKill(t *testing.T, dir string, opts ...Option) *LastKill {
	t.Helper()
	l, err := OpenLastKill(dir, append([]Option{WithLogger(slog.New(slog.DiscardHandler))}, opts...)...)
	if err != nil {
		t.Fatalf("OpenLastKill(%q) = %v", dir, err)
	}
	return l
}

const savedPayload = `{"type":"killmail_update","solar_system_id":31000727,"killmails":[{"killmail_id":42}]}`

func TestLastKillRoundTrip(t *testing.T) {
	dir := t.TempDir()
	testLastKill(t, dir).Save(41, json.RawMessage(`{"type":"killmail_update","killmails":[{"killmail_id":41}]}`))
	testLastKill(t, dir).Save(42, json.RawMessage(savedPayload))

	got, err := testLastKill(t, dir).Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if got.KillmailID != 42 {
		t.Errorf("KillmailID = %d, want 42, the later save", got.KillmailID)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, got.Payload); err != nil || compact.String() != savedPayload {
		t.Errorf("Payload = %s, want %s", got.Payload, savedPayload)
	}
}

func TestLastKillMissing(t *testing.T) {
	_, err := testLastKill(t, t.TempDir()).Load()
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load() = %v, want an error wrapping fs.ErrNotExist", err)
	}
}

func TestLastKillUnreadable(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":   "{",
		"no ID":      `{"payload":{}}`,
		"no payload": `{"killmail_id":42}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, LastKillFileName), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := testLastKill(t, dir).Load(); err == nil || errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Load() = %v, want an error that is not fs.ErrNotExist", err)
			}
		})
	}
}

func TestLastKillLeavesStateFileAlone(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatal(err)
	}
	store.SetLastEventID("01JB")
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}

	testLastKill(t, dir).Save(42, json.RawMessage(savedPayload))

	after, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("state.json changed from %s to %s", before, after)
	}
}

func TestLastKillFailedSaveIsLogged(t *testing.T) {
	dir := t.TempDir()
	var logs bytes.Buffer
	l := testLastKill(t, dir, WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	// A directory where the file should be makes the rename fail.
	if err := os.Mkdir(l.Path(), 0o700); err != nil {
		t.Fatal(err)
	}

	l.Save(42, json.RawMessage(savedPayload))

	if !strings.Contains(logs.String(), "saving the last kill failed") {
		t.Errorf("logs = %q, want a warning about the failed save", logs.String())
	}
}
