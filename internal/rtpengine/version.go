package rtpengine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/napadailo/voip-tui/i18n"
)

// Version is an rtpengine release. Sipwise tags releases as mrX.Y.Z.W and the
// Debian package uses the same numbers, so both spellings parse.
type Version struct {
	Major, Minor int
	Full         string // "12.5.1.3" when known
	Raw          string // what it was parsed from
	Source       string // config, rtpengine --version, package, CLI
	// Floor marks a lower bound estimated from available CLI commands.
	Floor bool
}

var (
	mrRe  = regexp.MustCompile(`(?i)mr(\d+)\.(\d+)((?:\.\d+)*)`)
	numRe = regexp.MustCompile(`(?:^|[^\d.])(\d{1,2})\.(\d{1,2})((?:\.\d+)*)`)
)

// ParseVersion accepts "mr12.5.1.3", "Version: 12.5.1.3+0~mr12.5.1.3 git-master-abc"
// or a Debian package version such as "10.5.3.5-1".
func ParseVersion(s string) (Version, error) {
	m := mrRe.FindStringSubmatch(s)
	if m == nil {
		m = numRe.FindStringSubmatch(s)
	}
	if m == nil {
		return Version{}, fmt.Errorf(i18n.T("no rtpengine version in %q"), strings.TrimSpace(s))
	}
	v := Version{Raw: strings.TrimSpace(s)}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	v.Full = m[1] + "." + m[2] + m[3]
	return v, nil
}

// Known reports whether the version is set.
func (v Version) Known() bool { return v.Major > 0 }

func (v Version) String() string {
	switch {
	case !v.Known():
		return "unknown"
	case v.Floor:
		return fmt.Sprintf("≥mr%d.%d", v.Major, v.Minor)
	}
	return "mr" + v.Full
}

// Short is the release branch, e.g. "mr12.5".
func (v Version) Short() string {
	if !v.Known() {
		return "unknown"
	}
	return fmt.Sprintf("mr%d.%d", v.Major, v.Minor)
}

// AtLeast reports v >= major.minor. Unknown versions are treated as new.
func (v Version) AtLeast(major, minor int) bool {
	if !v.Known() {
		return true
	}
	return v.Major > major || v.Major == major && v.Minor >= minor
}

// Compare orders versions by major.minor.
func (v Version) Compare(major, minor int) int {
	switch {
	case v.Major != major:
		return sign(v.Major - major)
	default:
		return sign(v.Minor - minor)
	}
}

func sign(i int) int {
	switch {
	case i < 0:
		return -1
	case i > 0:
		return 1
	}
	return 0
}

// mr makes a release number for tables.
type mr struct{ Major, Minor int }

func (r mr) String() string { return fmt.Sprintf("mr%d.%d", r.Major, r.Minor) }
