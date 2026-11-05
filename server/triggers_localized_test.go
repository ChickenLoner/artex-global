package server

import "testing"

// A regression-guard test that keeps the trigger config validation error text in triggers.go in Korean.
// The Korean check reuses assertKoreanError created by F3a (contains Hangul, zero Chinese Han characters).

// TestTriggerErrorConstantsLocalized asserts that all 3 error constants are Korean.
func TestTriggerErrorConstantsLocalized(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{"no_condition", errTriggerNoCondition},
		{"tool_set_empty", errTriggerToolSetEmpty},
		{"custom_only", errTriggerCustomOnly},
	}
	for _, c := range cases {
		assertKoreanError(t, c.name, c.msg)
	}
}

// TestValidateTriggerLocalized actually calls the pure function validateTrigger and confirms that the missing-condition and no-tool-
// selected paths return the Korean constants and that valid input returns an empty string. This return value is
// exposed as-is to the user by pgCreateTrigger and pgUpdateTrigger via writeErr(400, msg).
func TestValidateTriggerLocalized(t *testing.T) {
	if msg := validateTrigger(&triggerReq{}); msg != errTriggerNoCondition {
		t.Fatalf("조건 누락 문구 = %q, 기대 = %q", msg, errTriggerNoCondition)
	}
	if msg := validateTrigger(&triggerReq{OnToolCall: true}); msg != errTriggerToolSetEmpty {
		t.Fatalf("도구 미선택 문구 = %q, 기대 = %q", msg, errTriggerToolSetEmpty)
	}
	if msg := validateTrigger(&triggerReq{OnFinding: true}); msg != "" {
		t.Fatalf("유효 입력 문구 = %q, 기대 = 빈 문자열", msg)
	}
	if msg := validateTrigger(&triggerReq{OnToolCall: true, ToolNames: []string{"nmap"}}); msg != "" {
		t.Fatalf("유효 도구 호출 문구 = %q, 기대 = 빈 문자열", msg)
	}
}
