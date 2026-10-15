package killmail_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Vapor-Lock/chainsniffer/internal/killmail"
)

// testLogger returns a debug-level logger and the buffer holding its output, so
// tests can assert on the warnings the decoder emits.
func testLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), &buf
}

// loadPayload reads a fixture shaped like an event Wanderer's
// format_sse_event writes, with a payload from its message_handler, and returns
// the envelope's payload: what the relay hands to Extract.
func loadPayload(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var env struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	if env.Type != killmail.EventMapKill {
		t.Fatalf("fixture %s has event type %q, want %q", name, env.Type, killmail.EventMapKill)
	}
	return env.Payload
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return parsed.UTC()
}

func TestExtractKillmailUpdate(t *testing.T) {
	log, buf := testLogger()
	kills := killmail.Extract(loadPayload(t, "killmail_update.json"), log)
	if len(kills) != 2 {
		t.Fatalf("got %d killmails, want 2", len(kills))
	}

	want := killmail.Killmail{
		ID:            123456789,
		KillTime:      mustTime(t, "2026-10-13T18:41:02Z"),
		SolarSystemID: 31002604,
		Victim: killmail.Party{
			CharID: 2112625428, CharName: "Hapless Scout",
			CorpID: 98000001, CorpTicker: "VLOCK", CorpName: "Vapor Lock",
			AllianceID: 99000001, AllianceTicker: "VPRL", AllianceName: "Vapor Lock Alliance",
			ShipTypeID: 11202, ShipName: "Ares",
		},
		FinalBlow: killmail.Party{
			CharID: 90000042, CharName: "Camping Hostile",
			CorpID: 98765432, CorpTicker: "HOST", CorpName: "Hostile Holdings",
			AllianceID: 99887766, AllianceTicker: "HSTL", AllianceName: "Hostile Alliance",
			ShipTypeID: 29988, ShipName: "Proteus",
		},
		AttackerCount: 4,
		TotalValue:    412500000.55,
		NPC:           false,
	}
	if kills[0] != want {
		t.Errorf("first killmail:\n got %+v\nwant %+v", kills[0], want)
	}
	if strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("unexpected warning: %s", buf)
	}
}

// A structure kill has no victim character, which must not stop it from being
// decoded: every field but killmail_id is optional.
func TestExtractStructureKill(t *testing.T) {
	log, buf := testLogger()
	kills := killmail.Extract(loadPayload(t, "killmail_update.json"), log)
	if len(kills) != 2 {
		t.Fatalf("got %d killmails, want 2", len(kills))
	}

	k := kills[1]
	if k.ID != 123456790 {
		t.Errorf("ID = %d, want 123456790", k.ID)
	}
	if k.Victim.CharID != 0 || k.Victim.CharName != "" {
		t.Errorf("victim character = %d/%q, want empty", k.Victim.CharID, k.Victim.CharName)
	}
	if k.Victim.AllianceID != 0 || k.Victim.AllianceTicker != "" {
		t.Errorf("victim alliance = %d/%q, want empty", k.Victim.AllianceID, k.Victim.AllianceTicker)
	}
	if k.Victim.ShipName != "Astrahus" || k.Victim.CorpTicker != "VLOCK" {
		t.Errorf("victim ship/corp = %q/%q", k.Victim.ShipName, k.Victim.CorpTicker)
	}
	if k.AttackerCount != 17 || k.TotalValue != 1870000000 {
		t.Errorf("attackers/value = %d/%v", k.AttackerCount, k.TotalValue)
	}
	if strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("unexpected warning: %s", buf)
	}
}

func TestExtractKillCount(t *testing.T) {
	log, buf := testLogger()
	if kills := killmail.Extract(loadPayload(t, "kill_count.json"), log); len(kills) != 0 {
		t.Fatalf("got %d killmails, want none", len(kills))
	}
	if !strings.Contains(buf.String(), killmail.PayloadKillCount) {
		t.Errorf("ignored payload type not logged: %s", buf)
	}
}

func TestExtractSkipsEntriesWithoutIntegerID(t *testing.T) {
	log, buf := testLogger()
	kills := killmail.Extract(loadPayload(t, "missing_killmail_id.json"), log)
	if len(kills) != 1 {
		t.Fatalf("got %d killmails, want 1", len(kills))
	}
	if kills[0].ID != 987654321 {
		t.Errorf("ID = %d, want 987654321", kills[0].ID)
	}
	if !kills[0].NPC {
		t.Error("npc = false, want true")
	}
	if n := strings.Count(buf.String(), "without an integer killmail_id"); n != 2 {
		t.Errorf("got %d warnings, want 2: %s", n, buf)
	}
}

func TestExtractUnknownAndMalformedPayloads(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		warn    bool
	}{
		{name: "unknown type", payload: `{"type":"signatures_updated","added":[]}`},
		{name: "no type", payload: `{"killmails":[{"killmail_id":1}]}`},
		{name: "empty list", payload: `{"type":"killmail_update","killmails":[]}`},
		{name: "null killmails", payload: `{"type":"killmail_update","killmails":null}`},
		{name: "not an object", payload: `"killmail_update"`, warn: true},
		{name: "truncated", payload: `{"type":"killmail_upd`, warn: true},
		{name: "entry not an object", payload: `{"type":"killmail_update","killmails":[42]}`, warn: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			log, buf := testLogger()
			if kills := killmail.Extract([]byte(tc.payload), log); len(kills) != 0 {
				t.Fatalf("got %d killmails, want none", len(kills))
			}
			if warned := strings.Contains(buf.String(), "level=WARN"); warned != tc.warn {
				t.Errorf("warned = %v, want %v: %s", warned, tc.warn, buf)
			}
		})
	}
}

// Wanderer's kill payload is an internal map rather than a schema, so an
// unexpected type in an optional field must degrade to the zero value instead
// of dropping the killmail.
func TestExtractLenientFields(t *testing.T) {
	payload := `{"type":"killmail_update","killmails":[{
		"killmail_id": "123456789",
		"kill_time": null,
		"solar_system_id": 31002604.0,
		"victim_char_name": 17,
		"victim_corp_id": "98000001",
		"victim_ship_type_id": {"id": 11202},
		"attacker_count": 2.5,
		"total_value": "412500000.55",
		"npc": "true"
	}]}`
	log, buf := testLogger()
	kills := killmail.Extract([]byte(payload), log)
	if len(kills) != 1 {
		t.Fatalf("got %d killmails, want 1: %s", len(kills), buf)
	}

	k := kills[0]
	if k.ID != 123456789 {
		t.Errorf("ID = %d, want 123456789", k.ID)
	}
	if !k.KillTime.IsZero() {
		t.Errorf("KillTime = %v, want zero", k.KillTime)
	}
	if k.SolarSystemID != 31002604 {
		t.Errorf("SolarSystemID = %d, want 31002604", k.SolarSystemID)
	}
	if k.Victim.CharName != "" {
		t.Errorf("Victim.CharName = %q, want empty", k.Victim.CharName)
	}
	if k.Victim.CorpID != 98000001 {
		t.Errorf("Victim.CorpID = %d, want 98000001", k.Victim.CorpID)
	}
	if k.Victim.ShipTypeID != 0 {
		t.Errorf("Victim.ShipTypeID = %d, want 0", k.Victim.ShipTypeID)
	}
	if k.AttackerCount != 0 {
		t.Errorf("AttackerCount = %d, want 0", k.AttackerCount)
	}
	if k.TotalValue != 412500000.55 {
		t.Errorf("TotalValue = %v, want 412500000.55", k.TotalValue)
	}
	if !k.NPC {
		t.Error("NPC = false, want true")
	}
}

func TestExtractKillTime(t *testing.T) {
	tests := []struct {
		raw  string
		want string // empty means the zero time
	}{
		{raw: `"2026-10-13T18:41:02Z"`, want: "2026-10-13T18:41:02Z"},
		{raw: `"2026-10-13T18:41:02.512Z"`, want: "2026-10-13T18:41:02.512Z"},
		{raw: `"2026-10-13T20:41:02+02:00"`, want: "2026-10-13T18:41:02Z"},
		{raw: `"2026-10-13T18:41:02"`, want: "2026-10-13T18:41:02Z"},
		{raw: `"2026-10-13 18:41:02"`, want: "2026-10-13T18:41:02Z"},
		{raw: `"yesterday"`},
		{raw: `""`},
		{raw: `null`},
		{raw: `1760380862`},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			log, _ := testLogger()
			payload := `{"type":"killmail_update","killmails":[{"killmail_id":1,"kill_time":` + tc.raw + `}]}`
			kills := killmail.Extract([]byte(payload), log)
			if len(kills) != 1 {
				t.Fatalf("got %d killmails, want 1", len(kills))
			}
			var want time.Time
			if tc.want != "" {
				want = mustTime(t, tc.want)
			}
			if !kills[0].KillTime.Equal(want) {
				t.Errorf("KillTime = %v, want %v", kills[0].KillTime, want)
			}
		})
	}
}

// Extract must leave the remaining entries alone when one is skipped, and keep
// the payload's order so the relay posts kills in the order they happened.
func TestExtractPreservesOrder(t *testing.T) {
	payload := `{"type":"killmail_update","killmails":[
		{"killmail_id":3},{"killmail_id":0},{"killmail_id":1},{},{"killmail_id":2}]}`
	log, _ := testLogger()
	kills := killmail.Extract([]byte(payload), log)
	got := make([]int64, 0, len(kills))
	for _, k := range kills {
		got = append(got, k.ID)
	}
	if len(got) != 3 || got[0] != 3 || got[1] != 1 || got[2] != 2 {
		t.Errorf("IDs = %v, want [3 1 2]", got)
	}
}

func TestExtractNilLoggerUsesDefault(t *testing.T) {
	kills := killmail.Extract([]byte(`{"type":"killmail_update","killmails":[{"killmail_id":7}]}`), nil)
	if len(kills) != 1 || kills[0].ID != 7 {
		t.Fatalf("got %+v, want one killmail with ID 7", kills)
	}
}
