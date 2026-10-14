// Package opensips talks to OpenSIPS hosts: opensips-cli over ssh, version-aware MI operations.
package opensips

import (
	"fmt"
	"regexp"
	"strconv"
)

// Version is an OpenSIPS release, e.g. 3.4.7.
type Version struct {
	Major, Minor, Patch int
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// ParseVersion finds the first version in s: "OpenSIPS (3.4.3 (x86_64/linux))" from MI version,
// "version: opensips 3.4.3 (x86_64/linux)" from opensips -V.
func ParseVersion(s string) (Version, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, false
	}
	v := Version{}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		v.Patch, _ = strconv.Atoi(m[3])
	}
	return v, true
}

// V parses a version literal used in capability tables.
func V(s string) Version {
	v, ok := ParseVersion(s)
	if !ok {
		panic("bad version literal " + s)
	}
	return v
}

// IsZero reports an unknown version.
func (v Version) IsZero() bool { return v == Version{} }

// Less compares versions.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

// Branch is "3.4".
func (v Version) Branch() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

func (v Version) String() string {
	if v.IsZero() {
		return "?"
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}
