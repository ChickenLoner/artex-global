package server

import (
	"fmt"
	"strings"
	"testing"
)

// A regression-guard test that keeps the "display-only" runtime activity summary constants of engine.go, engine_timeout.go and goals.go in
// Korean. These summaries are exposed in the dashboard activity transcript, but carry no
// node_id (task-level), so they are never read back into any agent context — the feedback
// paths (planner.workerOutput and the get_worker_output tool) pick only 'result'/
// 'text' activities in intent scope (node_id). So the Korean localization does not touch brain input (BRIEF boundary #1).
// Conversely, summaries that are fed back to the brain (the Chinese seed intent summary "complete the task goal ...", the Chinese default description "unnamed task"
// and so on) preserve the original text, so they are not targets of this test. Korean detection reuses assertKoreanError (contains
// Hangul, zero Chinese Han characters, intercept_archive_localized_test.go). [[G132]]
func TestRuntimeActivitySummariesLocalized(t *testing.T) {
	// Fixed summaries with no format arguments: must be Korean as they are.
	plain := map[string]string{
		"goalless_task_done": goallessTaskDoneSummary,
		"goal_breakdown_r0":  goalBreakdownRound0Summary,
		"queued_no_llm":      queuedNoLLMSummary,
		"queued_fifo":        queuedFIFOSummary,
	}
	for label, msg := range plain {
		assertKoreanError(t, label, msg)
	}

	// %d format strings: verify that the format argument is still present, that the formatted result is also Korean, and that the number is actually
	// reflected.
	fmtCases := map[string]string{
		"planner_round":       plannerRoundSummaryFmt,
		"timeout_final_round": timeoutFinalRoundSummaryFmt,
		"queued_conc_limit":   queuedConcurrencyLimitSummaryFmt,
	}
	for label, f := range fmtCases {
		if !strings.Contains(f, "%d") {
			t.Fatalf("%s: 포맷 인자 %%d 가 사라졌습니다: %q", label, f)
		}
		got := fmt.Sprintf(f, 7)
		assertKoreanError(t, label, got)
		if !strings.Contains(got, "7") {
			t.Fatalf("%s: 포맷 인자가 결과에 반영되지 않았습니다: %q", label, got)
		}
	}
}
