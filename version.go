package selfupdate

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// semverRe matches SemVer 2.0 strings (with optional v prefix).
// Captures: 1=major, 2=minor, 3=patch, 4=pre-release, 5=build metadata.
var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([a-zA-Z0-9.+-]+))?(?:\+([a-zA-Z0-9.+-]+))?$`)

// CompareVersions compares two semver-like version strings.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
// Pre-release versions have lower precedence than the associated normal
// version (SemVer 2.0 §11): 1.0.0-alpha < 1.0.0.
func CompareVersions(a, b string) int {
	pa, err := parseSemver(a)
	if err != nil {
		pa = fallbackParse(a)
	}
	pb, err := parseSemver(b)
	if err != nil {
		pb = fallbackParse(b)
	}

	// Compare major.minor.patch
	if pa.major != pb.major {
		if pa.major < pb.major {
			return -1
		}
		return 1
	}
	if pa.minor != pb.minor {
		if pa.minor < pb.minor {
			return -1
		}
		return 1
	}
	if pa.patch != pb.patch {
		if pa.patch < pb.patch {
			return -1
		}
		return 1
	}

	// Pre-release comparison (SemVer 2.0 §11.4):
	// A version without pre-release has higher precedence than one with.
	if pa.pre == "" && pb.pre != "" {
		return 1
	}
	if pa.pre != "" && pb.pre == "" {
		return -1
	}
	if pa.pre != "" && pb.pre != "" {
		return comparePreRelease(pa.pre, pb.pre)
	}

	return 0
}

// IsNewer returns true if candidate is strictly greater than current.
func IsNewer(candidate, current string) bool {
	return CompareVersions(candidate, current) > 0
}

// ValidateVersion checks that a version string is a valid SemVer.
// Accepts optional "v" prefix, and 1-3 numeric segments (e.g. "1.0", "1.0.0").
// Pre-release and build metadata are allowed.
func ValidateVersion(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return fmt.Errorf("empty version")
	}

	// Try full SemVer first
	if semverRe.MatchString(v) {
		return nil
	}

	// Allow relaxed 1-2 segment versions like "1" or "1.2"
	v2 := strings.TrimPrefix(v, "v")
	if idx := strings.Index(v2, "+"); idx >= 0 {
		v2 = v2[:idx]
	}
	if idx := strings.Index(v2, "-"); idx >= 0 {
		v2 = v2[:idx]
	}
	parts := strings.Split(v2, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return fmt.Errorf("invalid version %q: expected 1-3 numeric segments", v)
	}
	for _, p := range parts {
		if p == "" {
			return fmt.Errorf("invalid version %q: empty segment", v)
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return fmt.Errorf("invalid version %q: non-numeric segment %q", v, p)
			}
		}
	}
	return nil
}

type semver struct {
	major, minor, patch int
	pre                 string
}

func parseSemver(v string) (semver, error) {
	v = strings.TrimSpace(v)
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		return semver{}, fmt.Errorf("not a semver: %s", v)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	return semver{major: major, minor: minor, patch: patch, pre: m[4]}, nil
}

// fallbackParse handles non-standard versions like "1.0" or "1" (no patch).
func fallbackParse(v string) semver {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if idx := strings.Index(v, "+"); idx >= 0 {
		v = v[:idx]
	}
	var pre string
	if idx := strings.Index(v, "-"); idx >= 0 {
		pre = v[idx+1:]
		v = v[:idx]
	}

	parts := strings.Split(v, ".")
	s := semver{pre: pre}
	if len(parts) >= 1 {
		s.major, _ = strconv.Atoi(parts[0])
	}
	if len(parts) >= 2 {
		s.minor, _ = strconv.Atoi(parts[1])
	}
	if len(parts) >= 3 {
		s.patch, _ = strconv.Atoi(parts[2])
	}
	return s
}

// comparePreRelease compares two pre-release strings per SemVer 2.0 §11.4.
// Numeric identifiers are compared as integers; alphanumeric as strings.
// Numeric identifiers always have lower precedence than alphanumeric.
func comparePreRelease(a, b string) int {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")

	maxLen := len(ap)
	if len(bp) > maxLen {
		maxLen = len(bp)
	}

	for i := 0; i < maxLen; i++ {
		if i >= len(ap) {
			return -1 // shorter pre-release has lower precedence
		}
		if i >= len(bp) {
			return 1
		}

		aNum, aIsNum := strconv.Atoi(ap[i])
		bNum, bIsNum := strconv.Atoi(bp[i])

		if aIsNum == nil && bIsNum == nil {
			// Both numeric: compare as integers
			if aNum != bNum {
				if aNum < bNum {
					return -1
				}
				return 1
			}
			continue
		}

		if aIsNum == nil && bIsNum != nil {
			return -1 // numeric has lower precedence than alphanumeric
		}
		if aIsNum != nil && bIsNum == nil {
			return 1
		}

		// Both alphanumeric: compare lexicographically
		if ap[i] != bp[i] {
			if ap[i] < bp[i] {
				return -1
			}
			return 1
		}
	}

	return 0
}
