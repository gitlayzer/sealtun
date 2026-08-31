package cmd

import "time"

// watchOptions drives the shared watch loop used by `list --watch` and
// `inspect --watch`.
type watchOptions struct {
	JSON     bool
	Interval time.Duration
	Count    int
}
