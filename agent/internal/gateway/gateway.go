// Package gateway turns the command a deploy key asked for (SSH's
// SSH_ORIGINAL_COMMAND) into an allowed sagand subcommand. The key's
// authorized_keys entry forces this gateway, so this list is everything the
// key can do: no shell, no scp, no tunnels.
package gateway

import (
	"errors"
	"fmt"
	"strings"
)

var ErrDenied = errors.New("command not allowed")

var allowed = map[string]bool{
	"version": true, "host": true, "deploy": true, "list": true, "logs": true,
	"remove": true, "dev": true, "put": true, "rm": true, "env": true,
}

func Parse(original string) ([]string, error) {
	args, err := Split(original)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDenied, err)
	}
	if len(args) > 0 && (args[0] == "sagand" || args[0] == "/usr/local/bin/sagand") {
		args = args[1:]
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("%w: interactive sessions are disabled for this key", ErrDenied)
	}
	if !allowed[args[0]] {
		return nil, fmt.Errorf("%w: %q", ErrDenied, args[0])
	}
	return args, nil
}

// Split breaks s into words like a minimal POSIX shell, without expansions.
func Split(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; ch {
		case ' ', '\t', '\n':
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		case '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("unterminated single quote")
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errors.New("unterminated double quote")
			}
			inWord = true
		case '\\':
			if i+1 >= len(s) {
				return nil, errors.New("trailing backslash")
			}
			i++
			cur.WriteByte(s[i])
			inWord = true
		default:
			cur.WriteByte(ch)
			inWord = true
		}
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args, nil
}
