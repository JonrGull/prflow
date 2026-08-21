package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A config kept in a dotfiles repo and symlinked into ~/.config must stay a
// symlink. Renaming a temp file onto the link would replace the link with a
// regular file — the write appears to succeed, and the tracked copy silently
// stops changing. The os.WriteFile this replaced followed the link.
func TestWriteFileAtomicWritesThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "prflow.toml")
	link := filepath.Join(dir, "config", "prflow.toml")

	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("original\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := writeFileAtomic(link, []byte("updated\n"), 0644); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "updated\n" {
		t.Errorf("target holds %q, want the new contents — the write did not reach it", got)
	}
}

// The ordinary cases must be untouched by the symlink handling.
func TestWriteFileAtomicPlainFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("new file", func(t *testing.T) {
		path := filepath.Join(dir, "new", "prflow.toml")
		if err := writeFileAtomic(path, []byte("hello\n"), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "hello\n" {
			t.Errorf("read %q", got)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("mode = %v, want 0600", perm)
		}
	})

	t.Run("overwrite", func(t *testing.T) {
		path := filepath.Join(dir, "existing.toml")
		if err := os.WriteFile(path, []byte("old\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := writeFileAtomic(path, []byte("new\n"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "new\n" {
			t.Errorf("read %q", got)
		}
	})

	// A link whose target does not exist yet — a fresh dotfiles checkout before
	// the first save. os.WriteFile would follow it and create the target; the
	// first version of this checked only that the path was readable afterwards,
	// which passes whether or not the link survived, so it missed that the
	// rename was replacing the link with a regular file.
	for _, tc := range []struct{ name, target string }{
		{"absolute target", ""}, // filled in below
		{"relative target", "sub/x.toml"},
	} {
		t.Run("dangling symlink, "+tc.name, func(t *testing.T) {
			base := t.TempDir()
			if err := os.MkdirAll(filepath.Join(base, "sub"), 0755); err != nil {
				t.Fatal(err)
			}
			target := tc.target
			if target == "" {
				target = filepath.Join(base, "sub", "x.toml")
			}
			link := filepath.Join(base, "prflow.toml")
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}

			if err := writeFileAtomic(link, []byte("x\n"), 0644); err != nil {
				t.Fatalf("writing through a dangling symlink failed: %v", err)
			}

			info, err := os.Lstat(link)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode()&os.ModeSymlink == 0 {
				t.Error("the dangling symlink was replaced by a regular file")
			}
			resolved := target
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(base, resolved)
			}
			if got, err := os.ReadFile(resolved); err != nil || string(got) != "x\n" {
				t.Errorf("target holds %q (err %v) — the write did not reach it", got, err)
			}
		})
	}

	// No temp files may survive a successful write.
	t.Run("leaves no temp files", func(t *testing.T) {
		clean := t.TempDir()
		path := filepath.Join(clean, "prflow.toml")
		if err := writeFileAtomic(path, []byte("y\n"), 0644); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(clean)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.Name()
			}
			t.Errorf("directory holds %v, want just the config", names)
		}
	})
}
