package upkeep

import (
	"fmt"
	"strconv"
	"strings"
)

// CompareVersions orders two semantic versions without a leading "v":
// -1 when a < b, 0 when equal, 1 when a > b. Pre-release versions sort
// before the release they precede, as semver requires; build metadata
// after "+" is ignored.
func CompareVersions(a, b string) (int, error) {
	pa, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseSemver(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if pa.nums[i] != pb.nums[i] {
			if pa.nums[i] < pb.nums[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	switch {
	case pa.pre == "" && pb.pre == "":
		return 0, nil
	case pa.pre == "":
		return 1, nil
	case pb.pre == "":
		return -1, nil
	}
	return comparePrerelease(pa.pre, pb.pre), nil
}

type semver struct {
	nums [3]int
	pre  string
}

func parseSemver(s string) (semver, error) {
	var v semver
	core := s
	if i := strings.IndexByte(core, '+'); i >= 0 {
		core = core[:i]
	}
	if i := strings.IndexByte(core, '-'); i >= 0 {
		v.pre = core[i+1:]
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("version %q: want major.minor.patch", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (len(p) > 1 && p[0] == '0') {
			return v, fmt.Errorf("version %q: %q is not a valid number", s, p)
		}
		v.nums[i] = n
	}
	return v, nil
}

func comparePrerelease(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aNum := strconv.Atoi(as[i])
		bn, bNum := strconv.Atoi(bs[i])
		switch {
		case aNum == nil && bNum == nil:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case aNum == nil:
			return -1 // numeric identifiers sort before alphanumeric ones
		case bNum == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}
