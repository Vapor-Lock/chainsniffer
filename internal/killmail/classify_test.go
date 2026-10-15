package killmail_test

import (
	"testing"

	"github.com/Vapor-Lock/chainsniffer/internal/killmail"
)

const (
	friendlyCorp     = int64(98000001)
	friendlyAlliance = int64(99000001)
	hostileCorp      = int64(98765432)
	hostileAlliance  = int64(99887766)
)

// Each case is one scenario of the killmail-filtering spec's "Kill
// classification" requirement.
func TestClassify(t *testing.T) {
	tests := []struct {
		name        string
		corpIDs     []int64
		allianceIDs []int64
		victim      killmail.Party
		finalBlow   killmail.Party
		want        killmail.Classification
	}{
		{
			name:        "friendly victim",
			allianceIDs: []int64{friendlyAlliance},
			victim:      killmail.Party{CorpID: 98123456, AllianceID: friendlyAlliance},
			finalBlow:   killmail.Party{CorpID: hostileCorp, AllianceID: hostileAlliance},
			want:        killmail.Loss,
		},
		{
			name:      "friendly final blow",
			corpIDs:   []int64{friendlyCorp},
			victim:    killmail.Party{CorpID: hostileCorp, AllianceID: hostileAlliance},
			finalBlow: killmail.Party{CorpID: friendlyCorp},
			want:      killmail.Kill,
		},
		{
			name:        "blue on blue",
			corpIDs:     []int64{friendlyCorp},
			allianceIDs: []int64{friendlyAlliance},
			victim:      killmail.Party{CorpID: friendlyCorp, AllianceID: friendlyAlliance},
			finalBlow:   killmail.Party{CorpID: friendlyCorp, AllianceID: friendlyAlliance},
			want:        killmail.Loss,
		},
		{
			name:        "unrelated pilots",
			corpIDs:     []int64{friendlyCorp},
			allianceIDs: []int64{friendlyAlliance},
			victim:      killmail.Party{CorpID: 98123456, AllianceID: 99123456},
			finalBlow:   killmail.Party{CorpID: hostileCorp, AllianceID: hostileAlliance},
			want:        killmail.Neutral,
		},
		{
			name:      "no friendly IDs configured",
			victim:    killmail.Party{CorpID: friendlyCorp, AllianceID: friendlyAlliance},
			finalBlow: killmail.Party{CorpID: hostileCorp},
			want:      killmail.Neutral,
		},
		{
			name:        "friendly alliance of a hostile final blow is not a kill",
			allianceIDs: []int64{friendlyAlliance},
			victim:      killmail.Party{CorpID: hostileCorp, AllianceID: hostileAlliance},
			finalBlow:   killmail.Party{CorpID: 98123456, AllianceID: 99123456},
			want:        killmail.Neutral,
		},
		{
			name:        "friendly alliance on the final blow",
			allianceIDs: []int64{friendlyAlliance},
			victim:      killmail.Party{CorpID: hostileCorp, AllianceID: hostileAlliance},
			finalBlow:   killmail.Party{CorpID: 98123456, AllianceID: friendlyAlliance},
			want:        killmail.Kill,
		},
		{
			// A structure kill has no victim character and may have no
			// alliance, so classification must fall back to the corporation.
			name:    "structure of a friendly corp",
			corpIDs: []int64{friendlyCorp},
			victim:  killmail.Party{CorpID: friendlyCorp, ShipName: "Astrahus"},
			want:    killmail.Loss,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := killmail.NewFriendlies(tc.corpIDs, tc.allianceIDs)
			k := killmail.Killmail{ID: 1, Victim: tc.victim, FinalBlow: tc.finalBlow}
			if got := f.Classify(k); got != tc.want {
				t.Errorf("Classify() = %q, want %q", got, tc.want)
			}
		})
	}
}

// An omitted ID decodes to 0, which must never match a friendly entry; nor may
// a misconfigured 0 in the friendly lists match everything.
func TestClassifyIgnoresMissingAndNonPositiveIDs(t *testing.T) {
	f := killmail.NewFriendlies([]int64{0, -1, friendlyCorp}, []int64{0})
	if f.Empty() {
		t.Fatal("Empty() = true, want false")
	}
	if got := f.Classify(killmail.Killmail{ID: 1}); got != killmail.Neutral {
		t.Errorf("Classify() = %q, want %q", got, killmail.Neutral)
	}
	k := killmail.Killmail{ID: 2, Victim: killmail.Party{CorpID: friendlyCorp}}
	if got := f.Classify(k); got != killmail.Loss {
		t.Errorf("Classify() = %q, want %q", got, killmail.Loss)
	}
}

func TestFriendliesEmpty(t *testing.T) {
	tests := []struct {
		name        string
		corpIDs     []int64
		allianceIDs []int64
		want        bool
	}{
		{name: "zero value", want: true},
		{name: "nil slices", corpIDs: nil, allianceIDs: nil, want: true},
		{name: "only non-positive IDs", corpIDs: []int64{0}, allianceIDs: []int64{-7}, want: true},
		{name: "a corp", corpIDs: []int64{friendlyCorp}},
		{name: "an alliance", allianceIDs: []int64{friendlyAlliance}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := killmail.NewFriendlies(tc.corpIDs, tc.allianceIDs).Empty(); got != tc.want {
				t.Errorf("Empty() = %v, want %v", got, tc.want)
			}
		})
	}
	var zero killmail.Friendlies
	if !zero.Empty() {
		t.Error("zero Friendlies: Empty() = false, want true")
	}
	if got := zero.Classify(killmail.Killmail{Victim: killmail.Party{CorpID: friendlyCorp}}); got != killmail.Neutral {
		t.Errorf("zero Friendlies: Classify() = %q, want %q", got, killmail.Neutral)
	}
}
