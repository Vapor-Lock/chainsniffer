package killmail

// Classification says how a killmail relates to the operator's friendly
// corporations and alliances.
type Classification string

const (
	// Loss is a killmail whose victim is friendly.
	Loss Classification = "loss"
	// Kill is a killmail whose final blow is friendly and whose victim is not.
	Kill Classification = "kill"
	// Neutral is a killmail with no friendly party on either side.
	Neutral Classification = "neutral"
)

// Friendlies is the set of corporation and alliance IDs the operator counts as
// their own. The zero value holds none, so it classifies every killmail as
// Neutral.
type Friendlies struct {
	corps     map[int64]struct{}
	alliances map[int64]struct{}
}

// NewFriendlies builds the friendly sets from the configured corporation and
// alliance IDs. IDs that are not positive are dropped, since no EVE entity has
// such an ID and a zero would otherwise match every killmail whose payload
// omitted that field.
func NewFriendlies(corpIDs, allianceIDs []int64) Friendlies {
	return Friendlies{corps: idSet(corpIDs), alliances: idSet(allianceIDs)}
}

func idSet(ids []int64) map[int64]struct{} {
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id > 0 {
			set[id] = struct{}{}
		}
	}
	return set
}

// Empty reports whether no friendly IDs are configured, in which case every
// killmail classifies as Neutral. Startup validation uses it to reject
// friendly-only mode, which would then drop everything.
func (f Friendlies) Empty() bool {
	return len(f.corps) == 0 && len(f.alliances) == 0
}

// Classify classifies k. The victim takes precedence over the final blow, so a
// blue-on-blue kill is a Loss.
//
// Wanderer's payload names only the final blow, not every attacker, so a kill
// friendlies took part in without landing the last shot is Neutral.
func (f Friendlies) Classify(k Killmail) Classification {
	switch {
	case f.contains(k.Victim):
		return Loss
	case f.contains(k.FinalBlow):
		return Kill
	default:
		return Neutral
	}
}

func (f Friendlies) contains(p Party) bool {
	if _, ok := f.corps[p.CorpID]; ok {
		return true
	}
	_, ok := f.alliances[p.AllianceID]
	return ok
}
