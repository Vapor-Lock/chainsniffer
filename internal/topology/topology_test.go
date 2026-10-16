package topology_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Vapor-Lock/chainsniffer/internal/topology"
)

// System IDs for the scenarios. The fixtures use J142845 (31002238) and Jita
// (30000142).
const (
	home int64 = 31002238
	a    int64 = 30000142
	b    int64 = 30000144
	c    int64 = 30002187
	d    int64 = 30002659
)

func testLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), &buf
}

// fixture reads a payload shaped like the map Wanderer hands to
// ExternalEvents.broadcast in map_server_connections_impl.ex and
// map_server_systems_impl.ex.
func fixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// withIDs returns the fixture with the given fields overwritten.
func withIDs(t *testing.T, name string, ids map[string]int64) json.RawMessage {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(fixture(t, name), &fields); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	for k, v := range ids {
		fields[k] = v
	}
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func added(t *testing.T, src, dst int64) json.RawMessage {
	return withIDs(t, "connection_added.json",
		map[string]int64{"solar_system_source_id": src, "solar_system_target_id": dst})
}

func removed(t *testing.T, src, dst int64) json.RawMessage {
	return withIDs(t, "connection_removed.json",
		map[string]int64{"solar_system_source_id": src, "solar_system_target_id": dst})
}

func deleted(t *testing.T, id int64) json.RawMessage {
	return withIDs(t, "deleted_system.json", map[string]int64{"solar_system_id": id})
}

// graph returns a graph holding the given connections, installed as a snapshot.
func graph(t *testing.T, conns ...[2]int64) (*topology.Graph, *bytes.Buffer) {
	t.Helper()
	log, buf := testLogger()
	g := topology.New(home, topology.WithLogger(log))
	g.Install(snapshot(conns...))
	return g, buf
}

// snapshot returns a snapshot of the connections, listing every system they
// touch plus home.
func snapshot(conns ...[2]int64) topology.Snapshot {
	s := topology.Snapshot{Systems: []int64{home}, Connections: conns}
	seen := map[int64]bool{home: true}
	for _, c := range conns {
		for _, id := range c {
			if !seen[id] {
				seen[id] = true
				s.Systems = append(s.Systems, id)
			}
		}
	}
	return s
}

func assertLinked(t *testing.T, g *topology.Graph, want map[int64]bool) {
	t.Helper()
	for id, w := range want {
		if got := g.Linked(id); got != w {
			t.Errorf("Linked(%d) = %v, want %v", id, got, w)
		}
	}
}

func TestLinkedSystems(t *testing.T) {
	t.Run("two jumps out from home", func(t *testing.T) {
		g, _ := graph(t, [2]int64{home, a}, [2]int64{a, b})
		assertLinked(t, g, map[int64]bool{home: true, a: true, b: true})
	})
	t.Run("connection recorded in the other direction", func(t *testing.T) {
		g, _ := graph(t, [2]int64{a, home})
		assertLinked(t, g, map[int64]bool{a: true})
	})
	t.Run("island not connected to home", func(t *testing.T) {
		g, _ := graph(t, [2]int64{home, a}, [2]int64{c, d})
		assertLinked(t, g, map[int64]bool{c: false, d: false})
	})
	t.Run("home without connections", func(t *testing.T) {
		g, _ := graph(t, [2]int64{c, d})
		assertLinked(t, g, map[int64]bool{home: true, a: false, c: false, d: false})
	})
	t.Run("new graph links only home", func(t *testing.T) {
		g := topology.New(home)
		assertLinked(t, g, map[int64]bool{home: true, a: false})
	})
	t.Run("system 0 is never linked", func(t *testing.T) {
		g, _ := graph(t, [2]int64{home, a})
		assertLinked(t, g, map[int64]bool{0: false})
	})
}

func TestSnapshotReplacesStaleTopology(t *testing.T) {
	g, _ := graph(t, [2]int64{home, a}, [2]int64{a, b})
	g.Install(snapshot([2]int64{home, a}))
	assertLinked(t, g, map[int64]bool{home: true, a: true, b: false})
}

func TestTopologyEvents(t *testing.T) {
	t.Run("new connection links an island", func(t *testing.T) {
		g, _ := graph(t, [2]int64{home, a}, [2]int64{c, d})
		g.Apply(topology.EventConnectionAdded, added(t, a, c))
		assertLinked(t, g, map[int64]bool{c: true, d: true})
	})
	t.Run("removed connection unlinks everything behind it", func(t *testing.T) {
		g, _ := graph(t, [2]int64{home, a}, [2]int64{a, b}, [2]int64{b, c})
		g.Apply(topology.EventConnectionRemoved, removed(t, a, b))
		assertLinked(t, g, map[int64]bool{home: true, a: true, b: false, c: false})
	})
	t.Run("removal is independent of direction", func(t *testing.T) {
		g, _ := graph(t, [2]int64{home, a}, [2]int64{a, b})
		g.Apply(topology.EventConnectionRemoved, removed(t, b, a))
		assertLinked(t, g, map[int64]bool{a: true, b: false})
	})
	t.Run("deleted system takes its connections with it", func(t *testing.T) {
		g, _ := graph(t, [2]int64{home, a}, [2]int64{a, b})
		g.Apply(topology.EventDeletedSystem, deleted(t, a))
		assertLinked(t, g, map[int64]bool{home: true, a: false, b: false})
		// The connection a–b went with a, so re-adding home–a links only a.
		g.Apply(topology.EventConnectionAdded, added(t, home, a))
		assertLinked(t, g, map[int64]bool{a: true, b: false})
	})
	t.Run("alternative path keeps a system linked", func(t *testing.T) {
		g, _ := graph(t, [2]int64{home, a}, [2]int64{home, b}, [2]int64{a, c}, [2]int64{b, c})
		g.Apply(topology.EventConnectionRemoved, removed(t, a, c))
		assertLinked(t, g, map[int64]bool{c: true})
	})
	t.Run("fixture payloads", func(t *testing.T) {
		g := topology.New(home)
		g.Apply(topology.EventConnectionAdded, fixture(t, "connection_added.json"))
		assertLinked(t, g, map[int64]bool{a: true})
		g.Apply(topology.EventConnectionRemoved, fixture(t, "connection_removed.json"))
		assertLinked(t, g, map[int64]bool{a: false})
		g.Apply(topology.EventConnectionAdded, fixture(t, "connection_added.json"))
		g.Apply(topology.EventDeletedSystem, fixture(t, "deleted_system.json"))
		assertLinked(t, g, map[int64]bool{a: false})
	})
	t.Run("unknown event type is ignored", func(t *testing.T) {
		g, buf := graph(t, [2]int64{home, a})
		buf.Reset()
		g.Apply("add_system", json.RawMessage(`{"solar_system_id": 30000142}`))
		g.Apply("map_kill", json.RawMessage(`not json`))
		if buf.Len() != 0 {
			t.Errorf("unknown types logged:\n%s", buf)
		}
		assertLinked(t, g, map[int64]bool{a: true})
	})
}

func TestMalformedTopologyEvent(t *testing.T) {
	cases := []struct {
		name, eventType, payload string
	}{
		{"missing target", topology.EventConnectionAdded, `{"solar_system_source_id": 30000142}`},
		{"string target", topology.EventConnectionAdded,
			`{"solar_system_source_id": 30000142, "solar_system_target_id": "Jita"}`},
		{"fractional source", topology.EventConnectionRemoved,
			`{"solar_system_source_id": 31002238.5, "solar_system_target_id": 30000142}`},
		{"null system", topology.EventDeletedSystem, `{"solar_system_id": null, "name": null}`},
		{"zero system", topology.EventDeletedSystem, `{"solar_system_id": 0}`},
		{"not an object", topology.EventConnectionRemoved, `[31002238, 30000142]`},
		{"not JSON", topology.EventDeletedSystem, `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, buf := graph(t, [2]int64{home, a})
			buf.Reset()
			g.Apply(tc.eventType, json.RawMessage(tc.payload))
			if !strings.Contains(buf.String(), "level=WARN") {
				t.Errorf("no warning logged:\n%s", buf)
			}
			assertLinked(t, g, map[int64]bool{home: true, a: true})
		})
	}

	t.Run("journal skips it", func(t *testing.T) {
		g, _ := graph(t, [2]int64{home, a})
		g.BeginResync()
		g.Apply(topology.EventDeletedSystem, json.RawMessage(`{"solar_system_id": "x"}`))
		g.Install(snapshot([2]int64{home, a}))
		assertLinked(t, g, map[int64]bool{a: true})
	})
}

func TestNumericStringIDsAccepted(t *testing.T) {
	g := topology.New(home)
	g.Apply(topology.EventConnectionAdded,
		json.RawMessage(`{"solar_system_source_id": "31002238", "solar_system_target_id": 30000142.0}`))
	assertLinked(t, g, map[int64]bool{a: true})
}

func TestEventsDuringSnapshotAreNotLost(t *testing.T) {
	t.Run("connection removed while a snapshot is loading", func(t *testing.T) {
		g, _ := graph(t, [2]int64{home, a}, [2]int64{a, b})
		err := g.Resync(context.Background(), func(context.Context) (topology.Snapshot, error) {
			// Handled before the response arrives, which still has a–b.
			g.Apply(topology.EventConnectionRemoved, removed(t, a, b))
			return snapshot([2]int64{home, a}, [2]int64{a, b}), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		assertLinked(t, g, map[int64]bool{a: true, b: false})
	})
	t.Run("events the snapshot already reflects change nothing", func(t *testing.T) {
		g, _ := graph(t, [2]int64{home, a})
		g.BeginResync()
		g.Apply(topology.EventConnectionAdded, added(t, a, b))
		g.Apply(topology.EventDeletedSystem, deleted(t, c))
		g.Install(snapshot([2]int64{home, a}, [2]int64{a, b}))
		assertLinked(t, g, map[int64]bool{a: true, b: true})
	})
	t.Run("connection added while a snapshot is loading", func(t *testing.T) {
		g, _ := graph(t, [2]int64{home, a})
		g.BeginResync()
		g.Apply(topology.EventConnectionAdded, added(t, a, b))
		g.Install(snapshot([2]int64{home, a}))
		assertLinked(t, g, map[int64]bool{b: true})
	})
	t.Run("journal ends with the snapshot", func(t *testing.T) {
		g, _ := graph(t, [2]int64{home, a})
		g.BeginResync()
		g.Apply(topology.EventConnectionAdded, added(t, a, b))
		g.Install(snapshot([2]int64{home, a}))
		// Without a resync in flight, the next snapshot is the whole truth.
		g.Install(snapshot([2]int64{home, a}))
		assertLinked(t, g, map[int64]bool{b: false})
	})
	t.Run("failed snapshot keeps the graph", func(t *testing.T) {
		g, _ := graph(t, [2]int64{home, a})
		want := errors.New("HTTP 503")
		err := g.Resync(context.Background(), func(context.Context) (topology.Snapshot, error) {
			g.Apply(topology.EventConnectionAdded, added(t, a, b))
			return topology.Snapshot{}, want
		})
		if !errors.Is(err, want) {
			t.Fatalf("Resync error = %v, want %v", err, want)
		}
		assertLinked(t, g, map[int64]bool{a: true, b: true})
		// The aborted resync no longer journals: a later snapshot replaces it all.
		g.Install(snapshot([2]int64{home, a}))
		assertLinked(t, g, map[int64]bool{b: false})
	})
}

func TestHomeNotOnMap(t *testing.T) {
	log, buf := testLogger()
	g := topology.New(home, topology.WithLogger(log))
	g.Install(topology.Snapshot{Systems: []int64{c, d}, Connections: [][2]int64{{c, d}}})
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, fmt.Sprintf("home_system_id=%d", home)) {
		t.Errorf("no warning naming the home system:\n%s", out)
	}
	assertLinked(t, g, map[int64]bool{home: true, c: false, d: false})

	buf.Reset()
	g.Install(snapshot([2]int64{home, a}))
	if strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("warned with home on the map:\n%s", buf)
	}
}

func TestTopologyLogging(t *testing.T) {
	t.Run("snapshot summary", func(t *testing.T) {
		// 22 systems: home with 15 linked systems hanging off it, and an island
		// of 6.
		s := topology.Snapshot{Systems: []int64{home}}
		for i := int64(1); i <= 15; i++ {
			s.Systems = append(s.Systems, i)
			s.Connections = append(s.Connections, [2]int64{home, i})
		}
		for i := int64(100); i < 106; i++ {
			s.Systems = append(s.Systems, i)
			s.Connections = append(s.Connections, [2]int64{100, i})
		}
		log, buf := testLogger()
		g := topology.New(home, topology.WithLogger(log))
		g.Install(s)
		out := buf.String()
		if !strings.Contains(out, "level=INFO") || !strings.Contains(out, "systems=22") ||
			!strings.Contains(out, "linked_systems=16") {
			t.Errorf("no info line with 22 systems and 16 linked:\n%s", out)
		}
	})
	t.Run("event that changes the linked systems", func(t *testing.T) {
		g, buf := graph(t, [2]int64{home, a}, [2]int64{c, d})
		buf.Reset()
		g.Apply(topology.EventConnectionAdded, added(t, a, c))
		out := buf.String()
		want := fmt.Sprintf("linked=\"[%d %d]\"", c, d)
		if !strings.Contains(out, "level=DEBUG") || !strings.Contains(out, want) {
			t.Errorf("no debug line with %s:\n%s", want, out)
		}

		buf.Reset()
		g.Apply(topology.EventConnectionRemoved, removed(t, home, a))
		want = fmt.Sprintf("unlinked=\"[%d %d %d]\"", a, c, d)
		if !strings.Contains(buf.String(), want) {
			t.Errorf("no debug line with %s:\n%s", want, buf)
		}
	})
	t.Run("event that changes nothing is quiet", func(t *testing.T) {
		g, buf := graph(t, [2]int64{home, a}, [2]int64{c, d})
		buf.Reset()
		g.Apply(topology.EventConnectionRemoved, removed(t, c, d))
		if buf.Len() != 0 {
			t.Errorf("logged for an unchanged linked set:\n%s", buf)
		}
	})
}

// TestConcurrentUse is meant for go test -race: events, resyncs and lookups
// from separate goroutines, as the stream, the resync ticker and the relay do.
func TestConcurrentUse(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	g := topology.New(home, topology.WithLogger(log))
	ctx := context.Background()

	// Built up front: t.Fatal must not run on another goroutine.
	var events [][3]json.RawMessage
	for id := int64(1); id <= 20; id++ {
		events = append(events, [3]json.RawMessage{added(t, home, id), removed(t, home, id), deleted(t, id)})
	}
	var renames []json.RawMessage
	for id := int64(1); id <= 20; id++ {
		renames = append(renames, metadata(t, id, fmt.Sprintf("A%d", id)), metadata(t, id, nil))
	}

	var wg sync.WaitGroup
	wg.Add(5)
	go func() {
		defer wg.Done()
		for i := range 400 {
			g.Apply(topology.EventSystemMetadataChanged, renames[i%len(renames)])
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 2000 {
			g.Alias(int64(i % 25))
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 200 {
			e := events[i%len(events)]
			g.Apply(topology.EventConnectionAdded, e[0])
			g.Apply(topology.EventConnectionRemoved, e[1])
			g.Apply(topology.EventDeletedSystem, e[2])
		}
	}()
	go func() {
		defer wg.Done()
		for range 50 {
			_ = g.Resync(ctx, func(context.Context) (topology.Snapshot, error) {
				return snapshot([2]int64{home, a}), nil
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 2000 {
			g.Linked(int64(i % 25))
		}
	}()
	wg.Wait()

	g.Install(snapshot([2]int64{home, a}))
	assertLinked(t, g, map[int64]bool{home: true, a: true, 5: false})
	assertAlias(t, g, 5, "")
}

// j134349 is the system the system_metadata_changed fixture names HOTEL.
const j134349 int64 = 31001234

// metadata returns the system_metadata_changed fixture for id with
// temporary_name set to name, or null when name is nil.
func metadata(t *testing.T, id int64, name any) json.RawMessage {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(fixture(t, "system_metadata_changed.json"), &fields); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	fields["solar_system_id"] = id
	fields["temporary_name"] = name
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// aliased returns a graph whose snapshot gives the systems these aliases.
func aliased(t *testing.T, home int64, aliases map[int64]string) (*topology.Graph, *bytes.Buffer) {
	t.Helper()
	log, buf := testLogger()
	g := topology.New(home, topology.WithLogger(log))
	s := snapshot([2]int64{home, j134349})
	s.Aliases = aliases
	g.Install(s)
	return g, buf
}

func assertAlias(t *testing.T, g *topology.Graph, id int64, want string) {
	t.Helper()
	if got := g.Alias(id); got != want {
		t.Errorf("Alias(%d) = %q, want %q", id, got, want)
	}
}

func TestAliasesTrackedForEveryMap(t *testing.T) {
	for _, h := range []int64{home, 0} {
		t.Run(fmt.Sprintf("home %d", h), func(t *testing.T) {
			g, _ := aliased(t, h, map[int64]string{j134349: "HOTEL"})
			assertAlias(t, g, j134349, "HOTEL")
			g.Apply(topology.EventSystemMetadataChanged, metadata(t, j134349, "INDIA"))
			assertAlias(t, g, j134349, "INDIA")
		})
	}
}

func TestAliasesFromSnapshot(t *testing.T) {
	t.Run("snapshot names a system", func(t *testing.T) {
		g, _ := aliased(t, home, map[int64]string{j134349: "HOTEL"})
		assertAlias(t, g, j134349, "HOTEL")
	})
	t.Run("snapshot clears a stale alias", func(t *testing.T) {
		g, _ := aliased(t, home, map[int64]string{j134349: "HOTEL"})
		g.Install(snapshot([2]int64{home, j134349}))
		assertAlias(t, g, j134349, "")
	})
	t.Run("blank temporary name", func(t *testing.T) {
		g, _ := aliased(t, home, map[int64]string{j134349: "  "})
		assertAlias(t, g, j134349, "")
	})
	t.Run("value is trimmed", func(t *testing.T) {
		g, _ := aliased(t, home, map[int64]string{j134349: " HOTEL\n"})
		assertAlias(t, g, j134349, "HOTEL")
	})
	t.Run("system without an alias", func(t *testing.T) {
		g, _ := aliased(t, home, map[int64]string{j134349: "HOTEL"})
		assertAlias(t, g, a, "")
	})
}

func TestAliasChangesFromStream(t *testing.T) {
	t.Run("fixture payload", func(t *testing.T) {
		g := topology.New(home)
		g.Apply(topology.EventSystemMetadataChanged, fixture(t, "system_metadata_changed.json"))
		assertAlias(t, g, j134349, "HOTEL")
	})
	t.Run("temporary name set after the system is linked", func(t *testing.T) {
		g, _ := aliased(t, home, nil)
		g.Apply(topology.EventSystemMetadataChanged, metadata(t, j134349, "HOTEL"))
		assertAlias(t, g, j134349, "HOTEL")
	})
	t.Run("temporary name renamed", func(t *testing.T) {
		g, _ := aliased(t, home, map[int64]string{j134349: "HOTEL"})
		g.Apply(topology.EventSystemMetadataChanged, metadata(t, j134349, "INDIA"))
		assertAlias(t, g, j134349, "INDIA")
	})
	for name, value := range map[string]any{"null": nil, "blank": " \t", "empty": ""} {
		t.Run("temporary name cleared: "+name, func(t *testing.T) {
			g, _ := aliased(t, home, map[int64]string{j134349: "HOTEL"})
			g.Apply(topology.EventSystemMetadataChanged, metadata(t, j134349, value))
			assertAlias(t, g, j134349, "")
		})
	}
	t.Run("temporary name missing", func(t *testing.T) {
		g, _ := aliased(t, home, map[int64]string{j134349: "HOTEL"})
		g.Apply(topology.EventSystemMetadataChanged,
			json.RawMessage(`{"solar_system_id": 31001234, "status": 2}`))
		assertAlias(t, g, j134349, "")
	})
	t.Run("other metadata changes", func(t *testing.T) {
		g, buf := aliased(t, home, map[int64]string{j134349: "HOTEL"})
		buf.Reset()
		var fields map[string]any
		if err := json.Unmarshal(metadata(t, j134349, "HOTEL"), &fields); err != nil {
			t.Fatal(err)
		}
		fields["status"] = 3
		fields["labels"] = `{"customLabel":"","labels":["la"]}`
		payload, _ := json.Marshal(fields)
		g.Apply(topology.EventSystemMetadataChanged, payload)
		assertAlias(t, g, j134349, "HOTEL")
		if buf.Len() != 0 {
			t.Errorf("logged for an unchanged alias:\n%s", buf)
		}
	})
	t.Run("system removed from the map", func(t *testing.T) {
		g, _ := aliased(t, home, map[int64]string{j134349: "HOTEL"})
		g.Apply(topology.EventDeletedSystem, deleted(t, j134349))
		assertAlias(t, g, j134349, "")
	})
	t.Run("metadata leaves the connections alone", func(t *testing.T) {
		g, _ := aliased(t, home, nil)
		g.Apply(topology.EventSystemMetadataChanged, metadata(t, j134349, "HOTEL"))
		assertLinked(t, g, map[int64]bool{j134349: true})
	})
}

func TestMalformedMetadataEvent(t *testing.T) {
	cases := []struct{ name, payload string }{
		{"missing system", `{"temporary_name": "INDIA"}`},
		{"string system", `{"solar_system_id": "J134349", "temporary_name": "INDIA"}`},
		{"null system", `{"solar_system_id": null, "temporary_name": "INDIA"}`},
		{"numeric name", `{"solar_system_id": 31001234, "temporary_name": 7}`},
		{"not an object", `["INDIA"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, buf := aliased(t, home, map[int64]string{j134349: "HOTEL", a: "ALPHA"})
			buf.Reset()
			g.Apply(topology.EventSystemMetadataChanged, json.RawMessage(tc.payload))
			if !strings.Contains(buf.String(), "level=WARN") {
				t.Errorf("no warning logged:\n%s", buf)
			}
			assertAlias(t, g, j134349, "HOTEL")
			assertAlias(t, g, a, "ALPHA")
		})
	}
}

func TestAliasChangesDuringSnapshotAreNotLost(t *testing.T) {
	t.Run("rename while a periodic snapshot is loading", func(t *testing.T) {
		g, _ := aliased(t, home, map[int64]string{j134349: "HOTEL"})
		err := g.Resync(context.Background(), func(context.Context) (topology.Snapshot, error) {
			g.Apply(topology.EventSystemMetadataChanged, metadata(t, j134349, "INDIA"))
			s := snapshot([2]int64{home, j134349})
			s.Aliases = map[int64]string{j134349: "HOTEL"}
			return s, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		assertAlias(t, g, j134349, "INDIA")
	})
	t.Run("clear while a snapshot is loading", func(t *testing.T) {
		g, _ := aliased(t, home, map[int64]string{j134349: "HOTEL"})
		g.BeginResync()
		g.Apply(topology.EventSystemMetadataChanged, metadata(t, j134349, nil))
		s := snapshot([2]int64{home, j134349})
		s.Aliases = map[int64]string{j134349: "HOTEL"}
		g.Install(s)
		assertAlias(t, g, j134349, "")
	})
	t.Run("deletion while a snapshot is loading", func(t *testing.T) {
		g, _ := aliased(t, home, map[int64]string{j134349: "HOTEL"})
		g.BeginResync()
		g.Apply(topology.EventDeletedSystem, deleted(t, j134349))
		s := snapshot([2]int64{home, j134349})
		s.Aliases = map[int64]string{j134349: "HOTEL"}
		g.Install(s)
		assertAlias(t, g, j134349, "")
	})
	t.Run("journal ends with the snapshot", func(t *testing.T) {
		g, _ := aliased(t, home, nil)
		g.BeginResync()
		g.Apply(topology.EventSystemMetadataChanged, metadata(t, j134349, "INDIA"))
		g.Install(snapshot([2]int64{home, j134349}))
		g.Install(snapshot([2]int64{home, j134349}))
		assertAlias(t, g, j134349, "")
	})
}

func TestAliasLogging(t *testing.T) {
	t.Run("snapshot summary", func(t *testing.T) {
		s := topology.Snapshot{Systems: []int64{home}, Aliases: map[int64]string{}}
		for i := int64(1); i <= 21; i++ {
			s.Systems = append(s.Systems, i)
			s.Connections = append(s.Connections, [2]int64{home, i})
			if i <= 9 {
				s.Aliases[i] = fmt.Sprintf("A%d", i)
			}
		}
		s.Aliases[1000] = " " // blank: no alias
		log, buf := testLogger()
		g := topology.New(home, topology.WithLogger(log))
		g.Install(s)
		out := buf.String()
		if !strings.Contains(out, "systems=22") || !strings.Contains(out, "aliases=9") {
			t.Errorf("no info line with 22 systems and 9 aliases:\n%s", out)
		}
	})
	t.Run("alias change is logged at debug", func(t *testing.T) {
		g, buf := aliased(t, home, nil)
		buf.Reset()
		g.Apply(topology.EventSystemMetadataChanged, metadata(t, j134349, "HOTEL"))
		out := buf.String()
		if !strings.Contains(out, "level=DEBUG") || !strings.Contains(out, "solar_system_id=31001234") ||
			!strings.Contains(out, "alias=HOTEL") {
			t.Errorf("no debug line with the system and alias:\n%s", out)
		}
	})
	t.Run("deleted alias is logged", func(t *testing.T) {
		g, buf := aliased(t, 0, map[int64]string{j134349: "HOTEL"})
		buf.Reset()
		g.Apply(topology.EventDeletedSystem, deleted(t, j134349))
		if !strings.Contains(buf.String(), "chain alias changed") {
			t.Errorf("no debug line for the cleared alias:\n%s", buf)
		}
	})
}

func TestHomelessGraph(t *testing.T) {
	newGraph := func() (*topology.Graph, *bytes.Buffer) {
		log, buf := testLogger()
		return topology.New(0, topology.WithLogger(log)), buf
	}
	t.Run("snapshot without a warning or linked count", func(t *testing.T) {
		g, buf := newGraph()
		g.Install(topology.Snapshot{Systems: []int64{c, d}, Connections: [][2]int64{{c, d}},
			Aliases: map[int64]string{c: "CHARLIE"}})
		out := buf.String()
		if strings.Contains(out, "level=WARN") || strings.Contains(out, "linked_systems") {
			t.Errorf("home-less snapshot logged a home warning or linked count:\n%s", out)
		}
		if !strings.Contains(out, "systems=2") || !strings.Contains(out, "aliases=1") {
			t.Errorf("no info line with 2 systems and 1 alias:\n%s", out)
		}
		assertAlias(t, g, c, "CHARLIE")
	})
	t.Run("all topology events apply quietly and link nothing", func(t *testing.T) {
		g, buf := newGraph()
		g.Install(topology.Snapshot{Systems: []int64{c, d}, Connections: [][2]int64{{c, d}}})
		buf.Reset()
		g.Apply(topology.EventConnectionAdded, added(t, d, a))
		g.Apply(topology.EventConnectionRemoved, removed(t, c, d))
		g.Apply(topology.EventDeletedSystem, deleted(t, a))
		if buf.Len() != 0 {
			t.Errorf("connection events logged without a home system:\n%s", buf)
		}
		assertLinked(t, g, map[int64]bool{0: false, a: false, c: false, d: false})
	})
	t.Run("journalled events replay onto a snapshot", func(t *testing.T) {
		g, buf := newGraph()
		g.BeginResync()
		g.Apply(topology.EventConnectionAdded, added(t, c, d))
		g.Apply(topology.EventSystemMetadataChanged, metadata(t, c, "CHARLIE"))
		g.Install(topology.Snapshot{Systems: []int64{c, d}})
		assertAlias(t, g, c, "CHARLIE")
		if !strings.Contains(buf.String(), "replayed_events=2") {
			t.Errorf("snapshot did not replay both events:\n%s", buf)
		}
		g.Apply(topology.EventDeletedSystem, deleted(t, c))
		assertAlias(t, g, c, "")
	})
}
