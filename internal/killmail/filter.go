package killmail

import "time"

// The names of the filters in the chain. Allow returns one of them for every
// killmail it drops, so the relay can log which filter made the decision.
const (
	FilterNPC          = "npc"
	FilterMinValue     = "min_value"
	FilterFriendlyOnly = "friendly_only"
	FilterMaxKillAge   = "max_kill_age"
)

// Filters is the chain deciding which killmails are worth posting. The zero
// value posts every killmail; the relay builds one from the configuration,
// whose defaults differ (NPC kills are excluded and kills older than an hour
// are dropped).
type Filters struct {
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

func (f Filters) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}
