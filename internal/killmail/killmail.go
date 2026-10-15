// Package killmail turns Wanderer `map_kill` payloads into individual
// killmails, classifies each one relative to the operator's friendly
// corporations and alliances, and decides which ones are worth posting.
//
// Wanderer's kill payload is an internal map rather than a versioned schema
// (see openspec/changes/add-killmail-relay-bot/design.md), so decoding is
// deliberately lenient: `killmail_id` is the only field a killmail must have,
// and anything else that is missing, null or of an unexpected type decodes to
// its zero value instead of failing the whole entry.
//
// Duplicate suppression is not part of this package; it lives in the relay
// state, which is what makes it survive restarts.
package killmail

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"time"
)

// Wanderer's SSE event type carrying kills, and the payload types it wraps.
const (
	// EventMapKill is the only event type the relay subscribes to.
	EventMapKill = "map_kill"
	// PayloadKillmailUpdate carries a list of killmails.
	PayloadKillmailUpdate = "killmail_update"
	// PayloadKillCount carries only a per-system counter, so it is ignored.
	PayloadKillCount = "kill_count"
)

// Party is one side of a killmail: the victim, or the pilot who landed the
// final blow. It holds the payload's `victim_*` and `final_blow_*` field
// groups, whose names match field for field.
type Party struct {
	CharID         int64
	CharName       string
	CorpID         int64
	CorpTicker     string
	CorpName       string
	AllianceID     int64
	AllianceTicker string
	AllianceName   string
	ShipTypeID     int64
	ShipName       string
}

// Killmail is one kill out of a `killmail_update` payload. Only ID is
// guaranteed: every other field is its zero value when the payload omitted it,
// so a structure kill simply has an empty Victim character. KillTime is the
// zero time when `kill_time` was absent or unparseable.
//
// The payload carries no attacker list and no system name; the relay resolves
// the name from SolarSystemID.
type Killmail struct {
	ID            int64
	KillTime      time.Time
	SolarSystemID int64
	Victim        Party
	FinalBlow     Party
	AttackerCount int
	TotalValue    float64
	NPC           bool
}

// Extract decodes a `map_kill` payload and returns its killmails in payload
// order. Payloads of any type other than PayloadKillmailUpdate, kill counts
// included, yield no killmails. An entry without an integer `killmail_id` is
// skipped with a warning, and the remaining entries are still returned.
func Extract(payload []byte, log *slog.Logger) []Killmail {
	log = orDefault(log)

	var p struct {
		Type      string            `json:"type"`
		Killmails []json.RawMessage `json:"killmails"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		log.Warn("ignoring unparseable map_kill payload", "error", err)
		return nil
	}
	if p.Type != PayloadKillmailUpdate {
		log.Debug("ignoring map_kill payload", "payload_type", p.Type)
		return nil
	}

	kills := make([]Killmail, 0, len(p.Killmails))
	for i, raw := range p.Killmails {
		k, ok := decode(raw, i, log)
		if !ok {
			continue
		}
		kills = append(kills, k)
	}
	return kills
}

// decode reads one entry of a payload's `killmails` list. index only names the
// entry in the warning, since a skipped entry has no usable ID to log.
func decode(raw json.RawMessage, index int, log *slog.Logger) (Killmail, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		log.Warn("skipping killmail that is not a JSON object", "index", index, "error", err)
		return Killmail{}, false
	}

	id := fieldInt(fields, "killmail_id")
	if id <= 0 {
		log.Warn("skipping killmail without an integer killmail_id", "index", index,
			"killmail_id", string(fields["killmail_id"]))
		return Killmail{}, false
	}

	return Killmail{
		ID:            id,
		KillTime:      fieldTime(fields, "kill_time"),
		SolarSystemID: fieldInt(fields, "solar_system_id"),
		Victim:        party(fields, "victim"),
		FinalBlow:     party(fields, "final_blow"),
		AttackerCount: int(fieldInt(fields, "attacker_count")),
		TotalValue:    fieldFloat(fields, "total_value"),
		NPC:           fieldBool(fields, "npc"),
	}, true
}

func party(fields map[string]json.RawMessage, prefix string) Party {
	return Party{
		CharID:         fieldInt(fields, prefix+"_char_id"),
		CharName:       fieldString(fields, prefix+"_char_name"),
		CorpID:         fieldInt(fields, prefix+"_corp_id"),
		CorpTicker:     fieldString(fields, prefix+"_corp_ticker"),
		CorpName:       fieldString(fields, prefix+"_corp_name"),
		AllianceID:     fieldInt(fields, prefix+"_alliance_id"),
		AllianceTicker: fieldString(fields, prefix+"_alliance_ticker"),
		AllianceName:   fieldString(fields, prefix+"_alliance_name"),
		ShipTypeID:     fieldInt(fields, prefix+"_ship_type_id"),
		ShipName:       fieldString(fields, prefix+"_ship_name"),
	}
}

// killTimeLayouts are tried in order. Wanderer passes zKillboard's RFC 3339
// timestamps through, but the payload is not a schema, so the naive variants
// are accepted as UTC rather than dropping the time.
var killTimeLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05",
}

// fieldTime reads a timestamp field, returning the zero time when it is
// missing or in none of the known layouts. The max-age filter keeps killmails
// whose time is unknown.
func fieldTime(fields map[string]json.RawMessage, key string) time.Time {
	s := fieldString(fields, key)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range killTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func fieldString(fields map[string]json.RawMessage, key string) string {
	var s string
	if err := json.Unmarshal(fields[key], &s); err != nil {
		return ""
	}
	return s
}

// fieldInt reads an integer field. A JSON string holding a number is accepted,
// as is a float with no fractional part; anything else, a genuine fraction
// included, reads as 0.
func fieldInt(fields map[string]json.RawMessage, key string) int64 {
	n, ok := fieldNumber(fields, key)
	if !ok {
		return 0
	}
	if i, err := strconv.ParseInt(n, 10, 64); err == nil {
		return i
	}
	f, err := strconv.ParseFloat(n, 64)
	if err != nil || f != float64(int64(f)) {
		return 0
	}
	return int64(f)
}

func fieldFloat(fields map[string]json.RawMessage, key string) float64 {
	n, ok := fieldNumber(fields, key)
	if !ok {
		return 0
	}
	f, err := strconv.ParseFloat(n, 64)
	if err != nil {
		return 0
	}
	return f
}

// fieldNumber returns the field's numeric text. encoding/json decodes both a
// JSON number and a JSON string holding one into json.Number, which is the
// leniency we want here.
func fieldNumber(fields map[string]json.RawMessage, key string) (string, bool) {
	var n json.Number
	if err := json.Unmarshal(fields[key], &n); err != nil || n == "" {
		return "", false
	}
	return n.String(), true
}

// fieldBool reads a boolean field, also accepting the strings and the 0/1
// numbers strconv.ParseBool understands.
func fieldBool(fields map[string]json.RawMessage, key string) bool {
	var b bool
	if err := json.Unmarshal(fields[key], &b); err == nil {
		return b
	}
	n, ok := fieldNumber(fields, key)
	if !ok {
		n = fieldString(fields, key)
	}
	v, err := strconv.ParseBool(n)
	return err == nil && v
}

func orDefault(log *slog.Logger) *slog.Logger {
	if log == nil {
		return slog.Default()
	}
	return log
}
