package cmd

import "strings"

// regionMatches compares a session-stored region with the active login region
// tolerating cosmetic differences (trailing slashes, surrounding whitespace,
// case) so scope checks don't reject a user's own tunnels after config
// migrations or hand-edited auth files.
func regionMatches(a, b string) bool {
	return normalizeRegionForCompare(a) == normalizeRegionForCompare(b)
}

func normalizeRegionForCompare(region string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(region), "/"))
}
