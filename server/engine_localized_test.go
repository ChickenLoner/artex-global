package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// A regression-guard test that keeps the "user-facing" wording of the task-control and intent-intervention errors in engine.go in Korean.
// Korean detection reuses assertKoreanError built in F3a (contains Hangul, zero Chinese Han characters).
//
//
// Call-graph determination (see the engine.go constant block comment):
//   - The four ControlWork errors (no running work, already under control, cancelling while waiting to wrap up, wrap-up wait
//     timeout) -> applyIntentControl (task_control.go) -> controlIntent (server.go:1236) ->
//     writeErr 409. User-only; never reaches the agent tool (actool) path.
//   - The two runDetachedIntent errors (Worker not ready, resume CAS conflict) ->
//     sendWorkerMessage (intent_intervention.go:147) -> writeErr. User-only.
//   - Conversely, the Chinese in SteerWork and KillWork (the steer_work and kill_work tools' actool.Errorf) and
//     transitionIntentState (internal state-transition logs) is brain input / logs and is preserved; it is not
//     the target of this test.

// TestControlWorkNoRunningWorkErrorLocalized drives, without the DB or engine, whether ControlWork
// really returns the Korean error when there is no running work. The run==nil branch only looks at the e.work map,
// so it is reached all the way through with NewEngine(nil). Because %w wraps the errWorkControlConflict sentinel,
// it also verifies that the errors.Is relationship is preserved. Fails if someone reverts this literal to Chinese.
func TestControlWorkNoRunningWorkErrorLocalized(t *testing.T) {
	e := NewEngine(nil)
	err := e.ControlWork(context.Background(), 42, "pause")
	if err == nil {
		t.Fatal("실행 중 work 가 없는데 ControlWork 가 nil 을 반환했습니다")
	}
	if !errors.Is(err, errWorkControlConflict) {
		t.Fatalf("errors.Is(err, errWorkControlConflict) = false, err=%v", err)
	}
	assertKoreanError(t, "control_work_no_running", err.Error())
}

// TestEngineUserFacingErrorsLocalized pins the remaining user-facing format strings that sit behind engine/DB gates or goroutine timing and are
// hard to drive all the way through (same approach as the paths behind the gates in finding_retests and
// finding_traffic). Format strings are filled with representative arguments before being checked.
func TestEngineUserFacingErrorsLocalized(t *testing.T) {
	for _, c := range []struct {
		label, msg string
	}{
		{"work_control_busy", fmt.Errorf(errWorkControlBusyFmt, errWorkControlConflict, int64(42), "pause").Error()},
		{"work_control_wait", fmt.Errorf(errWorkControlWaitFmt, int64(42), "pause", context.DeadlineExceeded).Error()},
		{"detached_worker_not_ready", errDetachedWorkerNotReady},
		{"detached_state_conflict", fmt.Errorf(errIntentCtrlStateConflictFmt, db.ErrIntentStateConflict).Error()},
	} {
		assertKoreanError(t, c.label, c.msg)
	}
}
