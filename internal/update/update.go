package update

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/JonrGull/prflow/internal/run"
)

// Release represents a GitHub release
type Release struct {
	TagName string `json:"tagName"`
}

// CheckForUpdate queries GitHub releases and returns latest if newer than current
func CheckForUpdate(currentVersion, repo string) (*Release, error) {
	// Use gh CLI to get latest release
	output, err := run.Output(run.Network, "", "gh", "release", "list",
		"--repo", repo,
		"--json", "tagName",
		"--limit", "1",
	)
	if err != nil {
		return nil, fmt.Errorf("gh release list failed: %w", err)
	}

	var releases []Release
	if err := json.Unmarshal(output, &releases); err != nil {
		return nil, fmt.Errorf("failed to parse releases: %w", err)
	}

	if len(releases) == 0 {
		return nil, nil
	}

	latest := &releases[0]

	// Compare versions - strip 'v' or 'prflow/v' prefix for comparison
	latestVer := normalizeVersion(latest.TagName)
	currentVer := normalizeVersion(currentVersion)

	// "dev" version is always older than any release
	if currentVer == "dev" {
		return latest, nil
	}

	if compareVersions(latestVer, currentVer) > 0 {
		return latest, nil
	}

	return nil, nil
}

// normalizeVersion strips version prefixes for comparison
func normalizeVersion(v string) string {
	v = strings.TrimPrefix(v, "prflow/")
	v = strings.TrimPrefix(v, "v")
	return v
}

// compareVersions compares two dot-separated version strings numerically,
// returning -1 if a < b, 0 if equal, and 1 if a > b.
//
// A lexical comparison is wrong here: "1.0.10" sorts before "1.0.9" as a
// string, which silently stopped the updater from offering any release once
// the patch number reached double digits. Missing trailing components are
// treated as zero, so "1.1" == "1.1.0". Non-numeric components (e.g. a
// "-rc1" suffix) fall back to a string comparison of that component.
func compareVersions(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	for i := 0; i < len(aParts) || i < len(bParts); i++ {
		aPart := partAt(aParts, i)
		bPart := partAt(bParts, i)

		aNum, aErr := strconv.Atoi(aPart)
		bNum, bErr := strconv.Atoi(bPart)

		if aErr == nil && bErr == nil {
			if aNum != bNum {
				return sign(aNum - bNum)
			}
			continue
		}

		if aPart != bPart {
			return strings.Compare(aPart, bPart)
		}
	}

	return 0
}

func partAt(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return "0"
}

func sign(n int) int {
	if n < 0 {
		return -1
	}
	if n > 0 {
		return 1
	}
	return 0
}

// getBinaryPath returns the path to the current executable
func getBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// Resolve symlinks to get actual path
	return filepath.EvalSymlinks(exe)
}

// getBinaryAssetName returns the expected binary name for the current platform
func getBinaryAssetName() string {
	return fmt.Sprintf("prflow-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// DownloadAndInstall downloads the binary and replaces the current executable
func DownloadAndInstall(release *Release, repo string) error {
	binaryPath, err := getBinaryPath()
	if err != nil {
		return fmt.Errorf("failed to get binary path: %w", err)
	}

	assetName := getBinaryAssetName()
	tmpPath := filepath.Join(os.TempDir(), "prflow-update")

	// Download using gh CLI. Binaries are large, so this gets a longer
	// deadline than an ordinary API call.
	output, err := run.Combined(run.Slow, "", "gh", "release", "download",
		release.TagName,
		"--repo", repo,
		"--pattern", assetName,
		"--output", tmpPath,
		"--clobber",
	)
	if err != nil {
		if run.IsTimeout(err) {
			return err
		}
		return fmt.Errorf("download failed: %s", string(output))
	}

	// Make executable
	err = os.Chmod(tmpPath, 0755)
	if err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}

	// Verify the download is a valid executable by checking file size
	info, err := os.Stat(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to stat downloaded file: %w", err)
	}
	if info.Size() < 1000 {
		return fmt.Errorf("downloaded file too small (%d bytes), likely invalid", info.Size())
	}

	// Atomic replace: rename over the current binary
	if err := os.Rename(tmpPath, binaryPath); err != nil {
		// If rename fails (e.g., cross-device), fall back to copy
		return copyFile(tmpPath, binaryPath)
	}

	return nil
}

// copyFile copies src to dst with proper permissions
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// Create temp file in same directory as dst for atomic replace
	dstDir := filepath.Dir(dst)
	tmpFile, err := os.CreateTemp(dstDir, "prflow-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	if _, err := io.Copy(tmpFile, srcFile); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Clean up source
	os.Remove(src)
	return nil
}

// VersionDisplay returns a formatted version string for display
func VersionDisplay(tag string) string {
	return normalizeVersion(tag)
}
