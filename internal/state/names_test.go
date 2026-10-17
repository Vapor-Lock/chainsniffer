package state

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testNames opens a Names in dir, failing the test when that does not work.
func testNames(t *testing.T, dir string, opts ...Option) *Names {
	t.Helper()
	n, err := OpenNames(dir, append([]Option{WithLogger(slog.New(slog.DiscardHandler))}, opts...)...)
	if err != nil {
		t.Fatalf("OpenNames(%q) = %v", dir, err)
	}
	return n
}

func wantName(t *testing.T, n *Names, kind string, id int64, name, ticker string) {
	t.Helper()
	gotName, gotTicker, ok := n.Get(kind, id)
	if !ok || gotName != name || gotTicker != ticker {
		t.Errorf("Get(%q, %d) = %q, %q, %v; want %q, %q, true", kind, id, gotName, gotTicker, ok, name, ticker)
	}
}

func TestNamesMissingFileStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	n := testNames(t, dir)

	if _, _, ok := n.Get(KindCorporation, 98735003); ok {
		t.Error("Get on an empty cache reported an entry")
	}
	if _, err := os.Stat(n.Path()); !os.IsNotExist(err) {
		t.Errorf("OpenNames created %s (stat error %v)", n.Path(), err)
	}
}

func TestNamesRoundTripAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	n := testNames(t, dir)
	n.Put(KindCorporation, 98735003, "Vapor Lock", "VAPOR")
	n.Put(KindAlliance, 99014747, "Some Alliance", "SA")

	reopened := testNames(t, dir)
	wantName(t, reopened, KindCorporation, 98735003, "Vapor Lock", "VAPOR")
	wantName(t, reopened, KindAlliance, 99014747, "Some Alliance", "SA")
	if _, _, ok := reopened.Get(KindAlliance, 98735003); ok {
		t.Error("a corporation ID was found among the alliances")
	}
}

func TestNamesFileShape(t *testing.T) {
	dir := t.TempDir()
	n := testNames(t, dir)
	n.Put(KindCorporation, 98735003, "Vapor Lock", "VAPOR")

	data, err := os.ReadFile(filepath.Join(dir, NamesFileName))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("names.json is not the expected shape: %v\n%s", err, data)
	}
	if e := got["corporations"]["98735003"]; e["name"] != "Vapor Lock" || e["ticker"] != "VAPOR" {
		t.Errorf("corporations[98735003] = %v", e)
	}
	if a, ok := got["alliances"]; !ok || len(a) != 0 {
		t.Errorf("alliances = %v, want an empty object", a)
	}
}

func TestNamesUnchangedPutWritesNothing(t *testing.T) {
	dir := t.TempDir()
	n := testNames(t, dir)
	n.Put(KindCorporation, 98735003, "Vapor Lock", "VAPOR")

	path := filepath.Join(dir, NamesFileName)
	// Replace the file behind the cache's back: a rewrite would restore it.
	if err := os.WriteFile(path, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	n.Put(KindCorporation, 98735003, "Vapor Lock", "VAPOR")
	if data, _ := os.ReadFile(path); string(data) != "sentinel" {
		t.Errorf("unchanged Put rewrote names.json: %s", data)
	}

	n.Put(KindCorporation, 98735003, "Vapor Lock", "VAPR")
	if data, _ := os.ReadFile(path); string(data) == "sentinel" {
		t.Error("changed Put did not rewrite names.json")
	}
	wantName(t, testNames(t, dir), KindCorporation, 98735003, "Vapor Lock", "VAPR")
}

func TestNamesCorruptFileIsMovedAside(t *testing.T) {
	for name, content := range map[string]string{
		"invalid JSON": "{not json",
		"invalid ID":   `{"corporations":{"abc":{"name":"x","ticker":"y"}},"alliances":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, NamesFileName)
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}

			var logs bytes.Buffer
			n, err := OpenNames(dir, WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
			if err != nil {
				t.Fatalf("OpenNames = %v", err)
			}
			if _, _, ok := n.Get(KindCorporation, 98735003); ok {
				t.Error("cache is not empty after a corrupt file")
			}
			moved, err := os.ReadFile(filepath.Join(dir, NamesFileName+".corrupt"))
			if err != nil || string(moved) != content {
				t.Errorf("names.json.corrupt = %q, %v; want the original contents", moved, err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("names.json still exists (stat error %v)", err)
			}
			if !strings.Contains(logs.String(), "level=WARN") {
				t.Errorf("no warning logged: %q", logs.String())
			}

			n.Put(KindAlliance, 99014747, "Some Alliance", "SA")
			wantName(t, testNames(t, dir), KindAlliance, 99014747, "Some Alliance", "SA")
		})
	}
}

func TestNamesFailedWriteKeepsEntryInMemory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := filepath.Join(t.TempDir(), "names")
	var logs bytes.Buffer
	n, err := OpenNames(dir, WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	n.Put(KindCorporation, 98735003, "Vapor Lock", "VAPOR")
	wantName(t, n, KindCorporation, 98735003, "Vapor Lock", "VAPOR")
	if !strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("no warning logged for the failed write: %q", logs.String())
	}
}

func TestNamesUnknownKind(t *testing.T) {
	dir := t.TempDir()
	n := testNames(t, dir)
	n.Put("character", 1, "Someone", "")
	if _, _, ok := n.Get("character", 1); ok {
		t.Error("Get returned an entry of an unknown kind")
	}
	if _, err := os.Stat(n.Path()); !os.IsNotExist(err) {
		t.Errorf("Put of an unknown kind wrote names.json (stat error %v)", err)
	}
}

func TestNamesLeavesStateFileAlone(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)
	s.SetLastEventID("01JABC")
	s.MarkPosted(42)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, FileName)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(statePath, past, past); err != nil {
		t.Fatal(err)
	}

	// A corrupt name cache is moved aside without touching state.json.
	if err := os.WriteFile(filepath.Join(dir, NamesFileName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	n := testNames(t, dir)
	n.Put(KindCorporation, 98735003, "Vapor Lock", "VAPOR")

	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("state.json changed:\nbefore %s\nafter  %s", before, after)
	}
	if info, err := os.Stat(statePath); err != nil || !info.ModTime().Equal(past) {
		t.Errorf("state.json was rewritten (stat %v, %v)", info, err)
	}
	if _, err := os.Stat(filepath.Join(dir, corruptName)); !os.IsNotExist(err) {
		t.Errorf("state.json.corrupt exists (stat error %v)", err)
	}
	if got := testStore(t, dir).LastEventID(); got != "01JABC" {
		t.Errorf("LastEventID() = %q after names activity, want 01JABC", got)
	}
}

func TestNamesConcurrentPuts(t *testing.T) {
	dir := t.TempDir()
	n := testNames(t, dir)

	const workers, perWorker = 8, 20
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for i := range perWorker {
				id := int64(w*perWorker + i + 1)
				n.Put(KindCorporation, id, "Corp", "C")
				n.Put(KindAlliance, id, "Alliance", "A")
				n.Get(KindCorporation, id)
			}
		})
	}
	wg.Wait()

	reopened := testNames(t, dir)
	for id := int64(1); id <= workers*perWorker; id++ {
		wantName(t, reopened, KindCorporation, id, "Corp", "C")
		wantName(t, reopened, KindAlliance, id, "Alliance", "A")
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(matches) != 0 {
		t.Errorf("temporary files left behind: %v", matches)
	}
}
