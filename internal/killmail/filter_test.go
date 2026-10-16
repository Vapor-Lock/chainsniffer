package killmail_test

import (
	"testing"
	"time"

	"github.com/Vapor-Lock/chainsniffer/internal/killmail"
)

// now is the fixed clock the age filter reads in these tests.
var now = time.Date(2026, 10, 13, 18, 45, 0, 0, time.UTC)

func fixedClock() func() time.Time {
	return func() time.Time { return now }
}

// defaultFilters mirrors the configuration defaults from the relay-runtime
// spec: NPC kills excluded, no minimum value, every classification posted and
// kills older than an hour dropped.
func defaultFilters() killmail.Filters {
	return killmail.Filters{ExcludeNPC: true, MaxKillAge: time.Hour, Now: fixedClock()}
}

// Each case is one scenario of the killmail-filtering spec's NPC, minimum
// value, friendly-only and maximum kill age requirements.
func TestFiltersAllow(t *testing.T) {
	tests := []struct {
		name        string
		filters     killmail.Filters
		kill        killmail.Killmail
		class       killmail.Classification
		wantAllowed bool
		wantFilter  string
	}{
		{
			name:        "defaults post an ordinary kill",
			filters:     defaultFilters(),
			kill:        killmail.Killmail{ID: 1, KillTime: now.Add(-2 * time.Minute), TotalValue: 2_500_000},
			class:       killmail.Neutral,
			wantAllowed: true,
		},
		{
			name:       "rat kill with default settings",
			filters:    defaultFilters(),
			kill:       killmail.Killmail{ID: 2, KillTime: now.Add(-time.Minute), NPC: true},
			class:      killmail.Neutral,
			wantFilter: killmail.FilterNPC,
		},
		{
			name:        "NPC kills explicitly allowed",
			filters:     killmail.Filters{ExcludeNPC: false, MaxKillAge: time.Hour, Now: fixedClock()},
			kill:        killmail.Killmail{ID: 3, KillTime: now.Add(-time.Minute), NPC: true},
			class:       killmail.Neutral,
			wantAllowed: true,
		},
		{
			name:       "cheap kill below threshold",
			filters:    killmail.Filters{MinValue: 10_000_000, Now: fixedClock()},
			kill:       killmail.Killmail{ID: 4, KillTime: now, TotalValue: 2_500_000},
			class:      killmail.Neutral,
			wantFilter: killmail.FilterMinValue,
		},
		{
			name:        "kill at threshold",
			filters:     killmail.Filters{MinValue: 10_000_000, Now: fixedClock()},
			kill:        killmail.Killmail{ID: 5, KillTime: now, TotalValue: 10_000_000},
			class:       killmail.Neutral,
			wantAllowed: true,
		},
		{
			// A payload without total_value counts as 0 ISK.
			name:       "missing value counts as zero",
			filters:    killmail.Filters{MinValue: 1, Now: fixedClock()},
			kill:       killmail.Killmail{ID: 6, KillTime: now},
			class:      killmail.Neutral,
			wantFilter: killmail.FilterMinValue,
		},
		{
			name:        "missing value passes the default minimum",
			filters:     defaultFilters(),
			kill:        killmail.Killmail{ID: 7, KillTime: now},
			class:       killmail.Neutral,
			wantAllowed: true,
		},
		{
			name:       "neutral kill in friendly-only mode",
			filters:    killmail.Filters{FriendlyOnly: true, Now: fixedClock()},
			kill:       killmail.Killmail{ID: 8, KillTime: now},
			class:      killmail.Neutral,
			wantFilter: killmail.FilterFriendlyOnly,
		},
		{
			name:        "loss in friendly-only mode",
			filters:     killmail.Filters{FriendlyOnly: true, Now: fixedClock()},
			kill:        killmail.Killmail{ID: 9, KillTime: now},
			class:       killmail.Loss,
			wantAllowed: true,
		},
		{
			name:        "kill in friendly-only mode",
			filters:     killmail.Filters{FriendlyOnly: true, Now: fixedClock()},
			kill:        killmail.Killmail{ID: 10, KillTime: now},
			class:       killmail.Kill,
			wantAllowed: true,
		},
		{
			name:        "neutral kill with friendly-only off",
			filters:     defaultFilters(),
			kill:        killmail.Killmail{ID: 11, KillTime: now},
			class:       killmail.Neutral,
			wantAllowed: true,
		},
		{
			name:       "historical kill for a newly mapped system",
			filters:    defaultFilters(),
			kill:       killmail.Killmail{ID: 12, KillTime: now.Add(-5 * time.Hour)},
			class:      killmail.Neutral,
			wantFilter: killmail.FilterMaxKillAge,
		},
		{
			name:        "fresh kill",
			filters:     defaultFilters(),
			kill:        killmail.Killmail{ID: 13, KillTime: now.Add(-2 * time.Minute)},
			class:       killmail.Neutral,
			wantAllowed: true,
		},
		{
			name:        "kill exactly at the age limit",
			filters:     defaultFilters(),
			kill:        killmail.Killmail{ID: 14, KillTime: now.Add(-time.Hour)},
			class:       killmail.Neutral,
			wantAllowed: true,
		},
		{
			// kill_time absent or unparseable decodes to the zero time; such a
			// killmail is kept rather than treated as infinitely old.
			name:        "unknown kill time is kept",
			filters:     defaultFilters(),
			kill:        killmail.Killmail{ID: 15},
			class:       killmail.Neutral,
			wantAllowed: true,
		},
		{
			// Wanderer can send a kill whose time is slightly ahead of ours.
			name:        "kill time in the future is kept",
			filters:     defaultFilters(),
			kill:        killmail.Killmail{ID: 16, KillTime: now.Add(5 * time.Minute)},
			class:       killmail.Neutral,
			wantAllowed: true,
		},
		{
			name:        "zero max age disables the age filter",
			filters:     killmail.Filters{Now: fixedClock()},
			kill:        killmail.Killmail{ID: 17, KillTime: now.Add(-100 * 24 * time.Hour)},
			class:       killmail.Neutral,
			wantAllowed: true,
		},
		{
			name:        "zero value posts everything",
			kill:        killmail.Killmail{ID: 18, KillTime: now.Add(-5 * time.Hour), NPC: true},
			class:       killmail.Neutral,
			wantAllowed: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			allowed, filter := tc.filters.Allow(tc.kill, tc.class)
			if allowed != tc.wantAllowed {
				t.Errorf("Allow() allowed = %v, want %v (filter %q)", allowed, tc.wantAllowed, filter)
			}
			if filter != tc.wantFilter {
				t.Errorf("Allow() filter = %q, want %q", filter, tc.wantFilter)
			}
		})
	}
}

// The chain reports the first filter that matches, in the order linked system
// (see TestFiltersAllowLinkedSystemComesFirst), NPC, minimum value,
// friendly-only, maximum age, so the relay's debug line names one filter even
// when several would drop the kill.
func TestFiltersAllowReportsFirstMatch(t *testing.T) {
	f := killmail.Filters{
		ExcludeNPC:   true,
		MinValue:     10_000_000,
		FriendlyOnly: true,
		MaxKillAge:   time.Hour,
		Now:          fixedClock(),
	}
	k := killmail.Killmail{ID: 1, KillTime: now.Add(-5 * time.Hour), NPC: true}

	allowed, filter := f.Allow(k, killmail.Neutral)
	if allowed || filter != killmail.FilterNPC {
		t.Fatalf("Allow() = %v, %q; want false, %q", allowed, filter, killmail.FilterNPC)
	}

	f.ExcludeNPC = false
	if allowed, filter = f.Allow(k, killmail.Neutral); allowed || filter != killmail.FilterMinValue {
		t.Fatalf("Allow() = %v, %q; want false, %q", allowed, filter, killmail.FilterMinValue)
	}

	k.TotalValue = 20_000_000
	if allowed, filter = f.Allow(k, killmail.Neutral); allowed || filter != killmail.FilterFriendlyOnly {
		t.Fatalf("Allow() = %v, %q; want false, %q", allowed, filter, killmail.FilterFriendlyOnly)
	}

	if allowed, filter = f.Allow(k, killmail.Loss); allowed || filter != killmail.FilterMaxKillAge {
		t.Fatalf("Allow() = %v, %q; want false, %q", allowed, filter, killmail.FilterMaxKillAge)
	}
}

// A nil clock must fall back to time.Now rather than to the zero time, which
// would make every killmail look like it came from the future.
func TestFiltersAllowNilClock(t *testing.T) {
	f := killmail.Filters{MaxKillAge: time.Hour}

	fresh := killmail.Killmail{ID: 1, KillTime: time.Now().Add(-time.Minute)}
	if allowed, filter := f.Allow(fresh, killmail.Neutral); !allowed {
		t.Errorf("fresh kill dropped by %q", filter)
	}

	old := killmail.Killmail{ID: 2, KillTime: time.Now().Add(-5 * time.Hour)}
	if allowed, filter := f.Allow(old, killmail.Neutral); allowed || filter != killmail.FilterMaxKillAge {
		t.Errorf("Allow() = %v, %q; want false, %q", allowed, filter, killmail.FilterMaxKillAge)
	}
}

// The filters work on what the decoder produces, not on hand-built structs.
func TestFiltersAllowDecodedPayload(t *testing.T) {
	payload := `{"type":"killmail_update","killmails":[
		{"killmail_id":1,"kill_time":"2026-10-13T18:44:00Z","total_value":18750000.0,"npc":true},
		{"killmail_id":2,"kill_time":"2026-10-13T13:00:00Z","total_value":500000000.0},
		{"killmail_id":3,"kill_time":"2026-10-13T18:40:00Z","total_value":2500000.0,
		 "victim_corp_id":98000001},
		{"killmail_id":4,"kill_time":"2026-10-13T18:44:30Z","total_value":412500000.55,
		 "victim_corp_id":98000001}]}`

	log, _ := testLogger()
	kills := killmail.Extract([]byte(payload), log)
	if len(kills) != 4 {
		t.Fatalf("got %d killmails, want 4", len(kills))
	}

	friendlies := killmail.NewFriendlies([]int64{friendlyCorp}, nil)
	filters := killmail.Filters{
		ExcludeNPC: true,
		MinValue:   10_000_000,
		MaxKillAge: time.Hour,
		Now:        fixedClock(),
	}

	wantFilters := []string{
		killmail.FilterNPC,
		killmail.FilterMaxKillAge,
		killmail.FilterMinValue,
		"",
	}
	for i, k := range kills {
		allowed, filter := filters.Allow(k, friendlies.Classify(k))
		if filter != wantFilters[i] {
			t.Errorf("killmail %d: filter = %q, want %q", k.ID, filter, wantFilters[i])
		}
		if allowed != (wantFilters[i] == "") {
			t.Errorf("killmail %d: allowed = %v", k.ID, allowed)
		}
	}
}

// The home chain the linked-system scenarios run against: the home system and
// one system linked to it, with an island elsewhere on the map.
const (
	homeSystem   = 31000001
	linkedSystem = 31000002
	islandSystem = 31000099
)

func linkedToHome(id int64) bool { return id == homeSystem || id == linkedSystem }

// Each case is one scenario of the killmail-filtering spec's linked-system
// filter requirement.
func TestFiltersAllowLinkedSystem(t *testing.T) {
	tests := []struct {
		name        string
		linked      func(int64) bool
		systemID    int64
		wantAllowed bool
	}{
		{name: "kill in a linked system", linked: linkedToHome, systemID: linkedSystem, wantAllowed: true},
		{name: "kill in an unlinked island", linked: linkedToHome, systemID: islandSystem},
		{name: "kill in the home system", linked: linkedToHome, systemID: homeSystem, wantAllowed: true},
		{
			// The predicate is never asked about system 0: an unknown system
			// cannot be on the home chain.
			name:     "no system known",
			linked:   func(int64) bool { return true },
			systemID: 0,
		},
		{name: "no home system, island", systemID: islandSystem, wantAllowed: true},
		{name: "no home system, unknown system", systemID: 0, wantAllowed: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := killmail.Filters{Linked: tc.linked, Now: fixedClock()}
			k := killmail.Killmail{ID: 1, KillTime: now, SolarSystemID: tc.systemID}

			allowed, filter := f.Allow(k, killmail.Neutral)
			wantFilter := ""
			if !tc.wantAllowed {
				wantFilter = killmail.FilterUnlinkedSystem
			}
			if allowed != tc.wantAllowed || filter != wantFilter {
				t.Errorf("Allow() = %v, %q; want %v, %q", allowed, filter, tc.wantAllowed, wantFilter)
			}
		})
	}
}

// The linked-system filter runs before every other one, so a kill in an island
// is reported as unlinked even when it would also fail the rest.
func TestFiltersAllowLinkedSystemComesFirst(t *testing.T) {
	f := killmail.Filters{
		Linked:     linkedToHome,
		ExcludeNPC: true,
		MinValue:   10_000_000,
		MaxKillAge: time.Hour,
		Now:        fixedClock(),
	}
	k := killmail.Killmail{ID: 1, KillTime: now.Add(-5 * time.Hour), NPC: true, SolarSystemID: islandSystem}

	if allowed, filter := f.Allow(k, killmail.Neutral); allowed || filter != killmail.FilterUnlinkedSystem {
		t.Fatalf("Allow() = %v, %q; want false, %q", allowed, filter, killmail.FilterUnlinkedSystem)
	}
}

// The filter reads the topology as it stands when the killmail is processed:
// a replayed kill whose system has since been cut off is dropped.
func TestFiltersAllowLinkedAtProcessingTime(t *testing.T) {
	linked := map[int64]bool{homeSystem: true, linkedSystem: true}
	f := killmail.Filters{Linked: func(id int64) bool { return linked[id] }, Now: fixedClock()}
	k := killmail.Killmail{ID: 1, KillTime: now, SolarSystemID: linkedSystem}

	if allowed, filter := f.Allow(k, killmail.Neutral); !allowed {
		t.Fatalf("kill in a linked system dropped by %q", filter)
	}

	delete(linked, linkedSystem)
	if allowed, filter := f.Allow(k, killmail.Neutral); allowed || filter != killmail.FilterUnlinkedSystem {
		t.Errorf("after the system is cut off, Allow() = %v, %q; want false, %q",
			allowed, filter, killmail.FilterUnlinkedSystem)
	}
}

// The system the filter checks is the one the decoder settled on, which falls
// back to the payload's when an entry has none.
func TestFiltersAllowLinkedSystemFromPayload(t *testing.T) {
	payload := `{"type":"killmail_update","solar_system_id":31000002,"killmails":[
		{"killmail_id":1,"kill_time":"2026-10-13T18:44:00Z"},
		{"killmail_id":2,"kill_time":"2026-10-13T18:44:00Z","solar_system_id":31000099}]}`

	log, _ := testLogger()
	kills := killmail.Extract([]byte(payload), log)
	if len(kills) != 2 {
		t.Fatalf("got %d killmails, want 2", len(kills))
	}

	f := killmail.Filters{Linked: linkedToHome, Now: fixedClock()}
	if allowed, filter := f.Allow(kills[0], killmail.Neutral); !allowed {
		t.Errorf("killmail with the payload's linked system dropped by %q", filter)
	}
	if allowed, filter := f.Allow(kills[1], killmail.Neutral); allowed || filter != killmail.FilterUnlinkedSystem {
		t.Errorf("killmail in its own island system: Allow() = %v, %q; want false, %q",
			allowed, filter, killmail.FilterUnlinkedSystem)
	}
}
