// Package validate holds the input rules the agent enforces on everything it
// receives from the CLI. The agent never trusts the client.
package validate

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// ErrInvalid is wrapped by every validation error.
var ErrInvalid = errors.New("invalid input")

var (
	nameRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)
	labelRe  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func invalid(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, fmt.Sprintf(format, args...))
}

// Name validates project and workspace names.
func Name(field, s string) error {
	if !nameRe.MatchString(s) {
		return invalid(field, "%q must be 1-40 characters of a-z, 0-9 and '-', not starting or ending with '-'", s)
	}
	return nil
}

// Domain validates a hostname such as "api.example.com".
func Domain(field, s string) error {
	if len(s) == 0 || len(s) > 253 {
		return invalid(field, "%q must be 1-253 characters", s)
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return invalid(field, "%q must have at least two labels", s)
	}
	for _, l := range labels {
		if !labelRe.MatchString(l) {
			return invalid(field, "%q has an invalid label %q", s, l)
		}
	}
	return nil
}

// Port validates a TCP port number.
func Port(field string, p int) error {
	if p < 1 || p > 65535 {
		return invalid(field, "%d is not a valid port", p)
	}
	return nil
}

// HealthPath validates an optional HTTP path such as "/health".
func HealthPath(field, s string) error {
	if s == "" {
		return nil
	}
	control := strings.IndexFunc(s, func(r rune) bool { return r <= ' ' || r == 0x7f }) >= 0
	if !strings.HasPrefix(s, "/") || len(s) > 200 || control {
		return invalid(field, "%q must start with '/', contain no spaces and be at most 200 characters", s)
	}
	return nil
}

// EnvKey validates an environment variable name.
func EnvKey(field, s string) error {
	if !envKeyRe.MatchString(s) {
		return invalid(field, "%q is not a valid environment variable name", s)
	}
	return nil
}

// RelPath validates a clean, relative, slash-separated path that stays inside
// its root: no leading '/', no '..', no '.' segments and no NUL bytes.
func RelPath(field, s string) error {
	if s == "" || strings.HasPrefix(s, "/") || strings.ContainsRune(s, 0) {
		return invalid(field, "%q is not a relative path", s)
	}
	c := path.Clean(s)
	if c != s || c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return invalid(field, "%q must be a clean path inside the project", s)
	}
	return nil
}
