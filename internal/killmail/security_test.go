package killmail_test

import (
	"testing"

	"github.com/Vapor-Lock/chainsniffer/internal/killmail"
)

// Each case is a scenario of the killmail-filtering spec's security classes
// requirement, or one of the edges between the classes.
func TestClassOf(t *testing.T) {
	tests := []struct {
		security float64
		want     killmail.SecurityClass
	}{
		{1.0, killmail.HighSec},
		{0.46, killmail.HighSec},
		{0.45, killmail.HighSec},
		{0.4499, killmail.LowSec},
		{0.02, killmail.LowSec},
		{0.0001, killmail.LowSec},
		{0.0, killmail.NullSec},
		{-0.2, killmail.NullSec},
		{-1.0, killmail.NullSec},
	}
	for _, tc := range tests {
		if got := killmail.ClassOf(tc.security); got != tc.want {
			t.Errorf("ClassOf(%v) = %q, want %q", tc.security, got, tc.want)
		}
	}
}

func TestIsKSpace(t *testing.T) {
	tests := []struct {
		name     string
		systemID int64
		want     bool
	}{
		{"Jita", 30000142, true},
		{"last known-space ID", 30999999, true},
		{"Zarzakh", 30100000, true},
		{"Thera", 31000005, false},
		{"first wormhole ID", 31000000, false},
		{"J-space C2", 31002604, false},
		{"Abyssal pocket", 32000001, false},
		{"unknown system", 0, false},
		{"below known space", 29999999, false},
	}
	for _, tc := range tests {
		if got := killmail.IsKSpace(tc.systemID); got != tc.want {
			t.Errorf("IsKSpace(%d) (%s) = %v, want %v", tc.systemID, tc.name, got, tc.want)
		}
	}
}

// Systems the k-space scenarios run against, with the security status the
// fake lookup answers for each.
const (
	highSystem       = 30000142 // Jita, 0.9459
	lowSystem        = 30002813 // Tama, 0.3
	nullSystem       = 30004759 // 1DQ1-A, -0.38
	unresolvedSystem = 30000001
	wormholeSystem   = 31002604
	theraSystem      = 31000005
)

var securities = map[int64]float64{highSystem: 0.9459, lowSystem: 0.3, nullSystem: -0.38}

// lookup returns a Security function answering from securities and the
// number of times it has been asked.
func lookup() (func(int64) (float64, bool), *int) {
	calls := 0
	return func(id int64) (float64, bool) {
		calls++
		s, ok := securities[id]
		return s, ok
	}, &calls
}

func classes(cs ...killmail.SecurityClass) map[killmail.SecurityClass]bool {
	set := make(map[killmail.SecurityClass]bool, len(cs))
	for _, c := range cs {
		set[c] = true
	}
	return set
}

// Each case is one scenario of the killmail-filtering spec's k-space security
// filter and unresolved security requirements, or one branch of the filter.
func TestFiltersAllowKSpace(t *testing.T) {
	all := classes(killmail.HighSec, killmail.LowSec, killmail.NullSec)
	tests := []struct {
		name        string
		kspace      map[killmail.SecurityClass]bool
		nilSecurity bool
		systemID    int64
		wantAllowed bool
		wantLookup  bool
	}{
		{name: "class not configured", kspace: classes(killmail.LowSec, killmail.NullSec),
			systemID: highSystem, wantLookup: true},
		{name: "class configured", kspace: classes(killmail.LowSec, killmail.NullSec),
			systemID: lowSystem, wantAllowed: true, wantLookup: true},
		{name: "only null configured, null kill", kspace: classes(killmail.NullSec),
			systemID: nullSystem, wantAllowed: true, wantLookup: true},
		{name: "no classes configured", systemID: nullSystem},
		{name: "empty set", kspace: classes(), systemID: highSystem},
		{name: "false entries count as unset", kspace: map[killmail.SecurityClass]bool{killmail.HighSec: false},
			systemID: highSystem},
		{name: "all classes configured", kspace: all, systemID: highSystem, wantAllowed: true},
		{name: "all classes, unresolvable system", kspace: all, systemID: unresolvedSystem, wantAllowed: true},
		{name: "wormhole kill, no classes", systemID: wormholeSystem, wantAllowed: true},
		{name: "Thera kill, no classes", systemID: theraSystem, wantAllowed: true},
		{name: "wormhole kill, some classes", kspace: classes(killmail.HighSec),
			systemID: wormholeSystem, wantAllowed: true},
		{name: "unknown system, no classes", systemID: 0, wantAllowed: true},
		{name: "unknown system, some classes", kspace: classes(killmail.HighSec), systemID: 0, wantAllowed: true},
		{name: "security lookup fails", kspace: classes(killmail.NullSec),
			systemID: unresolvedSystem, wantAllowed: true, wantLookup: true},
		{name: "no security lookup wired", kspace: classes(killmail.NullSec), nilSecurity: true,
			systemID: highSystem, wantAllowed: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			security, calls := lookup()
			f := killmail.Filters{KSpace: tc.kspace, Security: security, Now: fixedClock()}
			if tc.nilSecurity {
				f.Security = nil
			}
			k := killmail.Killmail{ID: 1, KillTime: now, SolarSystemID: tc.systemID}

			allowed, filter := f.Allow(k, killmail.Neutral)
			wantFilter := ""
			if !tc.wantAllowed {
				wantFilter = killmail.FilterKSpaceSecurity
			}
			if allowed != tc.wantAllowed || filter != wantFilter {
				t.Errorf("Allow() = %v, %q; want %v, %q", allowed, filter, tc.wantAllowed, wantFilter)
			}
			if gotLookup := *calls > 0; gotLookup != tc.wantLookup {
				t.Errorf("Security called %d times, want lookup %v", *calls, tc.wantLookup)
			}
		})
	}
}

// The k-space filter runs after the linked-system filter and before the
// payload-only ones.
func TestFiltersAllowKSpaceOrder(t *testing.T) {
	f := killmail.Filters{
		Linked:     func(id int64) bool { return id == highSystem },
		ExcludeNPC: true,
		Now:        fixedClock(),
	}

	island := killmail.Killmail{ID: 1, KillTime: now, NPC: true, SolarSystemID: lowSystem}
	if allowed, filter := f.Allow(island, killmail.Neutral); allowed || filter != killmail.FilterUnlinkedSystem {
		t.Errorf("Allow() = %v, %q; want false, %q", allowed, filter, killmail.FilterUnlinkedSystem)
	}

	linked := killmail.Killmail{ID: 2, KillTime: now, NPC: true, SolarSystemID: highSystem}
	if allowed, filter := f.Allow(linked, killmail.Neutral); allowed || filter != killmail.FilterKSpaceSecurity {
		t.Errorf("Allow() = %v, %q; want false, %q", allowed, filter, killmail.FilterKSpaceSecurity)
	}
}
