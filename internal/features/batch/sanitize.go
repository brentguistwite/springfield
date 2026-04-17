package batch

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	reUnsafe  = regexp.MustCompile(`[^a-z0-9-]+`)
	reCollapse = regexp.MustCompile(`-+`)
)

// SanitizeID converts a human title or filename into a branch-safe batch ID.
// Output is lowercase, contains only [a-z0-9-], no leading/trailing dashes.
func SanitizeID(raw string) string {
	lower := strings.ToLower(raw)
	safe := reUnsafe.ReplaceAllString(lower, "-")
	collapsed := reCollapse.ReplaceAllString(safe, "-")
	return strings.Trim(collapsed, "-")
}

// UniqueID returns id if it's not in existing, else appends -2, -3, ... until unique.
func UniqueID(id string, existing map[string]struct{}) string {
	if _, ok := existing[id]; !ok {
		return id
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", id, i)
		if _, ok := existing[candidate]; !ok {
			return candidate
		}
	}
}
