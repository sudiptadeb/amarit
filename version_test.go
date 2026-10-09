package upkeep

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.5.2", "0.5.3", -1},
		{"0.5.3", "0.5.3", 0},
		{"1.0.0", "0.9.9", 1},
		{"0.10.0", "0.9.0", 1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0-rc.1", "1.0.0-rc.2", -1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"1.0.0-1", "1.0.0-alpha", -1},
		{"1.0.0+build.7", "1.0.0", 0},
	}
	for _, c := range cases {
		got, err := CompareVersions(c.a, c.b)
		if err != nil {
			t.Errorf("%s vs %s: %v", c.a, c.b, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s vs %s = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, bad := range []string{"v1.0.0", "1.0", "1.01.0", "2026-10-09--11-17+sha", ""} {
		if _, err := CompareVersions(bad, "1.0.0"); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}
