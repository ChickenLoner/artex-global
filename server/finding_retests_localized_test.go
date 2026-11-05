package server

import "testing"

// This is a regression-guard test that keeps the user-facing HTTP error response messages of finding_retests.go in Korean.
// The Korean check reuses assertKoreanError made by F3a (contains Hangul, zero Chinese Han characters).
// The startFindingRetest handler that returns the four messages first passes the s.pg(w) (DB)
// gate, so it cannot run to the end on this DB-less host; the constants themselves are pinned
// (the same way as the DB-gated paths of finding_traffic, goals_api and notify_api). If someone reverts this
// literal to Chinese, this test fails.
//
// The tool descriptions (get_finding_retest_context, record_finding_retest_result), parameter descriptions,
// actool.Errorf (the round-not-linked notice), and the DB-seeded agent name, profile and note of seedFindingRetester are
// brain input read by the agent, or seeds, so the original text (Chinese) is intentionally
// preserved and is not asserted by this test (see the comment on the constant block in finding_retests.go).
func TestFindingRetestErrorsLocalized(t *testing.T) {
	for _, c := range []struct {
		label, msg string
	}{
		{"notes_too_long", errFindingRetestNotesTooLong},
		{"agent_missing", errFindingRetestAgentMissing},
		{"tool_required", errFindingRetestToolRequired},
		{"service_stopping", errFindingRetestServiceStopping},
	} {
		assertKoreanError(t, c.label, c.msg)
	}
}
