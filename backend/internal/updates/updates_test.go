package updates

import "testing"

func TestUpdateAvailable(t *testing.T) {
	for _, c := range []struct {
		cur, latest string
		want        bool
	}{
		{"v0.2.0", "v0.2.1", true},
		{"v0.2.1", "v0.2.1", false},
		{"v0.3.0", "v0.2.9", false},
		{"v3.1.14", "v3.1.15", true},
		{"3.1.14", "v3.1.15", true},             // a service reporting its version without "v"
		{"2026.11.20-eaf07bd", "v0.2.0", false}, // Journarr build from main
		{"dev", "v0.2.0", false},
		{"", "v0.2.0", false},
		{"v0.2.0", "nightly", false},
	} {
		if got := updateAvailable(c.cur, c.latest); got != c.want {
			t.Errorf("updateAvailable(%q, %q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}
