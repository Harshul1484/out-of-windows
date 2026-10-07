// Package selfupdate finds the latest published release, verifies the
// downloaded executable against the release's SHA256SUMS file, and replaces
// the installed executable with a rollback path.
//
// It never runs on its own: only an explicit `update` command reaches it.
package selfupdate

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed semantic version (https://semver.org).
type Version struct {
	Major, Minor, Patch int
	Pre                 []string // pre-release identifiers, e.g. ["rc", "1"]
}

// ParseVersion parses "1.2.3", "v1.2.3", "1.2.3-rc.1" or "1.2.3+build".
// Build metadata is ignored, as semver requires for precedence.
func ParseVersion(s string) (Version, error) {
	var v Version
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("%q is not a semantic version (want MAJOR.MINOR.PATCH)", s)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || (len(p) > 1 && p[0] == '0') || strings.ContainsAny(p, "+-") {
			return v, fmt.Errorf("%q is not a semantic version", s)
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	if hasPre {
		if pre == "" {
			return v, fmt.Errorf("%q has an empty pre-release", s)
		}
		for _, id := range strings.Split(pre, ".") {
			if id == "" || strings.Trim(id, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-") != "" {
				return v, fmt.Errorf("%q has an invalid pre-release", s)
			}
			v.Pre = append(v.Pre, id)
		}
	}
	return v, nil
}

// String formats the version without a leading "v".
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

// Compare returns -1, 0 or 1 following semver precedence.
func (v Version) Compare(o Version) int {
	for _, d := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if d[0] != d[1] {
			return cmpInt(d[0], d[1])
		}
	}
	// A version without pre-release has higher precedence.
	switch {
	case len(v.Pre) == 0 && len(o.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(o.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(o.Pre); i++ {
		a, b := v.Pre[i], o.Pre[i]
		an, aerr := strconv.Atoi(a)
		bn, berr := strconv.Atoi(b)
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				return cmpInt(an, bn)
			}
		case aerr == nil: // numeric identifiers sort before alphanumeric ones
			return -1
		case berr == nil:
			return 1
		case a != b:
			return strings.Compare(a, b)
		}
	}
	return cmpInt(len(v.Pre), len(o.Pre))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// IsDevBuild reports whether version belongs to a source build rather than a
// published release: it does not parse, or it carries a "dev" pre-release.
// Such builds cannot tell whether a release is newer, so they do not update
// themselves.
func IsDevBuild(version string) bool {
	v, err := ParseVersion(version)
	if err != nil {
		return true
	}
	for _, id := range v.Pre {
		if strings.EqualFold(id, "dev") {
			return true
		}
	}
	return false
}
