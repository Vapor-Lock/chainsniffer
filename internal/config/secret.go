package config

import "log/slog"

// redacted is what a Secret shows instead of itself.
const redacted = "[redacted]"

// Secret is a configuration value that must never reach a log line, an error
// message or a stack dump. Its String, GoString and LogValue methods all report
// it as redacted, so the easy path — printing a Secret, or logging the whole
// Config — is also the safe one, and revealing the value takes a deliberate
// call to Reveal.
type Secret string

// String implements fmt.Stringer, covering the %v, %s and %q verbs.
func (s Secret) String() string { return redacted }

// GoString implements fmt.GoStringer, covering the %#v verb.
func (s Secret) GoString() string { return redacted }

// LogValue implements slog.LogValuer, covering every slog handler.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// Reveal returns the secret itself. Call it only where the value is used, such
// as when building an Authorization header, and never store the result.
func (s Secret) Reveal() string { return string(s) }

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool { return s == "" }
