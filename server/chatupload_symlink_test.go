package server

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func chatUploadRequest(t *testing.T, query, filename, content string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/chat/upload?"+query, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

// The chat working dir is the agent's CWD, so a symlink planted there must never redirect an upload outside the workspace.
func TestChatUploadRefusesSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	s := &Server{m: &Manager{dir: root}}

	t.Run("session dir is a symlink to an outside dir", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "sessions", "s1")); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		s.chatUpload(rec, chatUploadRequest(t, "scope=session&id=s1", "evil.txt", "pwned"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
		}
		if got := dirEntries(t, outside); len(got) != 0 {
			t.Fatalf("files were written outside the workspace: %v", got)
		}
	})

	t.Run("uploads dir is a symlink to an outside dir", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Join(root, "sessions", "s2"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "sessions", "s2", "uploads")); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		s.chatUpload(rec, chatUploadRequest(t, "scope=session&id=s2", "evil.txt", "pwned"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
		}
		if got := dirEntries(t, outside); len(got) != 0 {
			t.Fatalf("files were written outside the workspace: %v", got)
		}
	})

	t.Run("target filename is a symlink to an outside file", func(t *testing.T) {
		victim := filepath.Join(outside, "victim.txt")
		if err := os.WriteFile(victim, []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}
		up := filepath.Join(root, "sessions", "s3", "uploads")
		if err := os.MkdirAll(up, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(up, "report.txt")); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		s.chatUpload(rec, chatUploadRequest(t, "scope=session&id=s3", "report.txt", "pwned"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (the upload must succeed under a different name; body %q)", rec.Code, rec.Body.String())
		}
		if b, _ := os.ReadFile(victim); string(b) != "original" {
			t.Fatalf("the upload was written through the symlink: victim now %q", b)
		}
		if _, err := os.Stat(filepath.Join(up, "report-1.txt")); err != nil {
			t.Fatalf("expected the upload under report-1.txt: %v", err)
		}
	})

	t.Run("dangling symlink target is not written through", func(t *testing.T) {
		up := filepath.Join(root, "sessions", "s4", "uploads")
		if err := os.MkdirAll(up, 0o755); err != nil {
			t.Fatal(err)
		}
		ghost := filepath.Join(outside, "ghost.txt") // does not exist
		if err := os.Symlink(ghost, filepath.Join(up, "note.txt")); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		s.chatUpload(rec, chatUploadRequest(t, "scope=session&id=s4", "note.txt", "pwned"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		if _, err := os.Lstat(ghost); err == nil {
			t.Fatal("the upload created a file at the dangling symlink's target")
		}
	})
}

func TestChatUploadNormalStillWorks(t *testing.T) {
	root := t.TempDir()
	s := &Server{m: &Manager{dir: root}}
	for i, want := range []string{"hello.txt", "hello-1.txt"} {
		rec := httptest.NewRecorder()
		s.chatUpload(rec, chatUploadRequest(t, "scope=session&id=ok1", "hello.txt", "hi"))
		if rec.Code != http.StatusOK {
			t.Fatalf("upload %d: status = %d (body %q)", i, rec.Code, rec.Body.String())
		}
		b, err := os.ReadFile(filepath.Join(root, "sessions", "ok1", "uploads", want))
		if err != nil || string(b) != "hi" {
			t.Fatalf("upload %d: expected %s with content %q, got %q (%v)", i, want, "hi", b, err)
		}
	}
}
