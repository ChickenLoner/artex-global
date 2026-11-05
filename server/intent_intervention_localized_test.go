package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A regression-guard test that keeps the Worker intervention API error responses of intent_intervention.go in Korean.
// For the Korean check it reuses assertKoreanError built by F3a (contains Hangul, zero Chinese Han characters).

// TestIntentInterventionErrorConstantsLocalized asserts that all 11 response constants are Korean.
// The lifecycle and intent-state paths need s.engine and Store setup and cannot run to the end on this DB-less host,
// so for those texts the constants themselves are asserted (precedent: conversations.go). The 4 input-validation paths are
// checked down to the response body by the HTTP test below.
func TestIntentInterventionErrorConstantsLocalized(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{"request_too_large", errIntentRequestTooLarge},
		{"message_empty", errIntentMessageEmpty},
		{"message_too_long", errIntentMessageTooLong},
		{"bad_request_id", errIntentBadRequestID},
		{"task_deleting", errIntentTaskDeleting},
		{"task_paused", errIntentTaskPaused},
		{"task_queued", errIntentTaskQueued},
		{"task_terminal", errIntentTaskTerminal},
		{"task_settling", errIntentTaskSettling},
		{"inherited_readonly", errIntentInheritedReadonly},
		{"not_paused", errIntentNotPaused},
	}
	for _, c := range cases {
		assertKoreanError(t, c.name, c.msg)
	}
}

// TestSendWorkerMessageInputValidationLocalized checks the 4 input-validation paths down to the actual HTTP response
// body. These 4 paths of sendWorkerMessage look only at s.m.Task (a map lookup) and the request body and do not go through
// s.engine, Store or the DB, so putting a single task into the tasks map is enough for it to run to the end without a DB.
// It also checks the wiring by which the constants actually appear in the response.
func TestSendWorkerMessageInputValidationLocalized(t *testing.T) {
	s := &Server{m: &Manager{tasks: map[string]*Task{"t1": {ID: "t1"}}}}

	call := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/tasks/t1/intents/1/message", strings.NewReader(body))
		req.SetPathValue("id", "t1")
		req.SetPathValue("iid", "1")
		rec := httptest.NewRecorder()
		s.sendWorkerMessage(rec, req)
		return rec
	}
	errBody := func(t *testing.T, rec *httptest.ResponseRecorder) string {
		t.Helper()
		var resp struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("응답 JSON 파싱 실패: %v (본문 %q)", err, rec.Body.String())
		}
		return resp.Error
	}

	cases := []struct {
		name string
		body string
		code int
		want string
	}{
		// A valid JSON body exceeding the 64KB limit. MaxBytesReader returns the over-limit error mid-read.
		{"request_too_large", `{"message":"` + strings.Repeat("a", maxWorkerMessageBytes+1024) + `"}`, http.StatusRequestEntityTooLarge, errIntentRequestTooLarge},
		{"message_empty", `{"message":"  "}`, http.StatusBadRequest, errIntentMessageEmpty},
		{"message_too_long", `{"message":"` + strings.Repeat("가", 4001) + `"}`, http.StatusBadRequest, errIntentMessageTooLong},
		{"bad_request_id", `{"message":"안녕","request_id":"공백 포함"}`, http.StatusBadRequest, errIntentBadRequestID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := call(c.body)
			if rec.Code != c.code {
				t.Fatalf("상태 코드 = %d, 기대 = %d (본문 %q)", rec.Code, c.code, rec.Body.String())
			}
			if got := errBody(t, rec); got != c.want {
				t.Fatalf("응답 문구 = %q, 기대 = %q", got, c.want)
			}
			assertKoreanError(t, c.name, errBody(t, rec))
		})
	}
}
