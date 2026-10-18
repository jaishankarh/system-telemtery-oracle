package cli_test

import (
	"errors"
	"strings"
)

var errPerm = errors.New("permission denied")

func joinCalls(c [][]string) string {
	parts := make([]string, len(c))
	for i, a := range c {
		parts[i] = strings.Join(a, " ")
	}
	return strings.Join(parts, ";")
}
