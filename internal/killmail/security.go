package killmail

// SecurityClass is the band of security status a known-space system falls in.
type SecurityClass string

// The security classes, as CHAINSNIFFER_KSPACE names them.
const (
	HighSec SecurityClass = "highsec"
	LowSec  SecurityClass = "lowsec"
	NullSec SecurityClass = "nullsec"
)

// SecurityClasses lists every class in the order a summary shows them.
var SecurityClasses = []SecurityClass{HighSec, LowSec, NullSec}

// The known-space solar system IDs, Pochven and Zarzakh included. Wormhole
// space (Thera and the shattered and Drifter systems included) starts at
// 31000000, and Abyssal pockets at 32000000.
const (
	firstKSpaceSystem = 30000000
	lastKSpaceSystem  = 30999999
)

// IsKSpace reports whether systemID is a known-space system. An unknown
// system (ID 0) is not.
func IsKSpace(systemID int64) bool {
	return systemID >= firstKSpaceSystem && systemID <= lastKSpaceSystem
}

// ClassOf returns the class of a known-space system with the given security
// status: high-sec at 0.45 and up, where the game's display rounds to 0.5;
// low-sec above 0.0, which shows as at least 0.1; null-sec at 0.0 and below,
// Pochven and Zarzakh's -1.0 included.
func ClassOf(security float64) SecurityClass {
	switch {
	case security >= 0.45:
		return HighSec
	case security > 0.0:
		return LowSec
	default:
		return NullSec
	}
}
