// Package version holds the semver math vesnik uses to pick the next release:
// bump levels, pre-release counters, promotion, and tag templates.
package version

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Bump is a semver bump level.
type Bump string

const (
	Patch Bump = "patch"
	Minor Bump = "minor"
	Major Bump = "major"
)

var order = []Bump{Patch, Minor, Major}

// ParseBump accepts patch, minor or major.
func ParseBump(s string) (Bump, error) {
	b := Bump(strings.ToLower(strings.TrimSpace(s)))
	if !slices.Contains(order, b) {
		return "", fmt.Errorf("unknown bump %q: want patch, minor or major", s)
	}
	return b, nil
}

// Rank orders bumps: patch < minor < major.
func (b Bump) Rank() int { return slices.Index(order, b) }

// Cycle returns the next bump in patch → minor → major → patch order.
func (b Bump) Cycle() Bump { return order[(b.Rank()+1)%len(order)] }

// Max returns the larger of two bumps.
func Max(a, b Bump) Bump {
	if b.Rank() > a.Rank() {
		return b
	}
	return a
}

// Options control how Next computes a version.
type Options struct {
	// Pre is a pre-release identifier such as "rc" or "beta". Empty means a
	// final release, which promotes a current pre-release to its final version.
	Pre string
	// BreakingBumpsMinor maps a major bump to a minor one while the version is
	// below 1.0.0.
	BreakingBumpsMinor bool
}

// Next returns the version that follows cur for bump. cur is the latest
// release on the line, which may be a pre-release; lastFinal is the latest
// final release. Either may be nil when nothing has been released yet.
func Next(cur, lastFinal *semver.Version, bump Bump, opts Options) (*semver.Version, error) {
	zero := semver.New(0, 0, 0, "", "")
	if cur == nil {
		cur = zero
	}
	if lastFinal == nil {
		lastFinal = zero
	}
	if bump.Rank() < 0 {
		return nil, fmt.Errorf("unknown bump %q", bump)
	}
	if opts.Pre != "" && !preID.MatchString(opts.Pre) {
		return nil, fmt.Errorf("invalid pre-release identifier %q", opts.Pre)
	}

	if cur.Prerelease() != "" {
		base := semver.New(cur.Major(), cur.Minor(), cur.Patch(), "", "")
		curID, n := splitPre(cur.Prerelease())
		// A bump larger than the one that started the pre-release line moves
		// to a new base; otherwise the line continues or is promoted.
		if target := apply(lastFinal, bump, opts); target.GreaterThan(base) {
			base, curID, n = target, "", 0
		}
		switch opts.Pre {
		case "":
			return base, nil
		case curID:
			return withPre(base, opts.Pre, n+1)
		default:
			return withPre(base, opts.Pre, 1)
		}
	}

	next := apply(cur, bump, opts)
	if opts.Pre != "" {
		return withPre(next, opts.Pre, 1)
	}
	return next, nil
}

var preID = regexp.MustCompile(`^[0-9A-Za-z-]+$`)

func apply(cur *semver.Version, bump Bump, opts Options) *semver.Version {
	if bump == Major && opts.BreakingBumpsMinor && cur.Major() == 0 {
		bump = Minor
	}
	switch bump {
	case Major:
		v := cur.IncMajor()
		return &v
	case Minor:
		v := cur.IncMinor()
		return &v
	default:
		return semver.New(cur.Major(), cur.Minor(), cur.Patch()+1, "", "")
	}
}

func splitPre(pre string) (string, int) {
	id, num, ok := strings.Cut(pre, ".")
	if !ok {
		return id, 0
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		return pre, 0
	}
	return id, n
}

func withPre(v *semver.Version, id string, n int) (*semver.Version, error) {
	out, err := v.SetPrerelease(fmt.Sprintf("%s.%d", id, n))
	if err != nil {
		return nil, err
	}
	return &out, nil
}
