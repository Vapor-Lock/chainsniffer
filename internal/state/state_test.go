package state

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// testStore opens a Store in dir, failing the test when that does not work.
func testStore(t *testing.T, dir string, opts ...Option) *Store {
	t.Helper()
	s, err := Open(dir, append([]Option{WithLogger(slog.New(slog.DiscardHandler))}, opts...)...)
	if err != nil {
		t.Fatalf("Open(%q) = %v", dir, err)
	}
	return s
}

func TestOpenEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)

	if got := s.LastEventID(); got != "" {
		t.Errorf("LastEventID() = %q, want empty", got)
	}
	if s.Posted(123) {
		t.Error("Posted(123) = true on a fresh state")
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Errorf("Stat(%q) = %v, want not-exist: Open must not write a state file", s.Path(), err)
	}
}

func TestOpenCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "chainsniffer")
	s := testStore(t, dir)

	if err := s.Save(); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Errorf("Stat(state file) = %v, want nil", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)

	s.SetLastEventID("01JB")
	s.MarkPosted(123)
	s.MarkPosted(456)
	if err := s.Save(); err != nil {
		t.Fatalf("Save() = %v", err)
	}

	// Only the state file is left behind: the temporary file is renamed, not
	// abandoned.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q) = %v", dir, err)
	}
	if len(entries) != 1 || entries[0].Name() != FileName {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("state directory holds %v, want only %s", names, FileName)
	}

	reopened := testStore(t, dir)
	if got := reopened.LastEventID(); got != "01JB" {
		t.Errorf("LastEventID() = %q, want %q", got, "01JB")
	}
	for _, id := range []int64{123, 456} {
		if !reopened.Posted(id) {
			t.Errorf("Posted(%d) = false, want true", id)
		}
	}
	if reopened.Posted(789) {
		t.Error("Posted(789) = true, want false")
	}
}

func TestSaveOverwritesPreviousState(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)

	s.SetLastEventID("01JB")
	s.MarkPosted(1)
	if err := s.Save(); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	s.SetLastEventID("01JC")
	s.MarkPosted(2)
	if err := s.Save(); err != nil {
		t.Fatalf("second Save() = %v", err)
	}

	reopened := testStore(t, dir)
	if got := reopened.LastEventID(); got != "01JC" {
		t.Errorf("LastEventID() = %q, want %q", got, "01JC")
	}
	if got, want := reopened.PostedIDs(), []int64{1, 2}; !slices.Equal(got, want) {
		t.Errorf("PostedIDs() = %v, want %v", got, want)
	}
}

func TestMarkPostedIsIdempotent(t *testing.T) {
	s := testStore(t, t.TempDir(), WithLimit(3))

	s.MarkPosted(1)
	s.MarkPosted(2)
	s.MarkPosted(1)

	if got, want := s.PostedIDs(), []int64{1, 2}; !slices.Equal(got, want) {
		t.Errorf("PostedIDs() = %v, want %v: re-marking must not duplicate or reorder", got, want)
	}
}

func TestPostedIDsAreBounded(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir, WithLimit(3))

	for id := int64(1); id <= 5; id++ {
		s.MarkPosted(id)
	}

	if got, want := s.PostedIDs(), []int64{3, 4, 5}; !slices.Equal(got, want) {
		t.Errorf("PostedIDs() = %v, want %v: the oldest IDs are evicted first", got, want)
	}
	for _, id := range []int64{1, 2} {
		if s.Posted(id) {
			t.Errorf("Posted(%d) = true, want false: evicted IDs are forgotten", id)
		}
	}

	// The bound survives a save/load round trip, with the order intact.
	if err := s.Save(); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	if got, want := readPosted(t, filepath.Join(dir, FileName)), []int64{3, 4, 5}; !slices.Equal(got, want) {
		t.Errorf("posted in state file = %v, want %v", got, want)
	}
	if got, want := testStore(t, dir, WithLimit(3)).PostedIDs(), []int64{3, 4, 5}; !slices.Equal(got, want) {
		t.Errorf("PostedIDs() after reopen = %v, want %v", got, want)
	}
}

func TestDefaultLimitRetainsLast1000(t *testing.T) {
	s := testStore(t, t.TempDir())

	for id := int64(1); id <= DefaultLimit+200; id++ {
		s.MarkPosted(id)
	}

	ids := s.PostedIDs()
	if len(ids) != DefaultLimit {
		t.Fatalf("len(PostedIDs()) = %d, want %d", len(ids), DefaultLimit)
	}
	if ids[0] != 201 || ids[len(ids)-1] != DefaultLimit+200 {
		t.Errorf("PostedIDs() spans %d..%d, want %d..%d", ids[0], ids[len(ids)-1], 201, DefaultLimit+200)
	}
	if s.Posted(200) {
		t.Error("Posted(200) = true, want false")
	}
	if !s.Posted(201) {
		t.Error("Posted(201) = false, want true")
	}
}

func TestLoadTruncatesOverlongPostedList(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, `{"last_event_id":"01JB","posted":[1,2,3,4,5]}`)

	s := testStore(t, dir, WithLimit(2))

	if got, want := s.PostedIDs(), []int64{4, 5}; !slices.Equal(got, want) {
		t.Errorf("PostedIDs() = %v, want %v: a shrunken bound keeps the newest IDs", got, want)
	}
}

func TestCorruptStateFileIsMovedAside(t *testing.T) {
	dir := t.TempDir()
	const broken = `{"last_event_id":"01JB","posted":[1,2`
	writeState(t, dir, broken)

	var logs bytes.Buffer
	s, err := Open(dir, WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))))
	if err != nil {
		t.Fatalf("Open(%q) = %v, want nil: a corrupt state file must not be fatal", dir, err)
	}

	if got := s.LastEventID(); got != "" {
		t.Errorf("LastEventID() = %q, want empty", got)
	}
	if got := s.PostedIDs(); len(got) != 0 {
		t.Errorf("PostedIDs() = %v, want empty", got)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Errorf("Stat(state file) = %v, want not-exist: the corrupt file is moved, not copied", err)
	}
	moved, err := os.ReadFile(filepath.Join(dir, corruptName))
	if err != nil {
		t.Fatalf("ReadFile(%s) = %v", corruptName, err)
	}
	if string(moved) != broken {
		t.Errorf("%s = %q, want %q", corruptName, moved, broken)
	}
	if level := logs.String(); !strings.Contains(level, "level=WARN") {
		t.Errorf("logs = %q, want a warning", logs.String())
	}

	// The relay carries on from empty state and overwrites nothing else.
	s.SetLastEventID("01JC")
	if err := s.Save(); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	if got := testStore(t, dir).LastEventID(); got != "01JC" {
		t.Errorf("LastEventID() after reopen = %q, want %q", got, "01JC")
	}
}

func TestCorruptStateFileIsReplacedOnSecondCorruption(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, `first`)
	testStore(t, dir)
	writeState(t, dir, `second`)
	testStore(t, dir)

	moved, err := os.ReadFile(filepath.Join(dir, corruptName))
	if err != nil {
		t.Fatalf("ReadFile(%s) = %v", corruptName, err)
	}
	if string(moved) != "second" {
		t.Errorf("%s = %q, want %q", corruptName, moved, "second")
	}
}

func TestOpenUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	dir := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatalf("Mkdir(%q) = %v", dir, err)
	}
	// Let the test's temporary directory be cleaned up afterwards.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err := Open(dir)
	if err == nil {
		t.Fatal("Open(read-only dir) = nil, want an error")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("Open(read-only dir) = %q, want the error to name %q", err, dir)
	}
}

func TestOpenEmptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Error(`Open("") = nil, want an error`)
	}
}

// writeState puts body in dir as the state file.
func writeState(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) = %v", FileName, err)
	}
}

// readPosted decodes the posted IDs straight from the state file, so the tests
// also cover its on-disk shape.
func readPosted(t *testing.T, path string) []int64 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) = %v", path, err)
	}
	var c contents
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("Unmarshal(%q) = %v", data, err)
	}
	return c.Posted
}

func TestConcurrentUse(t *testing.T) {
	s := testStore(t, t.TempDir())

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 50 {
				id := int64(i*50 + j)
				s.MarkPosted(id)
				s.Posted(id)
				s.SetLastEventID("01JB")
				if err := s.Save(); err != nil {
					t.Errorf("Save() = %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := len(s.PostedIDs()); got != 400 {
		t.Errorf("len(PostedIDs()) = %d, want 400", got)
	}
}
