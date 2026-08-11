package storage

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that survives project.yaml in BOTH directions as
// the string a human writes ("90m").
//
// A plain time.Duration field would not. yaml.v3 DECODES a duration string
// fine, but MARSHALS the value as a bare nanosecond integer - and its decoder
// explicitly refuses an integer for a duration field (decode.go's isDuration
// guard). So one writeProject rewrite, even an unrelated one (writeProject
// re-marshals the whole project and keeps the new node whenever the decoded
// value differs), would turn `timeout: 90m` into `timeout: 5400000000000` and
// the NEXT read of that project would fail outright. This is the same class of
// silent round-trip damage PhaseBinding.MarshalYAML exists to prevent
// (pm-cli-55), caught here before it could ship rather than after.
type Duration time.Duration

// Duration returns the underlying time.Duration, so call sites read as what
// they mean (`exc.Timeout.Duration()`) instead of casting.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

// UnmarshalYAML accepts what a person would type: a duration string ("45m",
// "1h30m"). A bare number is refused rather than guessed at - "timeout: 90"
// could be seconds or minutes, and picking one silently is how a project ends up
// with a 90-nanosecond ceiling.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a string like \"90m\", got %q", node.Value)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w (write it like \"45m\" or \"1h30m\")", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalYAML renders the duration back as its own string spelling, which is
// what makes the round trip lossless.
func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }
