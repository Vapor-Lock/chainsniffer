package killmail

import "time"

// The names of the filters in the chain. Allow returns one of them for every
// killmail it drops, so the relay can log which filter made the decision.
const (
	FilterUnlinkedSystem = "unlinked_system"
	FilterKSpaceSecurity = "kspace_security"
	FilterNPC            = "npc"
	FilterMinValue       = "min_value"
	FilterFriendlyOnly   = "friendly_only"
	FilterMaxKillAge     = "max_kill_age"
)

// Filters is the chain deciding which killmails are worth posting. The zero
// value posts every killmail outside known space and drops the rest; the
// relay builds one from the configuration, whose defaults also exclude NPC
// kills and drop kills older than an hour.
type Filters struct {
	// Linked drops killmails whose solar system it reports as not linked to
	// the home system. Nil disables the filter. A killmail whose system is
	// unknown (SolarSystemID 0) is never linked, whatever Linked says. The
	// relay wires in the topology graph, which answers as of the moment the
	// killmail is processed, not as of the kill.
	Linked func(systemID int64) bool

	// KSpace is the set of security classes whose known-space killmails are
	// posted; a known-space killmail in any other class is dropped, so an
	// empty set drops them all. Killmails in wormhole space, or whose system
	// is unknown, pass whatever it holds.
	KSpace map[SecurityClass]bool

	// Security returns the security status of a known-space system, or false
	// when it cannot be resolved, in which case the killmail passes. It is
	// asked only when KSpace holds some but not all of the classes. Nil
	// counts as never resolving.
	Security func(systemID int64) (float64, bool)

	// ExcludeNPC drops killmails the payload flags as NPC kills.
	ExcludeNPC bool

	// MinValue drops killmails whose total value in ISK is below it. A
	// killmail whose payload has no `total_value` counts as 0, and a killmail
	// exactly at the threshold passes.
	MinValue float64

	// FriendlyOnly drops killmails classified as Neutral.
	FriendlyOnly bool

	// MaxKillAge drops killmails whose kill time is further in the past than
	// this, so the historical kills Wanderer replays when a system is added to
	// the map are not posted. Zero or negative disables the filter. A killmail
	// whose kill time is missing or unparseable is always kept.
	MaxKillAge time.Duration

	// Now is the clock the age filter reads. Nil means time.Now.
	Now func() time.Time
}

// Allow reports whether k, classified as c, should be posted. When it returns
// false, the second result names the filter that dropped it.
func (f Filters) Allow(k Killmail, c Classification) (bool, string) {
	if f.Linked != nil && (k.SolarSystemID == 0 || !f.Linked(k.SolarSystemID)) {
		return false, FilterUnlinkedSystem
	}
	if !f.allowKSpace(k.SolarSystemID) {
		return false, FilterKSpaceSecurity
	}
	if f.ExcludeNPC && k.NPC {
		return false, FilterNPC
	}
	if k.TotalValue < f.MinValue {
		return false, FilterMinValue
	}
	if f.FriendlyOnly && c == Neutral {
		return false, FilterFriendlyOnly
	}
	if f.MaxKillAge > 0 && !k.KillTime.IsZero() && f.now().Sub(k.KillTime) > f.MaxKillAge {
		return false, FilterMaxKillAge
	}
	return true, ""
}

// allowKSpace applies the k-space security filter to a killmail in systemID.
func (f Filters) allowKSpace(systemID int64) bool {
	if !IsKSpace(systemID) {
		return true
	}
	configured := 0
	for _, c := range SecurityClasses {
		if f.KSpace[c] {
			configured++
		}
	}
	switch configured {
	case 0:
		return false
	case len(SecurityClasses):
		return true
	}
	if f.Security == nil {
		return true
	}
	security, ok := f.Security(systemID)
	if !ok {
		return true
	}
	return f.KSpace[ClassOf(security)]
}

func (f Filters) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}
