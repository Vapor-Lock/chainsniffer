// Package state persists the relay's progress across restarts: the ID of the
// last SSE event it processed and the IDs of the killmails it has already
// posted, so a resumed run neither loses nor repeats kills.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

const (
	// FileName is the state file's name inside the state directory.
	FileName = "state.json"

	// corruptName is where an unparsable state file is moved aside.
	corruptName = FileName + ".corrupt"

	// DefaultLimit is how many posted killmail IDs are retained. The relay
	// only needs enough history to recognise the kills a replayed event
	// carries, and Wanderer's backfill relay keeps at most 1000 events.
	DefaultLimit = 1000

	dirPerm = 0o755
)

// contents is the on-disk shape of the state file.
type contents struct {
	LastEventID string  `json:"last_event_id"`
	Posted      []int64 `json:"posted"`
}

// Store is the relay's state, backed by a JSON file. Its zero value is not
// usable; call Open. A Store is safe for concurrent use, so the shutdown path
// can save while the read loop updates it.
type Store struct {
	dir    string
	path   string
	logger *slog.Logger

	mu          sync.Mutex
	lastEventID string
	posted      ring
	seen        map[int64]struct{}
}

type options struct {
	limit  int
	logger *slog.Logger
}

// An Option configures a Store at Open time.
type Option func(*options)

// WithLimit sets how many posted killmail IDs are retained. Values below one
// select DefaultLimit.
func WithLimit(n int) Option {
	return func(o *options) { o.limit = n }
}

// WithLogger sets the logger used to report an unreadable state file.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// Open prepares dir as the state directory and loads any state already in it.
// The directory is created when missing, and Open fails when it cannot be
// written to, so an unusable state directory stops the relay at startup rather
// than after the first post. A state file that cannot be parsed is moved aside
// as state.json.corrupt and reported through the logger, and Open returns an
// empty state.
func Open(dir string, opts ...Option) (*Store, error) {
	if dir == "" {
		return nil, errors.New("state: directory is required")
	}

	o := options{limit: DefaultLimit, logger: slog.Default()}
	for _, opt := range opts {
		opt(&o)
	}
	if o.limit < 1 {
		o.limit = DefaultLimit
	}

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("state: creating directory %s: %w", dir, err)
	}
	if err := checkWritable(dir); err != nil {
		return nil, err
	}

	s := &Store{
		dir:    dir,
		path:   filepath.Join(dir, FileName),
		logger: o.logger,
		posted: newRing(o.limit),
		seen:   make(map[int64]struct{}, o.limit),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Path is the state file's path.
func (s *Store) Path() string { return s.path }

// LastEventID is the ID of the last fully processed SSE event, or "" when the
// relay has not processed one yet.
func (s *Store) LastEventID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastEventID
}

// SetLastEventID records id as the cursor to resume from. Callers advance the
// cursor only once every killmail in the event has been handled.
func (s *Store) SetLastEventID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastEventID = id
}

// Posted reports whether killmail id is among the retained posted IDs.
func (s *Store) Posted(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.seen[id]
	return ok
}

// MarkPosted records killmail id as posted, dropping the oldest retained ID
// once the bound is reached. Marking an ID that is already recorded does
// nothing, so it keeps its place in the eviction order.
func (s *Store) MarkPosted(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markPosted(id)
}

// PostedIDs returns the retained posted killmail IDs, oldest first.
func (s *Store) PostedIDs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.posted.ids()
}

// Save writes the state to disk atomically: it writes a temporary file in the
// state directory, flushes it, and renames it over the state file, so an
// interrupted save leaves either the previous or the new state behind, never a
// truncated file.
func (s *Store) Save() error {
	s.mu.Lock()
	data, err := json.Marshal(contents{LastEventID: s.lastEventID, Posted: s.posted.ids()})
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("state: encoding state: %w", err)
	}
	return writeFileAtomic(s.dir, s.path, append(data, '\n'))
}

// markPosted records id. The caller holds s.mu.
func (s *Store) markPosted(id int64) {
	if _, ok := s.seen[id]; ok {
		return
	}
	if evicted, ok := s.posted.push(id); ok {
		delete(s.seen, evicted)
	}
	s.seen[id] = struct{}{}
}

// load reads the state file into s, moving it aside when it cannot be parsed.
func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("state: reading %s: %w", s.path, err)
	}

	var c contents
	if err := json.Unmarshal(data, &c); err != nil {
		corrupt := filepath.Join(s.dir, corruptName)
		if renameErr := os.Rename(s.path, corrupt); renameErr != nil {
			return fmt.Errorf("state: moving unreadable %s aside: %w", s.path, renameErr)
		}
		s.logger.Warn("unreadable state file moved aside, starting with empty state",
			"path", s.path, "moved_to", corrupt, "error", err)
		return nil
	}

	s.lastEventID = c.LastEventID
	// Entries beyond the bound are dropped oldest first, which is also what a
	// shrunken limit should do to a state file written with a larger one.
	for _, id := range c.Posted {
		s.markPosted(id)
	}
	return nil
}

// checkWritable fails when dir does not accept new files, which is how an
// unwritable state directory is caught before the relay connects anywhere.
func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".writable-*")
	if err != nil {
		return fmt.Errorf("state: directory %s is not writable: %w", dir, err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		return fmt.Errorf("state: closing %s: %w", name, err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("state: removing %s: %w", name, err)
	}
	return nil
}

// writeFileAtomic writes data to path through a temporary file in dir, which
// has to be the directory holding path so that the rename stays within one
// filesystem.
func writeFileAtomic(dir, path string, data []byte) error {
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("state: creating temporary file in %s: %w", dir, err)
	}
	tmp := f.Name()
	// A no-op once the rename below succeeded; on every failure path it keeps
	// a partial file from piling up in the state directory.
	defer func() { _ = os.Remove(tmp) }()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("state: writing %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("state: flushing %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("state: closing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("state: renaming %s to %s: %w", tmp, path, err)
	}
	// The rename is only durable once the directory entry is on disk too.
	return syncDir(dir)
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("state: opening %s: %w", dir, err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("state: flushing %s: %w", dir, err)
	}
	return nil
}
