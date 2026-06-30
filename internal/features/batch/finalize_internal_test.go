package batch

import (
	"os"
	"path/filepath"
	"testing"
)

// copyTree is the EXDEV (cross-device) fallback for evidence relocation. It must
// preserve a symlink AS a symlink — WalkDir does not follow links, so without
// explicit handling a symlink-to-dir aborts the copy ("is a directory") and a
// symlink-to-file is silently materialized into a regular file.
func TestCopyTreePreservesSymlinks(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	if err := os.WriteFile(filepath.Join(src, "real.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write real file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(src, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink("real.txt", filepath.Join(src, "link-to-file")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	if err := os.Symlink("subdir", filepath.Join(src, "link-to-dir")); err != nil {
		t.Fatalf("symlink to dir: %v", err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	for _, name := range []string{"link-to-file", "link-to-dir"} {
		info, err := os.Lstat(filepath.Join(dst, name))
		if err != nil {
			t.Fatalf("lstat %s: %v", name, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s must be preserved as a symlink, got mode %v", name, info.Mode())
		}
	}
	// The regular file still copies through.
	if b, err := os.ReadFile(filepath.Join(dst, "real.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("real.txt copy wrong: %q err=%v", b, err)
	}
}
