package server

import "testing"

// TestReporterSeedLabelsLocalized is a regression-guard test that keeps the display-only labels of the seeded reporter agent
// (reporterAgentName·reporterAgentDescription) in Korean. These two strings are rendered only in the system/agents UI via `agentDTO` (server_mgmt.go),
// and enter neither any agent system prompt (renderSystem assembles only from key -> section [A] agent.ReporterDefaultPrompt)
// nor model-based agent selection (the reporter trigger is based on report_finding tool calls,
// OnToolCall, deterministic); they are pure UI labels. So localizing them into Korean does not touch BRIEF boundary #1
// (the brain is not translated). Conversely, the reporter's brain body (agent.ReporterDefaultPrompt)
// and the trigger-injected message (reporterToolCallMessage) are model input and keep their Chinese originals,
// so they are not covered by this test. For the Korean check it reuses assertKoreanError (contains Hangul, zero Chinese Han characters,
// intercept_archive_localized_test.go). [[F35]] [[G133]]
func TestReporterSeedLabelsLocalized(t *testing.T) {
	assertKoreanError(t, "reporter_name", reporterAgentName)
	assertKoreanError(t, "reporter_description", reporterAgentDescription)
}
