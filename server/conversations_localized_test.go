package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A regression-guard test that keeps the error responses of the conversation (chat) API in conversations.go in Korean.
// The Korean check reuses assertKoreanError created by F3a (contains Hangul, zero Chinese Han characters).

// TestConversationErrorConstantsLocalized asserts that all 12 response constants are Korean.
// Handlers go through s.pg(w) (DB) first and cannot run to the end on this host without a DB, so the constants themselves are
// asserted (precedent in auth.go). Format strings containing %d are filled with real arguments and the final text is checked.
func TestConversationErrorConstantsLocalized(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{"request_too_large", convErrRequestTooLarge},
		{"agent_key_empty", convErrAgentKeyEmpty},
		{"agent_key_too_long", fmt.Sprintf(convErrAgentKeyTooLong, maxConversationAgentKeyRunes)},
		{"agent_not_found", convErrAgentNotFound},
		{"llm_profile", convErrLLMProfile},
		{"title_too_long", fmt.Sprintf(convErrTitleTooLong, maxConversationTitleRunes)},
		{"title_or_pinned", convErrTitleOrPinned},
		{"title_empty", convErrTitleEmpty},
		{"bad_conv_id", convErrBadConvID},
		{"ids_count", fmt.Sprintf(convErrIDsCount, maxConversationDeleteBatch)},
		{"message_empty", convErrMessageEmpty},
		{"busy", convErrBusy},
	}
	for _, c := range cases {
		assertKoreanError(t, c.name, c.msg)
	}
}

// TestDecodeConversationRequestTooLargeLocalized checks the request-body-too-large path all the way to the actual HTTP response
// body. decodeConversationRequest is a package function that does not go through Server (DB),
// so it can run without a DB, and it also verifies the wiring that puts the constant into the response (request body too large →
// 413 + Korean text).
func TestDecodeConversationRequestTooLargeLocalized(t *testing.T) {
	// A valid JSON body exceeding the 64KB limit. MaxBytesReader returns the limit-exceeded error partway through reading.
	body := `{"title":"` + strings.Repeat("a", maxConversationRequestBytes+1024) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/conversations", strings.NewReader(body))
	rec := httptest.NewRecorder()
	var dst struct {
		Title string `json:"title"`
	}
	if decodeConversationRequest(rec, req, &dst) {
		t.Fatal("본문이 한도를 넘었는데 decodeConversationRequest 가 true 를 돌려줬다")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("상태 코드 = %d, 기대 = %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	var resp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("응답 JSON 파싱 실패: %v (본문=%q)", err, rec.Body.String())
	}
	if resp.Error != convErrRequestTooLarge {
		t.Fatalf("응답 error = %q, 기대 = %q", resp.Error, convErrRequestTooLarge)
	}
	assertKoreanError(t, "decode.too_large.response", resp.Error)
}

// TestConversationDefaultTitlesLocalized asserts that the two default conversation title constants (F8) are Korean and distinct
// from each other. convDefaultTitle is both the creation default and the sentinel of the auto-title branch, so
// if it reverts to the Chinese "New chat", the user would see Chinese in the conversation list and delete dialog.
func TestConversationDefaultTitlesLocalized(t *testing.T) {
	assertKoreanError(t, "default_title", convDefaultTitle)
	assertKoreanError(t, "attachment_title", convAttachmentTitle)
	if convDefaultTitle == convAttachmentTitle {
		t.Fatal("기본 제목과 첨부 기본 제목이 같으면 안 된다")
	}
}

// TestIsDefaultConversationTitle pins down the decision of the auto-title branch. The creation default
// (convDefaultTitle) and an empty title are eligible for auto-titling, while a title the user chose is not. The creation
// default and the sentinel are the same constant, which prevents a regression where the two drift apart and auto-titling is not applied.
func TestIsDefaultConversationTitle(t *testing.T) {
	if !isDefaultConversationTitle("") {
		t.Fatal("빈 제목은 자동 제목 대상이어야 한다")
	}
	if !isDefaultConversationTitle(convDefaultTitle) {
		t.Fatalf("생성 기본값 %q 는 자동 제목 대상이어야 한다", convDefaultTitle)
	}
	if isDefaultConversationTitle("사용자가 지은 제목") {
		t.Fatal("사용자가 지은 제목은 자동 제목 대상이 아니어야 한다")
	}
}

// TestConversationRetestReasonsLocalized asserts that the three retest terminal-reason constants (F9) are Korean.
// These values are stored in the finding_retests.error column and shown as item.error in the retest panel,
// so if they revert to Chinese the user would see Chinese reasons in the panel.
func TestConversationRetestReasonsLocalized(t *testing.T) {
	assertKoreanError(t, "retest_failed_to_start", convRetestFailedToStart)
	assertKoreanError(t, "retest_status_read_failed", convRetestStatusReadFailed)
	assertKoreanError(t, "retest_stopped_or_closed", convRetestStoppedOrClosed)
}

// TestTranscriptErrorSummaryLocalized pins down the activity-transcript error wrapper (F9). The chat turn
// (no label) and the task main agent (the Korean "main agent" label) calls come out in the same "(… error: …)" shape,
// the inner err original text is preserved as it is, and the wrapper must contain no Chinese Han characters or full-width punctuation.
func TestTranscriptErrorSummaryLocalized(t *testing.T) {
	cases := []struct {
		name  string
		label string
		errm  string
		want  string
	}{
		{"chat_turn", "", "connection reset", "(오류: connection reset)"},
		{"main_agent", "메인 에이전트", "connection reset", "(메인 에이전트 오류: connection reset)"},
	}
	for _, c := range cases {
		got := transcriptErrorSummary(c.label, c.errm)
		if got != c.want {
			t.Fatalf("%s: transcriptErrorSummary = %q, 기대 = %q", c.name, got, c.want)
		}
		if !strings.Contains(got, c.errm) {
			t.Fatalf("%s: err 원문이 보존되지 않았습니다: %q", c.name, got)
		}
		// Check that the full-width parentheses and colon were replaced with ASCII (F1, fluent-korean policy).
		if strings.ContainsAny(got, "（）：") {
			t.Fatalf("%s: 전각 부호가 남아 있습니다: %q", c.name, got)
		}
		// The wrapper label must contain Hangul and no Chinese Han characters (the err original text is not the subject of the check and is
		// fixed as ASCII). The chat turn without a label also contains the Hangul word for "error".
		wrapper := strings.ReplaceAll(got, c.errm, "")
		assertKoreanError(t, c.name+".wrapper", wrapper)
	}
}
