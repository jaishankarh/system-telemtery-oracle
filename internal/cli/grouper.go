package cli

import "github.com/jaishankarh/system-telemtery-oracle/internal/grouping"

// newGrouper builds the Grouper with the built-in layers and the user's rules.
func newGrouper(env Env) *grouping.Grouper {
	return grouping.Configured(env.Machine, env.grouping)
}
