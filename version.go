package selfupdate

import (
	"fmt"
	"strings"
)

// CompareVersions compares two semver-like version strings.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func CompareVersions(a, b string) int {
	a = normalizeVersion(a)
	b = normalizeVersion(b)

	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}

	for i := 0; i < maxLen; i++ {
		av := partVal(aParts, i)
		bv := partVal(bParts, i)

		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

// IsNewer returns true if candidate is strictly greater than current.
func IsNewer(candidate, current string) bool {
	return CompareVersions(candidate, current) > 0
}

// ValidateVersion checks that a version string is parseable.
func ValidateVersion(v string) error {
	v = normalizeVersion(v)
	if v == "" {
		return fmt.Errorf("empty version")
	}
	for _, p := range strings.Split(v, ".") {
		if p == "" {
			return fmt.Errorf("invalid version %q: empty segment", v)
		}
	}
	return nil
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	// strip build metadata
	if idx := strings.Index(v, "+"); idx >= 0 {
		v = v[:idx]
	}
	// strip pre-release for simple comparison
	if idx := strings.Index(v, "-"); idx >= 0 {
		v = v[:idx]
	}
	return v
}

func partVal(parts []string, idx int) int {
	if idx >= len(parts) {
		return 0
	}
	var n int
	for _, c := range parts[idx] {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			break
		}
	}
	return n
}
