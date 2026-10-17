package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

const (
	// NamesFileName is the name cache's file name inside the state directory.
	NamesFileName = "names.json"

	// namesCorruptName is where an unparsable name cache is moved aside.
	namesCorruptName = NamesFileName + ".corrupt"

	// KindCorporation and KindAlliance are the kinds of entity Names stores.
	KindCorporation = "corporation"
	KindAlliance    = "alliance"
)

// entity is one stored name and ticker.
type entity struct {
	Name   string `json:"name"`
	Ticker string `json:"ticker"`
}

// namesContents is the on-disk shape of the name cache. JSON object keys are
// strings, so the IDs are written in decimal.
type namesContents struct {
	Corporations map[string]entity `json:"corporations"`
	Alliances    map[string]entity `json:"alliances"`
}

// Names is a persistent cache of corporation and alliance names and tickers,
// backed by names.json in the state directory. Corporation and alliance IDs,
// names and tickers never change in EVE, so entries never expire. It is kept
// apart from state.json so that a growing cache does not ride along on every
// cursor save and a corrupt cache never costs the cursor.
//
// Names has the method set of universe.NameCache. Its zero value is not
// usable; call OpenNames. It is safe for concurrent use.
type Names struct {
	dir    string
	path   string
	logger *slog.Logger

	mu           sync.Mutex
	corporations map[int64]entity
	alliances    map[int64]entity
}

// OpenNames loads the name cache in dir, creating dir when missing. A missing
// names.json starts an empty cache. One that cannot be parsed is moved aside as
// names.json.corrupt and reported through the logger, and OpenNames returns an
// empty cache. Of the options only WithLogger applies.
func OpenNames(dir string, opts ...Option) (*Names, error) {
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

	n := &Names{
		dir:          dir,
		path:         filepath.Join(dir, NamesFileName),
		logger:       o.logger,
		corporations: make(map[int64]entity),
		alliances:    make(map[int64]entity),
	}
	if err := n.load(); err != nil {
		return nil, err
	}
	return n, nil
}

// Path is the name cache's path.
func (n *Names) Path() string { return n.path }

// Get returns the stored name and ticker of the corporation or alliance id,
// with kind KindCorporation or KindAlliance. ok is false when there is no entry
// or kind is unknown.
func (n *Names) Get(kind string, id int64) (name, ticker string, ok bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	m := n.table(kind)
	if m == nil {
		return "", "", false
	}
	e, ok := m[id]
	return e.Name, e.Ticker, ok
}

// Put stores the name and ticker of the corporation or alliance id and, when
// the entry is new or different, rewrites names.json atomically. A failed write
// is logged and the entry is kept in memory: the cache is an optimisation and
// never fails a post. An unknown kind is logged and ignored.
func (n *Names) Put(kind string, id int64, name, ticker string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	m := n.table(kind)
	if m == nil {
		n.logger.Warn("ignoring name of unknown kind", "kind", kind, "id", id)
		return
	}
	e := entity{Name: name, Ticker: ticker}
	if old, ok := m[id]; ok && old == e {
		return
	}
	m[id] = e

	// Writing under the lock serializes the writes, so the file on disk never
	// goes back to an older snapshot.
	if err := n.save(); err != nil {
		n.logger.Warn("saving name cache failed, keeping the name in memory",
			"path", n.path, "kind", kind, "id", id, "error", err)
	}
}

// table returns the map for kind, or nil when kind is unknown. The caller holds
// n.mu.
func (n *Names) table(kind string) map[int64]entity {
	switch kind {
	case KindCorporation:
		return n.corporations
	case KindAlliance:
		return n.alliances
	default:
		return nil
	}
}

// save writes the cache to names.json. The caller holds n.mu.
func (n *Names) save() error {
	data, err := json.Marshal(namesContents{
		Corporations: encodeIDs(n.corporations),
		Alliances:    encodeIDs(n.alliances),
	})
	if err != nil {
		return fmt.Errorf("state: encoding name cache: %w", err)
	}
	return writeFileAtomic(n.dir, n.path, append(data, '\n'))
}

// load reads names.json into n, moving it aside when it cannot be parsed.
func (n *Names) load() error {
	data, err := os.ReadFile(n.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("state: reading %s: %w", n.path, err)
	}

	corporations, alliances, err := decodeNames(data)
	if err != nil {
		corrupt := filepath.Join(n.dir, namesCorruptName)
		if renameErr := os.Rename(n.path, corrupt); renameErr != nil {
			return fmt.Errorf("state: moving unreadable %s aside: %w", n.path, renameErr)
		}
		n.logger.Warn("unreadable name cache moved aside, starting with an empty cache",
			"path", n.path, "moved_to", corrupt, "error", err)
		return nil
	}

	n.corporations, n.alliances = corporations, alliances
	return nil
}

// decodeNames parses the contents of names.json.
func decodeNames(data []byte) (corporations, alliances map[int64]entity, err error) {
	var c namesContents
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, nil, err
	}
	if corporations, err = decodeIDs(c.Corporations); err != nil {
		return nil, nil, err
	}
	if alliances, err = decodeIDs(c.Alliances); err != nil {
		return nil, nil, err
	}
	return corporations, alliances, nil
}

func encodeIDs(m map[int64]entity) map[string]entity {
	out := make(map[string]entity, len(m))
	for id, e := range m {
		out[strconv.FormatInt(id, 10)] = e
	}
	return out
}

func decodeIDs(m map[string]entity) (map[int64]entity, error) {
	out := make(map[int64]entity, len(m))
	for k, e := range m {
		id, err := strconv.ParseInt(k, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid id %q: %w", k, err)
		}
		out[id] = e
	}
	return out, nil
}
