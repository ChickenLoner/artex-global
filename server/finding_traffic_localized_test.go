package server

import "testing"

// A regression-guard test that keeps the user-facing HTTP error response wording of finding_traffic.go in Korean.
// Korean detection reuses assertKoreanError built in F3a (contains Hangul, zero Chinese Han characters).
// The paths that return the four messages (findingTrafficAccess, bindFindingTraffic,
// editFindingTraffic) all sit behind the s.m.pg.GetFinding (DB) gate, so they cannot run all the way through on this host without a DB;
// the constants themselves are therefore pinned (same approach as the DB-gated
// paths of goals_api and notify_api). This test fails if someone reverts these literals to Chinese.
//
// The errors.New in readEvidencePreview (offset/length validation), the binary-body placeholder and
// the roTool("get_finding_traffic") tool description are brain
// input fed back as agent tool results, so they intentionally preserve the original text (Chinese), and are not assertion targets of this test.
func TestFindingTrafficErrorsLocalized(t *testing.T) {
	for _, c := range []struct {
		label, msg string
	}{
		{"inherited_readonly", errFindingTrafficInheritedReadonly},
		{"select_required", errFindingTrafficSelectRequired},
		{"version_required", errFindingTrafficVersionRequired},
		{"binding_ids_required", errFindingTrafficBindingIDsRequired},
	} {
		assertKoreanError(t, c.label, c.msg)
	}
}
