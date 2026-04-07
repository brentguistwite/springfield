package runtime

import (
	"strings"

	"springfield/internal/core/exec"
)

var rateLimitNeedles = []string{
	"rate limit",
	"rate-limit",
	"too many requests",
	"429",
	"quota exceeded",
	"resource exhausted",
}

func IsRateLimitError(err error, events []exec.Event) bool {
	if containsRateLimitText(errorString(err)) {
		return true
	}
	for _, event := range events {
		if containsRateLimitText(event.Data) {
			return true
		}
	}
	return false
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func containsRateLimitText(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return false
	}
	for _, needle := range rateLimitNeedles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
