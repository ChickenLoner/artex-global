package server

import "testing"

// This is a regression-guard test that keeps the precondition-validation error messages for resuming a task (admitPausedTask) in goals.go in Korean.
// The Korean check reuses assertKoreanError made by F3a (contains Hangul, zero
// Chinese Han characters). The admitTaskWhen(requirePaused=true) path that returns the two messages goes through the
// engine gates s.m.Task(), s.engine.IsDeleting() and beginTaskOperation() after taking the s.concMu lock,
// so it cannot run to the end on this host without DB or engine; the constants themselves are pinned
// (the same way as the post-gate paths of finding_retests and finding_traffic). If someone reverts this literal
// to Chinese, this test fails.
//
// Call-graph ruling (see the comment on the constant block in goals.go): the two messages are user-only, exposed solely through the single-item (server.go:1194 →
// writeErr 409) and batch (task_control.go:298 → items[].error) task-control responses.
// The orchestrator path (orchestration.go:343) is fixed to action="pause" and
// never reaches resume validation, so they are not brain input.
func TestGoalResumeErrorsLocalized(t *testing.T) {
	for _, c := range []struct {
		label, msg string
	}{
		{"resume_terminal", errGoalResumeTerminal},
		{"resume_not_paused", errGoalResumeNotPaused},
	} {
		assertKoreanError(t, c.label, c.msg)
	}
}
