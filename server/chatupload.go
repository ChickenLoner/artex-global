package server

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// maxChatUpload caps a single chat-attachment upload request (memory + spill).
const maxChatUpload = 128 << 20 // 128 MiB

// User-visible error responses of chatUpload (Korean). They are exposed as-is in the UI via writeErr. Terminology:
// Chinese "attachment" → the Korean term for "attached file"; scope/id/file are request field names and kept verbatim. scope validation follows the pattern of intercept.go
// ("value must be one of …"), the task-deletion wording uses the same sentence form as goals_api.go, and "invalid id"
// has the same shape as workspace.go errWsIllegalPath ("invalid path"). The three "… failed" kinds are
// joined as a prefix constant + err.Error(), following the precedent of task_archives.go:229.
const (
	errChatUploadScopeInvalid = "scope 값은 task, session, staging 중 하나여야 합니다"
	errChatUploadBadID        = "잘못된 id입니다"
	errChatUploadTaskDeleting = "작업을 삭제하는 중이라 첨부 파일을 업로드할 수 없습니다"
	errChatUploadMkdir        = "디렉터리를 만들지 못했습니다: "
	errChatUploadParse        = "업로드를 해석하지 못했거나 크기 제한을 초과했습니다: "
	errChatUploadNoFile       = "업로드할 파일이 없습니다(폼 필드 file)"
	errChatUploadSaveFailed   = "저장하지 못했습니다: "
	errChatUploadOutside      = "업로드 위치가 작업 공간 밖입니다"
)

// safeChatID guards the {id} path segment against traversal — task ids are numeric,
// session ids are alnum/_/- ; anything with "/" or ".." is rejected.
var safeChatID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// chatAttachment is one uploaded file as the frontend + agent see it. Path is relative
// to the chat's working dir (e.g. "uploads/report.txt"), which is the agent's CWD, so
// it can Read/Bash the file directly; Name/Size drive the UI card.
type chatAttachment struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Abs is the absolute path on disk (m.dir is already absolute). When staging before task creation (scope=staging), the frontend uses it to write the
	// prompt into the description; task/session build the path on the backend via composeAgentMessage and do not depend on this field.
	Abs string `json:"abs,omitempty"`
}

// chatUpload implements method-1 file support: it saves one or more files into a chat's
// working dir under uploads/, so the agent opens them with its existing Read/Bash tools
// and the sent message carries their paths. No LLM-layer change, no multimodal.
//
// POST /api/chat/upload?scope=task|session|staging&id=<id>, multipart field "file"
// (repeatable). Returns {attachments:[{name,path,size,abs}]}. Target dir mirrors the
// agent CWD layout:
//
//	scope=task    → <workDir>/tasks/<id>/uploads/
//	scope=session → <workDir>/sessions/<id>/uploads/
//	scope=staging → <workDir>/drafts/<id>/uploads/   (staged before task creation: the task has no ID yet,
//	                files land here first, and the frontend writes the returned abs absolute path into the task description)
func (s *Server) chatUpload(w http.ResponseWriter, r *http.Request) {
	var sub string
	taskScoped := false
	switch r.URL.Query().Get("scope") {
	case "task":
		sub = "tasks"
		taskScoped = true
	case "session":
		sub = "sessions"
	case "staging":
		sub = "drafts"
	default:
		writeErr(w, 400, errChatUploadScopeInvalid)
		return
	}
	id := r.URL.Query().Get("id")
	if !safeChatID.MatchString(id) {
		writeErr(w, 400, errChatUploadBadID)
		return
	}
	if taskScoped {
		if s.m.ResolveTask(id) == nil {
			writeErr(w, 404, "task not found")
			return
		}
		if !s.engine.beginTaskOperation(id) {
			writeErr(w, http.StatusConflict, errChatUploadTaskDeleting)
			return
		}
		defer s.engine.decInflight(id)
	}
	dir := filepath.Join(s.m.dir, sub, id, "uploads")
	// The chat working dir is also the agent's CWD, so the agent (or anything it ran) can plant a symlink at
	// <sub>/<id> or <sub>/<id>/uploads. Resolve symlinks and refuse any target outside the workspace, both before
	// creating directories (MkdirAll would otherwise create them through the link) and again afterwards.
	if !s.wsInsideRoot(dir) {
		writeErr(w, 400, errChatUploadOutside)
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, 500, errChatUploadMkdir+err.Error())
		return
	}
	if !s.wsInsideRoot(dir) {
		writeErr(w, 400, errChatUploadOutside)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxChatUpload)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, 400, errChatUploadParse+err.Error())
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeErr(w, 400, errChatUploadNoFile)
		return
	}
	out := make([]chatAttachment, 0, len(files))
	for _, hdr := range files {
		name := filepath.Base(hdr.Filename) // strip any path component
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			continue
		}
		dest := uniqueUploadPath(dir, name)
		if err := s.saveChatUpload(hdr, dest); err != nil {
			if err == errUploadOutside {
				writeErr(w, 400, errChatUploadOutside)
				return
			}
			writeErr(w, 500, errChatUploadSaveFailed+err.Error())
			return
		}
		base := filepath.Base(dest)
		out = append(out, chatAttachment{Name: base, Path: "uploads/" + base, Size: hdr.Size, Abs: dest})
	}
	writeJSON(w, 200, map[string]any{"attachments": out})
}

// uniqueUploadPath returns dir/name, or dir/name-1, dir/name-2… when it already exists,
// so re-uploading the same filename never clobbers a prior attachment.
//
// It uses Lstat, not Stat: a symlink (including a dangling one) already occupies the name, and Stat would follow it
// and report a missing target as "free", sending the write through the link.
func uniqueUploadPath(dir, name string) string {
	dest := filepath.Join(dir, name)
	if _, err := os.Lstat(dest); os.IsNotExist(err) {
		return dest
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
		if _, err := os.Lstat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}

// errUploadOutside reports that a created upload resolved outside the workspace root.
var errUploadOutside = fmt.Errorf("upload resolved outside the workspace")

// saveChatUpload writes one uploaded file to dest without ever following a symlink at the final path component:
// O_EXCL makes OpenFile fail if anything (a file, or a symlink even if dangling) already exists there, so the
// upload can neither overwrite an existing file nor be redirected through a planted link. After creating the file
// it re-checks that the path still resolves inside the workspace (the parent directory could have been swapped for
// a symlink after the check in chatUpload); if not, the just-created empty file is removed and nothing is written.
func (s *Server) saveChatUpload(hdr *multipart.FileHeader, dest string) error {
	src, err := hdr.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if !s.wsInsideRoot(dest) {
		out.Close()
		_ = os.Remove(dest)
		return errUploadOutside
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// composeAgentMessage appends an attachment manifest to the user's message so the agent
// knows which files were uploaded and where to Read them. baseDir is the agent's working
// dir (its CWD); we emit ABSOLUTE paths (baseDir + relative) so the agent can Read/Bash
// them unambiguously regardless of how it interprets relative paths.
func composeAgentMessage(msg string, atts []chatAttachment, baseDir string) string {
	if len(atts) == 0 {
		return msg
	}
	var b strings.Builder
	b.WriteString(msg)
	// [F10 boundary ruling · preserve brain input] This attachment manifest header (`[User-uploaded attachments]…Read/Bash…`, originally Chinese) is
	// not translated. The return value of composeAgentMessage is the message sent to the agent via runConversation/ma.Chat,
	// so this wording is agent input (the brain) that instructs the model to "open the uploaded files with Read/Bash"
	// (BRIEF boundary #1). The transcript renders attachments separately as attachment JSON via userActivityWithAttachments,
	// so this header string itself is never exposed on a display path (brain-only, same kind as F16).
	b.WriteString("\n\n[Files uploaded by the user] (absolute paths; use Read/Bash to view them when needed):")
	for _, a := range atts {
		fmt.Fprintf(&b, "\n- %s（%s）", filepath.Join(baseDir, a.Path), humanBytes(a.Size))
	}
	return b.String()
}

// userActivityWithAttachments builds the persisted 'user' activity. With attachments,
// Detail holds JSON {text, attachments} so the transcript renders text + attachment
// cards; Summary stays the plain text (the list payload omits Detail, lazy-loaded).
func userActivityWithAttachments(worker, text string, atts []chatAttachment) db.Activity {
	a := db.Activity{Worker: worker, Kind: "user", Summary: text}
	if len(atts) > 0 {
		blob, _ := json.Marshal(map[string]any{"text": text, "attachments": atts})
		a.Detail = string(blob)
	}
	return a
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
