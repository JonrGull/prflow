package update

import "testing"

// compareVersions replaced a lexical string comparison that silently broke
// self-update. "1.0.10" sorts before "1.0.9" as a string, and the release
// history really did run v1.0.9 -> v1.0.10 -> ... -> v1.0.25, so anyone sitting
// on v1.0.9 stopped being offered updates and never recovered.
func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		// The regression: double-digit components.
		{"1.0.10", "1.0.9", 1},
		{"1.0.9", "1.0.10", -1},
		{"1.1.10", "1.1.9", 1},
		{"1.10.0", "1.9.0", 1},
		{"1.0.25", "1.0.9", 1},

		// Ordinary ordering.
		{"1.1.3", "1.1.3", 0},
		{"1.1.3", "1.0.9", 1},
		{"1.0.25", "1.1.0", -1},
		{"2.0.0", "1.99.99", 1},

		// Missing trailing components are zero.
		{"1.1", "1.1.0", 0},
		{"1.1.1", "1.1", 1},
		{"1", "1.0.0", 0},

		// Non-numeric components fall back to a string comparison rather than
		// being silently treated as equal, so ordering stays deterministic.
		//
		// Note this is not full semver: a proper implementation ranks a release
		// above its own pre-releases (1.0.0 > 1.0.0-rc1) whereas the string
		// fallback gives the opposite. That case is deliberately not asserted
		// here — pinning it would enshrine the wrong answer. The auto-tag
		// workflow only ever produces plain vX.Y.Z tags, so it does not arise;
		// if pre-release tags are ever published, fix the comparison rather
		// than adding a case for it.
		{"1.0.0-rc1", "1.0.0-rc2", -1},
	}

	for _, tt := range tests {
		if got := compareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
		// Comparison must be antisymmetric, or ordering is meaningless.
		if got, want := compareVersions(tt.b, tt.a), -tt.want; got != want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d (not antisymmetric)", tt.b, tt.a, got, want)
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	for in, want := range map[string]string{
		"v1.2.3":        "1.2.3",
		"prflow/v1.2.3": "1.2.3",
		"1.2.3":         "1.2.3",
		"dev":           "dev",
		"":              "",
	} {
		if got := normalizeVersion(in); got != want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
