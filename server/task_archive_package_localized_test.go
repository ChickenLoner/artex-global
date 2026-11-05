package server

import (
	"os"
	"path/filepath"
	"testing"
)

// A regression-guard test that keeps the archive-package deletion-prep error wording of task_archive_package.go in Korean.
// The Korean check reuses assertKoreanError created by F3a (contains Hangul, zero Chinese Han characters).
// If anyone reverts this literal to Chinese, this test fails.
//
// Call-graph determination (see the comment on the errArchiveStagedAndOriginalCoexist constant): this error is saved by the background
// archive worker runOneTaskArchiveJob (task_archives.go:102) via FailTaskArchiveJob into the
// task_archives.error column, and TaskArchivesPanel in tasks/page.tsx displays {archive.error}
// directly on screen, so it is purely user-facing (zero actool / planner feedback). Sibling errors on the same propagation path
// (archiveTask, validateArchivePath) are already in Korean, so this translation is consistent with them.
//
// stageTaskArchivePackageDelete is a pure file function that never touches the DB, so on this host without a DB,
// the original file and the deletion temp file (.deleting-N) can be created together to drive the actual contradictory branch.
func TestTaskArchiveStagedAndOriginalCoexistErrorLocalized(t *testing.T) {
	dataDir := t.TempDir()
	archivePath := taskArchivePath(dataDir, 21, "42")
	if err := os.MkdirAll(filepath.Dir(archivePath), archiveDirMode); err != nil {
		t.Fatal(err)
	}
	// Create the contradictory state in which both the original archive package and the deletion temp file exist.
	if err := os.WriteFile(archivePath, []byte("original"), archiveFileMode); err != nil {
		t.Fatal(err)
	}
	staged := archivePath + ".deleting-21"
	if err := os.WriteFile(staged, []byte("archive"), archiveFileMode); err != nil {
		t.Fatal(err)
	}
	_, moved, err := stageTaskArchivePackageDelete(archivePath, 21)
	if err == nil {
		t.Fatalf("원본과 삭제 임시 파일이 공존하면 오류가 반환되어야 합니다 (moved=%v)", moved)
	}
	if moved {
		t.Fatal("공존 모순 상태에서는 moved 가 true 가 되어서는 안 됩니다")
	}
	assertKoreanError(t, "staged_and_original_coexist", err.Error())
}
