package config

import (
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path via a temporary file in the same
// directory, then renames it into place.
//
// The previous os.WriteFile call truncated the real config before writing, so
// a crash, a full disk, or a killed process partway through left the user with
// a truncated or empty config — which then failed to parse on next launch and
// took the app down with it. Rename within a directory is atomic, so a reader
// sees either the old file or the new one.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	// Rename replaces whatever is at the destination, symlink included, where
	// the os.WriteFile this replaced would have followed the link and written
	// through to its target. People keep this config in a dotfiles repo and
	// symlink it into ~/.config, so without this an in-app settings change
	// reported success, turned the link into a regular file, and left the
	// tracked copy silently untouched.
	//
	// Resolve first, then write the temp file beside — and rename onto — the
	// real file, which keeps both the link and the atomicity.
	path = resolveSymlink(path)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// The temp file must share a directory with the target: rename cannot
	// cross filesystems, and the config dir may well be a different mount
	// from the system temp dir.
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	// Flush to disk before the rename, so a crash immediately afterwards
	// cannot leave the new name pointing at unwritten data.
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// resolveSymlink returns what path points at, or path unchanged when it is not
// a symlink and when it does not exist.
//
// A link whose target does not exist yet still has to be followed. EvalSymlinks
// fails on one, and returning the link path meant the rename replaced the link
// with a regular file — where os.WriteFile would have followed it and created
// the target. That is the state a fresh dotfiles checkout is in, before the app
// has written a config for the first time, so it is not an exotic case.
func resolveSymlink(path string) string {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return path
	}

	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}

	// Dangling: read the link's own text instead. A relative target is relative
	// to the directory holding the link, not the working directory.
	target, err := os.Readlink(path)
	if err != nil {
		return path
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return target
}
