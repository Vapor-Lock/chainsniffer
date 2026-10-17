package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// LastKillFileName is the last posted kill's file name inside the state
// directory.
const LastKillFileName = "last_kill.json"

// SavedKill is the last kill the relay posted: the ID of the killmail and the
// `map_kill` payload it arrived in, as Wanderer sent it. Keeping the payload
// rather than a decoded killmail means a later build decodes, classifies and
// renders it afresh, which is what replaying it to a test channel is for.
type SavedKill struct {
	KillmailID int64           `json:"killmail_id"`
	Payload    json.RawMessage `json:"payload"`
}

// LastKill keeps the last posted kill in last_kill.json in the state
// directory. It is kept apart from state.json, which it never touches, so a
// payload riding along cannot slow the cursor's saves or cost the cursor when
// it is unreadable.
//
// Its zero value is not usable; call OpenLastKill. It is safe for concurrent
// use.
type LastKill struct {
	dir    string
	path   string
	logger *slog.Logger
}

// OpenLastKill returns the last-kill file in dir, creating dir when missing.
// Of the options only WithLogger applies.
func OpenLastKill(dir string, opts ...Option) (*LastKill, error) {
	if dir == "" {
		return nil, errors.New("state: directory is required")
	}

	o := options{logger: slog.Default()}
	for _, opt := range opts {
		opt(&o)
	}

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("state: creating directory %s: %w", dir, err)
	}
	return &LastKill{dir: dir, path: filepath.Join(dir, LastKillFileName), logger: o.logger}, nil
}

// Path is the last-kill file's path.
func (l *LastKill) Path() string { return l.path }

// Save replaces the saved kill with killmail id from payload, writing
// last_kill.json atomically. A failed write is logged and otherwise ignored:
// the file only serves replays, and never fails a post.
func (l *LastKill) Save(id int64, payload json.RawMessage) {
	data, err := json.Marshal(SavedKill{KillmailID: id, Payload: payload})
	if err == nil {
		err = writeFileAtomic(l.dir, l.path, append(data, '\n'))
	}
	if err != nil {
		l.logger.Warn("saving the last kill failed", "path", l.path, "killmail_id", id, "error", err)
	}
}

// Load returns the saved kill. Before any kill has been saved it returns an
// error that wraps fs.ErrNotExist.
func (l *LastKill) Load() (SavedKill, error) {
	data, err := os.ReadFile(l.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return SavedKill{}, fmt.Errorf("state: no kill has been posted from %s yet: %w", l.dir, err)
	case err != nil:
		return SavedKill{}, fmt.Errorf("state: reading %s: %w", l.path, err)
	}

	var k SavedKill
	if err := json.Unmarshal(data, &k); err != nil {
		return SavedKill{}, fmt.Errorf("state: %s is not a saved kill: %w", l.path, err)
	}
	if k.KillmailID <= 0 || len(k.Payload) == 0 {
		return SavedKill{}, fmt.Errorf("state: %s has no killmail ID or payload", l.path)
	}
	return k, nil
}
