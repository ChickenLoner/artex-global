package server

import "testing"

// A regression guard that keeps the worker-agent-count setting validation error text in manager.go in Korean.
// The Korean check reuses assertKoreanError from F3a (contains Hangul, zero Chinese Han characters).
// The SetWorkers(n<=0) branch returns before reaching m.pg.SetSetting, so it can actually be driven with an empty Manager on this host that has no DB.
// If someone reverts this literal to Chinese,
// this test fails.
//
// Call-graph ruling (see the errWorkersPositive constant comment): this text is exposed only through the settings PUT handler
// (server.go:3512 → writeErr 400), for users only; there is no agent tool (actool)
// path, so it is not brain input. The startup-recovery error at line 384 of the same file is operator startup-log
// material and outside the scope of F3b (original text preserved), so it is not the subject of this test.
func TestSetWorkersErrorLocalized(t *testing.T) {
	err := (&Manager{}).SetWorkers(0)
	if err == nil {
		t.Fatal("워크 에이전트 수 0 은 거부되어야 합니다")
	}
	assertKoreanError(t, "set_workers_nonpositive", err.Error())
}
