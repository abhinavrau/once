package version

import (
	"cmp"
	"regexp"
	"strconv"
)

var Version = "dev"

// IsRelease reports whether v is exactly a release tag (vMAJOR.MINOR.PATCH).
// Local builds report "dev" or a `git describe` string such as
// "v1.2.3-5-gabc1234" or "v1.2.3-dirty", which are not releases.
func IsRelease(v string) bool {
	_, ok := parseRelease(v)
	return ok
}

// Helpers

var releasePattern = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

func parseRelease(v string) ([3]int, bool) {
	var parts [3]int
	m := releasePattern.FindStringSubmatch(v)
	if m == nil {
		return parts, false
	}
	for i := range parts {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}

func compareReleases(a, b [3]int) int {
	for i := range a {
		if c := cmp.Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}
