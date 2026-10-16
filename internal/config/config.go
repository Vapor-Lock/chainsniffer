// Package config reads the relay's settings from the environment: every
// CHAINSNIFFER_* variable, its default, and the validation that stops a
// misconfigured relay before it connects anywhere.
//
// Load reports every problem it finds in one error (see [Error]) rather than
// the first one, so an operator fixing a deployment needs one restart instead
// of one per typo. Tokens are read into a [Secret], which redacts itself in
// logs, errors and panics.
package config

import (
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// The environment variables the relay reads. Everything is prefixed, so an
// operator can tell at a glance which variables belong to this process.
const (
	EnvWandererURL      = "CHAINSNIFFER_WANDERER_URL"
	EnvWandererMap      = "CHAINSNIFFER_WANDERER_MAP"
	EnvWandererToken    = "CHAINSNIFFER_WANDERER_TOKEN"
	EnvDiscordToken     = "CHAINSNIFFER_DISCORD_TOKEN"
	EnvDiscordChannelID = "CHAINSNIFFER_DISCORD_CHANNEL_ID"

	EnvFriendlyCorpIDs     = "CHAINSNIFFER_FRIENDLY_CORP_IDS"
	EnvFriendlyAllianceIDs = "CHAINSNIFFER_FRIENDLY_ALLIANCE_IDS"
	EnvFriendlyOnly        = "CHAINSNIFFER_FRIENDLY_ONLY"
	EnvExcludeNPC          = "CHAINSNIFFER_EXCLUDE_NPC"
	EnvMinValue            = "CHAINSNIFFER_MIN_VALUE"
	EnvMaxKillAge          = "CHAINSNIFFER_MAX_KILL_AGE"

	EnvHomeSystemID           = "CHAINSNIFFER_HOME_SYSTEM_ID"
	EnvTopologyResyncInterval = "CHAINSNIFFER_TOPOLOGY_RESYNC_INTERVAL"

	EnvStateDir  = "CHAINSNIFFER_STATE_DIR"
	EnvLogLevel  = "CHAINSNIFFER_LOG_LEVEL"
	EnvLogFormat = "CHAINSNIFFER_LOG_FORMAT"
)

// FileSuffix marks the companion variable that names a file holding a secret's
// value instead of carrying the value itself, the shape Docker and Kubernetes
// secrets arrive in. Setting both forms of the same secret is an error.
const FileSuffix = "_FILE"

// Defaults for the optional settings.
const (
	DefaultStateDir               = "/var/lib/chainsniffer"
	DefaultMaxKillAge             = time.Hour
	DefaultExcludeNPC             = true
	DefaultTopologyResyncInterval = 15 * time.Minute
	DefaultLogLevel               = slog.LevelInfo
	DefaultLogFormat              = LogFormatText
)

// LogFormat selects the slog handler the relay logs through.
type LogFormat string

// The supported log formats.
const (
	LogFormatText LogFormat = "text"
	LogFormatJSON LogFormat = "json"
)

// Config is the relay's effective configuration. It is read once at startup
// and never mutated afterwards.
type Config struct {
	// Wanderer connection. URL is the instance's base URL without a trailing
	// slash; Map is a map ID or slug.
	WandererURL   string
	WandererMap   string
	WandererToken Secret

	// Discord connection. ChannelID is the decimal snowflake of the channel
	// the relay posts to.
	DiscordToken     Secret
	DiscordChannelID string

	// Filters. The friendly ID lists drive classification; both may be empty,
	// in which case every killmail is neutral.
	FriendlyCorpIDs     []int64
	FriendlyAllianceIDs []int64
	FriendlyOnly        bool
	ExcludeNPC          bool
	MinValue            float64 // ISK; a killmail below this is dropped
	MaxKillAge          time.Duration

	// Home chain. HomeSystemID is the EVE solar system ID kills are judged
	// against: a kill in a system with no path to it over the map's
	// connections is dropped. Zero means unset, in which case no kill is
	// dropped for where it happened; the map's topology, and the chain aliases
	// with it, is followed either way. TopologyResyncInterval is how often
	// that topology is reloaded from scratch.
	HomeSystemID           int64
	TopologyResyncInterval time.Duration

	// Runtime.
	StateDir  string
	LogLevel  slog.Level
	LogFormat LogFormat
}

// LookupFunc resolves an environment variable, reporting whether it is set. It
// has the signature of [os.LookupEnv], which is what the relay passes; tests
// pass a map instead.
type LookupFunc func(name string) (value string, ok bool)

// FromEnv loads the configuration from the process environment.
func FromEnv() (*Config, error) { return Load(os.LookupEnv) }

// Load reads the configuration through lookup. A nil lookup reads the process
// environment. On failure it returns an [Error] naming every missing or
// invalid variable, and a nil Config.
func Load(lookup LookupFunc) (*Config, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	l := &loader{lookup: lookup}

	cfg := &Config{
		WandererURL:   l.baseURL(EnvWandererURL),
		WandererMap:   l.required(EnvWandererMap),
		WandererToken: l.secret(EnvWandererToken),

		DiscordToken:     l.secret(EnvDiscordToken),
		DiscordChannelID: l.snowflake(EnvDiscordChannelID),

		FriendlyCorpIDs:     l.idList(EnvFriendlyCorpIDs),
		FriendlyAllianceIDs: l.idList(EnvFriendlyAllianceIDs),
		FriendlyOnly:        l.boolean(EnvFriendlyOnly, false),
		ExcludeNPC:          l.boolean(EnvExcludeNPC, DefaultExcludeNPC),
		MinValue:            l.isk(EnvMinValue),
		MaxKillAge:          l.duration(EnvMaxKillAge, DefaultMaxKillAge),

		HomeSystemID:           l.systemID(EnvHomeSystemID),
		TopologyResyncInterval: l.duration(EnvTopologyResyncInterval, DefaultTopologyResyncInterval),

		StateDir:  l.optional(EnvStateDir, DefaultStateDir),
		LogLevel:  l.level(EnvLogLevel),
		LogFormat: l.logFormat(EnvLogFormat),
	}

	// Friendly-only mode drops every neutral killmail, and without friendly
	// IDs every killmail is neutral: the relay would silently post nothing.
	if cfg.FriendlyOnly && len(cfg.FriendlyCorpIDs)+len(cfg.FriendlyAllianceIDs) == 0 {
		l.errorf("%s is enabled but no friendly IDs are configured: set %s or %s",
			EnvFriendlyOnly, EnvFriendlyCorpIDs, EnvFriendlyAllianceIDs)
	}

	if err := l.err(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LogValue implements slog.LogValuer, so logging a Config writes a summary of
// the effective settings with both tokens redacted. This is the summary
// cmd/chainsniffer logs at startup. The home system is left out when none is
// configured, rather than logged as 0.
func (c *Config) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("wanderer_url", c.WandererURL),
		slog.String("wanderer_map", c.WandererMap),
		slog.String("wanderer_token", c.WandererToken.String()),
		slog.String("discord_token", c.DiscordToken.String()),
		slog.String("discord_channel_id", c.DiscordChannelID),
		slog.Any("friendly_corp_ids", c.FriendlyCorpIDs),
		slog.Any("friendly_alliance_ids", c.FriendlyAllianceIDs),
		slog.Bool("friendly_only", c.FriendlyOnly),
		slog.Bool("exclude_npc", c.ExcludeNPC),
		slog.Float64("min_value", c.MinValue),
		slog.Duration("max_kill_age", c.MaxKillAge),
	}
	if c.HomeSystemID != 0 {
		attrs = append(attrs, slog.Int64("home_system_id", c.HomeSystemID))
	}
	attrs = append(attrs,
		slog.Duration("topology_resync_interval", c.TopologyResyncInterval),
		slog.String("state_dir", c.StateDir),
		slog.String("log_level", c.LogLevel.String()),
		slog.String("log_format", string(c.LogFormat)),
	)
	return slog.GroupValue(attrs...)
}

// Error reports everything wrong with the environment at once.
type Error struct {
	// Problems holds one error per missing or invalid variable, in the order
	// the variables are documented.
	Problems []error
}

// Error implements error, listing every problem.
func (e *Error) Error() string {
	msgs := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		msgs[i] = p.Error()
	}
	return "invalid configuration: " + strings.Join(msgs, "; ")
}

// Unwrap lets errors.Is and errors.As reach every problem.
func (e *Error) Unwrap() []error { return e.Problems }

// loader reads and validates one variable at a time, accumulating problems so
// that a single pass reports all of them.
type loader struct {
	lookup LookupFunc
	errs   []error
}

func (l *loader) errorf(format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf(format, args...))
}

func (l *loader) err() error {
	if len(l.errs) == 0 {
		return nil
	}
	return &Error{Problems: l.errs}
}

// value returns the trimmed value of name. A variable that is set but blank
// counts as unset: compose files and Kubernetes manifests routinely pass empty
// values through for settings the operator left out, and trimming means a
// value pasted with a trailing newline still works.
func (l *loader) value(name string) (string, bool) {
	v, ok := l.lookup(name)
	if !ok {
		return "", false
	}
	if v = strings.TrimSpace(v); v == "" {
		return "", false
	}
	return v, true
}

func (l *loader) required(name string) string {
	v, ok := l.value(name)
	if !ok {
		l.errorf("%s is required", name)
		return ""
	}
	return v
}

func (l *loader) optional(name, def string) string {
	if v, ok := l.value(name); ok {
		return v
	}
	return def
}

// baseURL requires an absolute http(s) URL and strips trailing slashes, so
// callers can join paths onto exactly one form.
func (l *loader) baseURL(name string) string {
	v, ok := l.value(name)
	if !ok {
		l.errorf("%s is required", name)
		return ""
	}
	u, err := url.Parse(v)
	if err != nil {
		l.errorf("%s: %q is not a URL: %v", name, v, err)
		return ""
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		l.errorf("%s: %q is not an absolute http or https URL", name, v)
		return ""
	}
	return strings.TrimRight(v, "/")
}

// snowflake requires a Discord ID, which is a decimal 64-bit snowflake. It
// stays a string because that is what the Discord API takes.
func (l *loader) snowflake(name string) string {
	v, ok := l.value(name)
	if !ok {
		l.errorf("%s is required", name)
		return ""
	}
	if _, err := strconv.ParseUint(v, 10, 64); err != nil {
		l.errorf("%s: %q is not a Discord ID (a decimal snowflake)", name, v)
		return ""
	}
	return v
}

// secret reads a secret from name, or from the file named by its _FILE
// variant. Exactly one of the two forms has to be set.
func (l *loader) secret(name string) Secret {
	fileName := name + FileSuffix
	inline, hasInline := l.value(name)
	path, hasFile := l.value(fileName)

	switch {
	case hasInline && hasFile:
		l.errorf("%s and %s are both set: use one or the other", name, fileName)
	case hasInline:
		return Secret(inline)
	case hasFile:
		// The error carries the path but never the contents.
		b, err := os.ReadFile(path)
		if err != nil {
			l.errorf("%s: %v", fileName, err)
			break
		}
		// A secret mounted from a file usually ends in a newline.
		v := strings.TrimSpace(string(b))
		if v == "" {
			l.errorf("%s: %s is empty", fileName, path)
			break
		}
		return Secret(v)
	default:
		l.errorf("%s (or %s) is required", name, fileName)
	}
	return ""
}

// idList parses a comma-separated list of EVE IDs. Spaces around the entries
// are tolerated, empty entries are ignored and duplicates are collapsed.
func (l *loader) idList(name string) []int64 {
	raw, ok := l.value(name)
	if !ok {
		return nil
	}
	var ids []int64
	seen := make(map[int64]struct{})
	for _, field := range strings.Split(raw, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		id, err := strconv.ParseInt(field, 10, 64)
		if err != nil {
			l.errorf("%s: %q is not an integer ID", name, field)
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func (l *loader) boolean(name string, def bool) bool {
	v, ok := l.value(name)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.errorf("%s: %q is not a boolean (true or false)", name, v)
		return def
	}
	return b
}

// systemID parses an optional EVE solar system ID. Unset returns 0; a value
// that is set has to be a positive integer, because 0 would read as "unset"
// and quietly turn the filter off.
func (l *loader) systemID(name string) int64 {
	v, ok := l.value(name)
	if !ok {
		return 0
	}
	id, err := strconv.ParseInt(v, 10, 64)
	switch {
	case err != nil:
		l.errorf("%s: %q is not a solar system ID (a positive integer)", name, v)
	case id <= 0:
		l.errorf("%s: %q must be a positive solar system ID", name, v)
	default:
		return id
	}
	return 0
}

// isk parses an ISK amount. Values are floats because killmail totals are.
func (l *loader) isk(name string) float64 {
	v, ok := l.value(name)
	if !ok {
		return 0
	}
	f, err := strconv.ParseFloat(v, 64)
	switch {
	case err != nil:
		l.errorf("%s: %q is not an amount of ISK", name, v)
	case math.IsNaN(f) || math.IsInf(f, 0):
		l.errorf("%s: %q is not a finite amount of ISK", name, v)
	case f < 0:
		l.errorf("%s: %q is negative", name, v)
	default:
		return f
	}
	return 0
}

// duration parses a Go duration. Zero and negative values are rejected: as a
// kill age they would drop every killmail, and as a resync interval they would
// reload the topology in a busy loop, neither of which an operator means.
func (l *loader) duration(name string, def time.Duration) time.Duration {
	v, ok := l.value(name)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	switch {
	case err != nil:
		l.errorf("%s: %q is not a duration (for example 90m or 1h30m)", name, v)
	case d <= 0:
		l.errorf("%s: %q must be positive", name, v)
	default:
		return d
	}
	return def
}

func (l *loader) level(name string) slog.Level {
	v, ok := l.value(name)
	if !ok {
		return DefaultLogLevel
	}
	switch strings.ToLower(v) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	l.errorf("%s: %q is not a log level (debug, info, warn or error)", name, v)
	return DefaultLogLevel
}

func (l *loader) logFormat(name string) LogFormat {
	v, ok := l.value(name)
	if !ok {
		return DefaultLogFormat
	}
	switch f := LogFormat(strings.ToLower(v)); f {
	case LogFormatText, LogFormatJSON:
		return f
	}
	l.errorf("%s: %q is not a log format (%s or %s)", name, v, LogFormatText, LogFormatJSON)
	return DefaultLogFormat
}
