package server

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A symlink planted inside the workspace must not let the file API reach outside it.
func TestWsResolveRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "ok", "sub"), 0o755))
	must(os.Symlink(outside, filepath.Join(root, "dirlink")))
	must(os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "filelink")))
	must(os.Symlink(filepath.Join(outside, "missing"), filepath.Join(root, "dangling")))
	must(os.Symlink(filepath.Join(root, "ok"), filepath.Join(root, "inlink")))

	s := &Server{m: &Manager{dir: root}}
	for _, rel := range []string{"dirlink", "dirlink/secret", "dirlink/new.txt", "filelink", "dangling"} {
		if _, ok := s.wsResolve(rel); ok {
			t.Errorf("wsResolve(%q) should be rejected", rel)
		}
	}
	for _, rel := range []string{"", "ok", "ok/sub", "ok/new/file.txt", "inlink/sub", "not-yet.txt"} {
		if _, ok := s.wsResolve(rel); !ok {
			t.Errorf("wsResolve(%q) should be allowed", rel)
		}
	}
	// Deleting the link itself is fine, but not through a link to an outside dir.
	if _, ok := s.wsResolveLeaf("dirlink"); !ok {
		t.Error("wsResolveLeaf(dirlink) should allow removing the link itself")
	}
	if _, ok := s.wsResolveLeaf("dirlink/secret"); ok {
		t.Error("wsResolveLeaf(dirlink/secret) must be rejected")
	}
}
