package config_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Vapor-Lock/chainsniffer/internal/config"
)

// Distinctive token values, so a test can assert that no log line or error
// message contains them.
const (
	wandererToken = "wanderer-public-api-key"
	discordToken  = "discord-bot-token"
)

// baseEnv is the minimal configuration: the required variables and nothing
// else, so every other setting falls back to its default.
func baseEnv() map[string]string {
	return map[string]string{
		config.EnvWandererURL:      "https://wanderer.example",
		config.EnvWandererMap:      "chain",
		config.EnvWandererToken:    wandererToken,
		config.EnvDiscordToken:     discordToken,
		config.EnvDiscordChannelID: "1234567890123456789",
	}
}

func lookup(env map[string]string) config.LookupFunc {
	return func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
}

// secretFile writes content to a file and returns its path, standing in for a
// Docker or Kubernetes secret mount.
func secretFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	return path
}

func TestLoadMinimal(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(lookup(baseEnv()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := config.Config{
		WandererURL:      "https://wanderer.example",
		WandererMap:      "chain",
		WandererToken:    wandererToken,
		DiscordToken:     discordToken,
		DiscordChannelID: "1234567890123456789",
		ExcludeNPC:       true,
		MinValue:         0,
		MaxKillAge:       time.Hour,
		// No k-space classes: no kill in known space is relayed.
		KSpace: nil,
		// No home system: the topology is neither loaded nor followed, but
		// the resync interval still carries its default.
		HomeSystemID:           0,
		TopologyResyncInterval: 15 * time.Minute,
		StateDir:               "/var/lib/chainsniffer",
		LogLevel:               slog.LevelInfo,
		LogFormat:              config.LogFormatText,
		CardStyle:              config.CardStyleComponents,
	}
	// want leaves both friendly ID slices nil, which is what no configured
	// friendly IDs has to produce.
	if !reflect.DeepEqual(*cfg, want) {
		t.Errorf("Load() = %+v, want %+v", *cfg, want)
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, env map[string]string)
		check func(t *testing.T, cfg *config.Config)
	}{
		{
			name: "replay channel",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvReplayChannelID] = " 1560738628290875472 "
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.ReplayChannelID != "1560738628290875472" {
					t.Errorf("ReplayChannelID = %q", cfg.ReplayChannelID)
				}
			},
		},
		{
			name: "trailing slashes stripped from the Wanderer URL",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvWandererURL] = "http://localhost:8080//"
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.WandererURL != "http://localhost:8080" {
					t.Errorf("WandererURL = %q", cfg.WandererURL)
				}
			},
		},
		{
			name: "values are trimmed",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvWandererMap] = "  chain-map \n"
				env[config.EnvWandererToken] = "\t" + wandererToken + "\n"
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.WandererMap != "chain-map" {
					t.Errorf("WandererMap = %q", cfg.WandererMap)
				}
				if cfg.WandererToken.Reveal() != wandererToken {
					t.Errorf("WandererToken = %q", cfg.WandererToken.Reveal())
				}
			},
		},
		{
			name: "blank optional variable falls back to its default",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvStateDir] = "   "
				env[config.EnvLogLevel] = ""
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.StateDir != config.DefaultStateDir {
					t.Errorf("StateDir = %q, want the default", cfg.StateDir)
				}
				if cfg.LogLevel != slog.LevelInfo {
					t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
				}
			},
		},
		{
			name: "custom state directory",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvStateDir] = "/data"
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.StateDir != "/data" {
					t.Errorf("StateDir = %q, want /data", cfg.StateDir)
				}
			},
		},
		{
			name: "friendly IDs with spaces",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvFriendlyCorpIDs] = "98000001, 98000002"
				env[config.EnvFriendlyAllianceIDs] = " 99000001 ,,99000001, 99000002,"
			},
			check: func(t *testing.T, cfg *config.Config) {
				if want := []int64{98000001, 98000002}; !slices.Equal(cfg.FriendlyCorpIDs, want) {
					t.Errorf("FriendlyCorpIDs = %v, want %v", cfg.FriendlyCorpIDs, want)
				}
				// Empty entries are ignored and duplicates collapsed.
				if want := []int64{99000001, 99000002}; !slices.Equal(cfg.FriendlyAllianceIDs, want) {
					t.Errorf("FriendlyAllianceIDs = %v, want %v", cfg.FriendlyAllianceIDs, want)
				}
			},
		},
		{
			name: "filters configured",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvFriendlyCorpIDs] = "98000001"
				env[config.EnvFriendlyOnly] = "true"
				env[config.EnvExcludeNPC] = "false"
				env[config.EnvMinValue] = "10000000"
				env[config.EnvMaxKillAge] = "90m"
			},
			check: func(t *testing.T, cfg *config.Config) {
				if !cfg.FriendlyOnly {
					t.Error("FriendlyOnly = false, want true")
				}
				if cfg.ExcludeNPC {
					t.Error("ExcludeNPC = true, want false")
				}
				if cfg.MinValue != 10_000_000 {
					t.Errorf("MinValue = %v, want 1e7", cfg.MinValue)
				}
				if cfg.MaxKillAge != 90*time.Minute {
					t.Errorf("MaxKillAge = %v, want 90m", cfg.MaxKillAge)
				}
			},
		},
		{
			name: "home system and resync interval",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvHomeSystemID] = "31000727"
				env[config.EnvTopologyResyncInterval] = "5m"
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.HomeSystemID != 31000727 {
					t.Errorf("HomeSystemID = %d, want 31000727", cfg.HomeSystemID)
				}
				if cfg.TopologyResyncInterval != 5*time.Minute {
					t.Errorf("TopologyResyncInterval = %v, want 5m", cfg.TopologyResyncInterval)
				}
			},
		},
		{
			name: "home system with the default resync interval",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvHomeSystemID] = " 31000727\n"
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.HomeSystemID != 31000727 {
					t.Errorf("HomeSystemID = %d, want 31000727", cfg.HomeSystemID)
				}
				if cfg.TopologyResyncInterval != config.DefaultTopologyResyncInterval {
					t.Errorf("TopologyResyncInterval = %v, want the default", cfg.TopologyResyncInterval)
				}
			},
		},
		{
			name: "blank home system is unset",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvHomeSystemID] = ""
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.HomeSystemID != 0 {
					t.Errorf("HomeSystemID = %d, want 0 (unset)", cfg.HomeSystemID)
				}
			},
		},
		{
			name: "ISK in exponent notation",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvMinValue] = "1.5e9"
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.MinValue != 1.5e9 {
					t.Errorf("MinValue = %v, want 1.5e9", cfg.MinValue)
				}
			},
		},
		{
			name: "booleans accept 1 and 0",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvExcludeNPC] = "0"
				env[config.EnvFriendlyCorpIDs] = "98000001"
				env[config.EnvFriendlyOnly] = "1"
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.ExcludeNPC || !cfg.FriendlyOnly {
					t.Errorf("ExcludeNPC = %v, FriendlyOnly = %v; want false, true", cfg.ExcludeNPC, cfg.FriendlyOnly)
				}
			},
		},
		{
			name: "log level and format are case insensitive",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvLogLevel] = "DEBUG"
				env[config.EnvLogFormat] = "JSON"
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.LogLevel != slog.LevelDebug {
					t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
				}
				if cfg.LogFormat != config.LogFormatJSON {
					t.Errorf("LogFormat = %q, want json", cfg.LogFormat)
				}
			},
		},
		{
			name: "k-space classes are trimmed, case insensitive and ordered",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvKSpace] = "Null, low"
			},
			check: func(t *testing.T, cfg *config.Config) {
				want := []config.SecurityClass{config.SecurityLow, config.SecurityNull}
				if !slices.Equal(cfg.KSpace, want) {
					t.Errorf("KSpace = %v, want %v", cfg.KSpace, want)
				}
			},
		},
		{
			name: "k-space duplicates collapse",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvKSpace] = "HIGH,high"
			},
			check: func(t *testing.T, cfg *config.Config) {
				want := []config.SecurityClass{config.SecurityHigh}
				if !slices.Equal(cfg.KSpace, want) {
					t.Errorf("KSpace = %v, want %v", cfg.KSpace, want)
				}
			},
		},
		{
			name: "k-space none",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvKSpace] = " None "
			},
			check: func(t *testing.T, cfg *config.Config) {
				if len(cfg.KSpace) != 0 {
					t.Errorf("KSpace = %v, want none", cfg.KSpace)
				}
			},
		},
		{
			name: "k-space every class",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvKSpace] = "high,low,null"
			},
			check: func(t *testing.T, cfg *config.Config) {
				want := []config.SecurityClass{config.SecurityHigh, config.SecurityLow, config.SecurityNull}
				if !slices.Equal(cfg.KSpace, want) {
					t.Errorf("KSpace = %v, want %v", cfg.KSpace, want)
				}
			},
		},
		{
			name: "card style is case insensitive",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvCardStyle] = "Fields"
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.CardStyle != config.CardStyleFields {
					t.Errorf("CardStyle = %q, want fields", cfg.CardStyle)
				}
			},
		},
		{
			name: "strip card style",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvCardStyle] = "strip"
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.CardStyle != config.CardStyleStrip {
					t.Errorf("CardStyle = %q, want strip", cfg.CardStyle)
				}
			},
		},
		{
			name: "components card style",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvCardStyle] = "Components"
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.CardStyle != config.CardStyleComponents {
					t.Errorf("CardStyle = %q, want components", cfg.CardStyle)
				}
			},
		},
		{
			name: "every log level",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvLogLevel] = "warn"
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.LogLevel != slog.LevelWarn {
					t.Errorf("LogLevel = %v, want warn", cfg.LogLevel)
				}
			},
		},
		{
			name: "secret from a file, trailing newline trimmed",
			setup: func(t *testing.T, env map[string]string) {
				delete(env, config.EnvDiscordToken)
				env[config.EnvDiscordToken+config.FileSuffix] = secretFile(t, " "+discordToken+"\n")
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.DiscordToken.Reveal() != discordToken {
					t.Errorf("DiscordToken = %q, want the file's contents trimmed", cfg.DiscordToken.Reveal())
				}
			},
		},
		{
			name: "both secrets from files",
			setup: func(t *testing.T, env map[string]string) {
				delete(env, config.EnvWandererToken)
				delete(env, config.EnvDiscordToken)
				env[config.EnvWandererToken+config.FileSuffix] = secretFile(t, wandererToken+"\n")
				env[config.EnvDiscordToken+config.FileSuffix] = secretFile(t, discordToken+"\n")
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.WandererToken.Reveal() != wandererToken || cfg.DiscordToken.Reveal() != discordToken {
					t.Error("tokens were not read from their files")
				}
			},
		},
		{
			name: "blank secret variable alongside its file variant",
			setup: func(t *testing.T, env map[string]string) {
				// Compose passes an empty value through for a variable the
				// operator left out; that must not count as "both set".
				env[config.EnvDiscordToken] = ""
				env[config.EnvDiscordToken+config.FileSuffix] = secretFile(t, discordToken)
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.DiscordToken.Reveal() != discordToken {
					t.Errorf("DiscordToken = %q, want the file's contents", cfg.DiscordToken.Reveal())
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := baseEnv()
			tt.setup(t, env)

			cfg, err := config.Load(lookup(env))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			tt.check(t, cfg)
		})
	}
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// setup mutates the minimal environment; it may delete keys from it.
		setup func(t *testing.T, env map[string]string)
		// want holds substrings the error must contain, one per problem.
		want []string
		// problems is the expected number of collected problems.
		problems int
	}{
		{
			name:  "nothing configured names every required variable",
			setup: func(_ *testing.T, env map[string]string) { clear(env) },
			want: []string{
				config.EnvWandererURL,
				config.EnvWandererMap,
				config.EnvWandererToken,
				config.EnvWandererToken + config.FileSuffix,
				config.EnvDiscordToken,
				config.EnvDiscordToken + config.FileSuffix,
				config.EnvDiscordChannelID,
			},
			problems: 5,
		},
		{
			name: "missing channel ID",
			setup: func(_ *testing.T, env map[string]string) {
				delete(env, config.EnvDiscordChannelID)
			},
			want:     []string{config.EnvDiscordChannelID, "required"},
			problems: 1,
		},
		{
			name: "blank required variable",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvWandererMap] = "   "
			},
			want:     []string{config.EnvWandererMap, "required"},
			problems: 1,
		},
		{
			name: "Wanderer URL without a scheme",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvWandererURL] = "wanderer.example"
			},
			want:     []string{config.EnvWandererURL, "wanderer.example"},
			problems: 1,
		},
		{
			name: "Wanderer URL with the wrong scheme",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvWandererURL] = "ftp://wanderer.example"
			},
			want:     []string{config.EnvWandererURL, "http"},
			problems: 1,
		},
		{
			name: "replay channel ID that is not a snowflake",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvReplayChannelID] = "#test"
			},
			want:     []string{config.EnvReplayChannelID, "#test"},
			problems: 1,
		},
		{
			name: "channel ID that is not a snowflake",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvDiscordChannelID] = "#killfeed"
			},
			want:     []string{config.EnvDiscordChannelID, "#killfeed"},
			problems: 1,
		},
		{
			name: "both forms of a secret set",
			setup: func(t *testing.T, env map[string]string) {
				env[config.EnvWandererToken+config.FileSuffix] = secretFile(t, wandererToken)
			},
			want: []string{
				config.EnvWandererToken,
				config.EnvWandererToken + config.FileSuffix,
				"both set",
			},
			problems: 1,
		},
		{
			name: "secret file missing",
			setup: func(t *testing.T, env map[string]string) {
				delete(env, config.EnvDiscordToken)
				env[config.EnvDiscordToken+config.FileSuffix] = filepath.Join(t.TempDir(), "absent")
			},
			want:     []string{config.EnvDiscordToken + config.FileSuffix, "absent"},
			problems: 1,
		},
		{
			name: "secret file empty",
			setup: func(t *testing.T, env map[string]string) {
				delete(env, config.EnvWandererToken)
				env[config.EnvWandererToken+config.FileSuffix] = secretFile(t, "\n\n")
			},
			want:     []string{config.EnvWandererToken + config.FileSuffix, "empty"},
			problems: 1,
		},
		{
			name: "minimum value that is not a number",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvMinValue] = "ten"
			},
			want:     []string{config.EnvMinValue, "ten"},
			problems: 1,
		},
		{
			name: "negative minimum value",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvMinValue] = "-1"
			},
			want:     []string{config.EnvMinValue, "negative"},
			problems: 1,
		},
		{
			name: "non-finite minimum value",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvMinValue] = "NaN"
			},
			want:     []string{config.EnvMinValue, "finite"},
			problems: 1,
		},
		{
			name: "friendly corp IDs with a non-integer",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvFriendlyCorpIDs] = "98000001, Karma Fleet, 98000002"
			},
			want:     []string{config.EnvFriendlyCorpIDs, "Karma Fleet", "integer"},
			problems: 1,
		},
		{
			name: "friendly alliance IDs with two non-integers",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvFriendlyAllianceIDs] = "x,99000001,y"
			},
			want:     []string{`"x"`, `"y"`},
			problems: 2,
		},
		{
			name: "NPC exclusion that is not a boolean",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvExcludeNPC] = "maybe"
			},
			want:     []string{config.EnvExcludeNPC, "boolean"},
			problems: 1,
		},
		{
			name: "kill age that is not a duration",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvMaxKillAge] = "an hour"
			},
			want:     []string{config.EnvMaxKillAge, "duration"},
			problems: 1,
		},
		{
			name: "non-positive kill age",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvMaxKillAge] = "-1h"
			},
			want:     []string{config.EnvMaxKillAge, "positive"},
			problems: 1,
		},
		{
			name: "home system that is a name, not an ID",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvHomeSystemID] = "J142845"
			},
			want:     []string{config.EnvHomeSystemID, "J142845"},
			problems: 1,
		},
		{
			name: "home system zero",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvHomeSystemID] = "0"
			},
			want:     []string{config.EnvHomeSystemID, "positive"},
			problems: 1,
		},
		{
			name: "negative home system",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvHomeSystemID] = "-31000727"
			},
			want:     []string{config.EnvHomeSystemID, "positive"},
			problems: 1,
		},
		{
			name: "resync interval that is not a duration",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvTopologyResyncInterval] = "soon"
			},
			want:     []string{config.EnvTopologyResyncInterval, "soon", "duration"},
			problems: 1,
		},
		{
			name: "zero resync interval",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvTopologyResyncInterval] = "0"
			},
			want:     []string{config.EnvTopologyResyncInterval, "positive"},
			problems: 1,
		},
		{
			name: "negative resync interval",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvHomeSystemID] = "31000727"
				env[config.EnvTopologyResyncInterval] = "-15m"
			},
			want:     []string{config.EnvTopologyResyncInterval, "positive"},
			problems: 1,
		},
		{
			name: "unknown log level",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvLogLevel] = "loud"
			},
			want:     []string{config.EnvLogLevel, "loud", "debug"},
			problems: 1,
		},
		{
			name: "unknown log format",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvLogFormat] = "yaml"
			},
			want:     []string{config.EnvLogFormat, "yaml", "text", "json"},
			problems: 1,
		},
		{
			name: "unknown card style",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvCardStyle] = "table"
			},
			want:     []string{config.EnvCardStyle, "table", "block", "fields", "strip", "components"},
			problems: 1,
		},
		{
			name: "unknown k-space class",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvKSpace] = "low,wormhole"
			},
			want:     []string{config.EnvKSpace, "wormhole", "high", "low", "null", "none"},
			problems: 1,
		},
		{
			name: "k-space none with a class",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvKSpace] = "none,high"
			},
			want:     []string{config.EnvKSpace, "none,high"},
			problems: 1,
		},
		{
			name: "friendly-only without friendly IDs",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvFriendlyOnly] = "true"
			},
			want: []string{
				config.EnvFriendlyOnly,
				config.EnvFriendlyCorpIDs,
				config.EnvFriendlyAllianceIDs,
			},
			problems: 1,
		},
		{
			name: "friendly-only with only unparseable friendly IDs",
			setup: func(_ *testing.T, env map[string]string) {
				env[config.EnvFriendlyOnly] = "true"
				env[config.EnvFriendlyCorpIDs] = "nope"
			},
			want:     []string{config.EnvFriendlyCorpIDs, config.EnvFriendlyOnly},
			problems: 2,
		},
		{
			name: "every problem is reported at once",
			setup: func(_ *testing.T, env map[string]string) {
				delete(env, config.EnvDiscordChannelID)
				env[config.EnvMinValue] = "ten"
				env[config.EnvLogLevel] = "loud"
				env[config.EnvFriendlyCorpIDs] = "abc"
				env[config.EnvHomeSystemID] = "J142845"
				env[config.EnvTopologyResyncInterval] = "soon"
			},
			want: []string{
				config.EnvDiscordChannelID,
				config.EnvMinValue,
				config.EnvLogLevel,
				config.EnvFriendlyCorpIDs,
				config.EnvHomeSystemID,
				config.EnvTopologyResyncInterval,
			},
			problems: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := baseEnv()
			tt.setup(t, env)

			cfg, err := config.Load(lookup(env))
			if err == nil {
				t.Fatalf("Load() = %+v, want an error", cfg)
			}
			if cfg != nil {
				t.Errorf("Load() returned a config alongside the error: %+v", cfg)
			}

			msg := err.Error()
			for _, want := range tt.want {
				if !strings.Contains(msg, want) {
					t.Errorf("error %q does not mention %q", msg, want)
				}
			}

			var cfgErr *config.Error
			if !errors.As(err, &cfgErr) {
				t.Fatalf("error is %T, want *config.Error", err)
			}
			if got := len(cfgErr.Problems); got != tt.problems {
				t.Errorf("collected %d problems, want %d: %v", got, tt.problems, cfgErr.Problems)
			}
		})
	}
}

// Errors are the other place a token could leak: a bad value is quoted into
// the message, so a mistyped variable must not drag a secret along.
func TestLoadErrorKeepsSecretsOut(t *testing.T) {
	t.Parallel()

	env := baseEnv()
	env[config.EnvMinValue] = "ten"
	// A plausible operator mistake: the token pasted into the wrong variable.
	env[config.EnvWandererToken+config.FileSuffix] = "/run/secrets/" + wandererToken

	_, err := config.Load(lookup(env))
	if err == nil {
		t.Fatal("Load() = nil error, want an error")
	}
	if strings.Contains(err.Error(), discordToken) {
		t.Errorf("error message leaks the Discord token: %q", err)
	}
}

func TestConfigLogValueRedactsTokens(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(lookup(baseEnv()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	handlers := map[string]func(*bytes.Buffer) slog.Handler{
		"text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
		"json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
	}

	for format, newHandler := range handlers {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			slog.New(newHandler(&buf)).Info("starting", "config", cfg)

			line := buf.String()
			for _, secret := range []string{wandererToken, discordToken} {
				if strings.Contains(line, secret) {
					t.Errorf("log line leaks a token: %q", line)
				}
			}
			// The summary is still useful: every setting is there and the
			// tokens are marked as withheld rather than silently dropped.
			for _, want := range []string{"wanderer.example", "chain", "max_kill_age", "[redacted]"} {
				if !strings.Contains(line, want) {
					t.Errorf("log line %q does not contain %q", line, want)
				}
			}
			if got := strings.Count(line, "[redacted]"); got != 2 {
				t.Errorf("found %d redactions in %q, want 2", got, line)
			}
		})
	}
}

func TestConfigLogValueIncludesTopology(t *testing.T) {
	t.Parallel()

	env := baseEnv()
	env[config.EnvHomeSystemID] = "31000727"
	cfg, err := config.Load(lookup(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("starting", "config", cfg)

	line := buf.String()
	for _, want := range []string{"home_system_id=31000727", "topology_resync_interval=15m0s"} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q does not contain %q", line, want)
		}
	}
}

func TestConfigLogValueIncludesCardStyle(t *testing.T) {
	t.Parallel()

	tests := map[string]string{"": "card_style=components", "strip": "card_style=strip", "block": "card_style=block", "fields": "card_style=fields"}
	for value, want := range tests {
		t.Run(want, func(t *testing.T) {
			t.Parallel()

			env := baseEnv()
			if value != "" {
				env[config.EnvCardStyle] = value
			}
			cfg, err := config.Load(lookup(env))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			var buf bytes.Buffer
			slog.New(slog.NewTextHandler(&buf, nil)).Info("starting", "config", cfg)

			if line := buf.String(); !strings.Contains(line, want) {
				t.Errorf("log line %q does not contain %q", line, want)
			}
		})
	}
}

func TestConfigLogValueIncludesKSpace(t *testing.T) {
	t.Parallel()

	tests := map[string]string{"": "kspace=none", "none": "kspace=none", "null, HIGH": "kspace=high,null"}
	for value, want := range tests {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			env := baseEnv()
			if value != "" {
				env[config.EnvKSpace] = value
			}
			cfg, err := config.Load(lookup(env))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			var buf bytes.Buffer
			slog.New(slog.NewTextHandler(&buf, nil)).Info("starting", "config", cfg)

			if line := buf.String(); !strings.Contains(line, want) {
				t.Errorf("log line %q does not contain %q", line, want)
			}
		})
	}
}

func TestConfigLogValueOmitsUnsetHomeSystem(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(lookup(baseEnv()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("starting", "config", cfg)

	line := buf.String()
	if strings.Contains(line, "home_system_id") {
		t.Errorf("log line %q mentions home_system_id without a home system", line)
	}
	if strings.Contains(line, "replay_channel_id") {
		t.Errorf("log line %q mentions replay_channel_id without a replay channel", line)
	}
	if !strings.Contains(line, "topology_resync_interval=15m0s") {
		t.Errorf("log line %q does not contain the resync interval", line)
	}
}

func TestSecretRedaction(t *testing.T) {
	t.Parallel()

	s := config.Secret("hunter2")

	for _, verb := range []string{"%v", "%s", "%q", "%#v"} {
		if got := fmt.Sprintf(verb, s); strings.Contains(got, "hunter2") {
			t.Errorf("fmt.Sprintf(%q, secret) = %s, want it redacted", verb, got)
		}
	}
	// A Secret inside a struct, which is how it reaches most log lines.
	if got := fmt.Sprintf("%+v", struct{ Token config.Secret }{s}); strings.Contains(got, "hunter2") {
		t.Errorf("printing a struct leaks the secret: %s", got)
	}
	if got := s.String(); got != "[redacted]" {
		t.Errorf("String() = %q", got)
	}
	if got := s.LogValue().String(); got != "[redacted]" {
		t.Errorf("LogValue() = %q", got)
	}
	if got := s.Reveal(); got != "hunter2" {
		t.Errorf("Reveal() = %q, want the secret itself", got)
	}
	if s.IsZero() || !config.Secret("").IsZero() {
		t.Error("IsZero does not report whether the secret is empty")
	}
}

// FromEnv is the one path that reads the real environment.
func TestFromEnv(t *testing.T) {
	for k, v := range baseEnv() {
		t.Setenv(k, v)
	}

	cfg, err := config.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.WandererMap != "chain" {
		t.Errorf("WandererMap = %q, want chain", cfg.WandererMap)
	}
}
