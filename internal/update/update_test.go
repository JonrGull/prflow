package update

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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

// fakeRelease stands in for gh: it writes the binary and, unless sums is nil,
// a SHA256SUMS file into the download dir.
func fakeRelease(t *testing.T, binary []byte, sums func(name string) string) {
	t.Helper()
	orig := download
	t.Cleanup(func() { download = orig })
	download = func(tag, repo, dir string, assets ...string) error {
		name := getBinaryAssetName()
		if err := os.WriteFile(filepath.Join(dir, name), binary, 0o644); err != nil {
			return err
		}
		if sums == nil {
			return nil
		}
		return os.WriteFile(filepath.Join(dir, checksumsAsset), []byte(sums(name)), 0o644)
	}
}

func sumLine(data []byte, name string) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]) + "  " + name + "\n"
}

// installed is the file install replaces, standing in for the running binary.
func installed(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "prflow")
	if err := os.WriteFile(p, []byte("working binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInstallVerifiesTheChecksum(t *testing.T) {
	good := []byte("the new binary")
	other := "0000000000000000000000000000000000000000000000000000000000000000  prflow-other-arch\n"

	cases := []struct {
		name    string
		sums    func(name string) string
		wantErr string // "" means installed
	}{
		{"matching", func(n string) string { return other + sumLine(good, n) }, ""},
		{"binary-mode marker", func(n string) string { return strings.Replace(sumLine(good, n), "  ", " *", 1) }, ""},
		// A truncated or corrupted download.
		{"mismatch", func(n string) string { return sumLine([]byte("something else"), n) }, "checksum mismatch"},
		{"no entry for this platform", func(string) string { return other }, "no entry"},
		// Fail closed: a release without SHA256SUMS can't be verified.
		{"no SHA256SUMS", nil, "can't be verified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeRelease(t, good, tc.sums)
			target := installed(t)

			err := install("v9.9.9", "owner/repo", target)
			got, _ := os.ReadFile(target)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("install: %v", err)
				}
				if string(got) != string(good) {
					t.Errorf("binary = %q, want the new one", got)
				}
				if info, _ := os.Stat(target); info.Mode().Perm()&0o100 == 0 {
					t.Errorf("mode = %v, want executable", info.Mode())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want one containing %q", err, tc.wantErr)
			}
			if string(got) != "working binary" {
				t.Errorf("binary was replaced with %q despite the failed check", got)
			}
		})
	}
}

// CheckForUpdate took the first row of gh release list, which includes
// drafts (visible to the repo owner) and pre-releases. gh release view with
// no tag is GitHub's latest release, which excludes both.
func TestCheckForUpdateUsesTheLatestRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
  "release view") printf '{"tagName":"v9.9.9"}' ;;
  *) echo "unexpected: $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	rel, err := CheckForUpdate("v1.0.0", "owner/repo")
	if err != nil || rel == nil || rel.TagName != "v9.9.9" {
		t.Errorf("got %+v, %v; want v9.9.9", rel, err)
	}
}
