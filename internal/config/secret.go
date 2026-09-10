package config

// Secret wraps a credential so it cannot be printed, logged, or formatted by
// accident. Every path that turns a value into text — fmt's %s/%v/%+v,
// slog's structured output, a struct dump in a panic — goes through String,
// GoString, or LogValue, all of which redact. Call Reveal only at the point
// the raw value is actually needed (e.g. signing a transaction).
type Secret string

// String implements fmt.Stringer, redacting the value for %s and %v.
func (s Secret) String() string { return "[REDACTED]" }

// GoString implements fmt.GoStringer, redacting the value for %#v.
func (s Secret) GoString() string { return "[REDACTED]" }

// Reveal returns the underlying secret. Callers must not log or format the
// result; pass it directly to the thing that consumes it (a signer, a file
// write) and let it go out of scope.
func (s Secret) Reveal() string { return string(s) }
