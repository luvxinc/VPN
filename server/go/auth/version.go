package auth

import (
	"strconv"
	"strings"
)

// ParseVersion parses "1.2.3" into [3]int{1, 2, 3}.
// Returns [3]int{0, 0, 0} on invalid input.
func ParseVersion(v string) [3]int {
	parts := strings.SplitN(strings.TrimSpace(v), ".", 3)
	var result [3]int
	for i, p := range parts {
		if i >= 3 {
			break
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}
		}
		result[i] = n
	}
	return result
}

// ClientVersionOutdated returns true if clientVer < minVer.
func ClientVersionOutdated(clientVer, minVer [3]int) bool {
	for i := 0; i < 3; i++ {
		if clientVer[i] < minVer[i] {
			return true
		}
		if clientVer[i] > minVer[i] {
			return false
		}
	}
	return false
}

// VersionString formats [3]int as "1.2.3".
func VersionString(v [3]int) string {
	return strconv.Itoa(v[0]) + "." + strconv.Itoa(v[1]) + "." + strconv.Itoa(v[2])
}
