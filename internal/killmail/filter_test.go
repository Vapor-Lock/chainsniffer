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

// The chain reports the first filter that matches, in the order NPC, minimum
// value, friendly-only, maximum age, so the relay's debug line names one
// filter even when several would drop the kill.
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
