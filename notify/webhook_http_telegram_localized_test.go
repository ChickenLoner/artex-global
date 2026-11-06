package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// This file guards the Korean localization of F4 items 4 and 5 (telegram, webhook, and the shared HTTP transport layer) against regression.
// It catches it if the message labels and batch title in telegram.go, the config validation errors in webhook.go,
// the transport errors in http.go, and the validateHTTPURL shared by the five channels revert to
// Chinese. The helpers hasHan, hasHangul and assertKorean are in
// notify_localized_test.go (same package).
//
// The data (title, type, asset, overview) is all kept ASCII. That way the assertion of "zero Han characters in the whole output"
// precisely catches regressions only in the skeleton labels, not in the content. The severity and status
// labels are already Korean from F4 item 1, so they appear as Hangul in the output (not Han characters) and pass the assertion.

// TestTelegramItemLabelsLocalized turns on every label branch of a single Telegram message, then
// confirms the skeleton is Korean and contains no Chinese Han characters.
func TestTelegramItemLabelsLocalized(t *testing.T) {
	m := Message{
		HomeURL: "https://example.com/panel",
		Items: []Item{{
			Name:       "sqli-login", // Title() = Name
			VulnClass:  "injection",  // must differ from Title() so that the type line is rendered
			Severity:   "high",       // SeverityLabel → the Korean label for high (Hangul)
			Summary:    "login form is injectable",
			Assets:     []string{"host-a.example.com"},
			DetailURL:  "https://example.com/f/1",
			FromStatus: "pending", // IsStatusChange()=true → status-change line
			ToStatus:   "fixed",
		}},
	}
	out, kept := telegramHTML(m)
	if kept != 1 {
		t.Fatalf("단건 메시지는 1 을 보고해야 합니다, 받은 값 %d", kept)
	}
	if hasHan(out) {
		t.Errorf("Telegram 본문에 중국어 한자가 남아 있습니다:\n%s", out)
	}
	for _, want := range []string{"상태 변경", "유형", "자산", "개요", "상세 보기"} {
		if !strings.Contains(out, want) {
			t.Errorf("Telegram 본문에 %q 라벨이 없습니다:\n%s", want, out)
		}
	}
}

// TestTelegramBatchTitleLocalized checks that the overflow and time-window branches of the batch (digest) title are
// rendered in Korean. It must match markdownTitle down to the characters, otherwise the channels would end up
// mixed.
func TestTelegramBatchTitleLocalized(t *testing.T) {
	items := []Item{{Name: "a", Severity: "high"}, {Name: "b", Severity: "low"}}

	// With overflow + time window: summary, total count, first N items, remainder, next message, last N minutes.
	over := telegramBatchTitle(Message{WindowMinutes: 30}, items, 5)
	assertKorean(t, "telegramBatchTitle(넘침)", over)
	for _, want := range []string{"취약점 요약 · 총 5건", "앞 2건만 표시", "나머지 3건", "다음 메시지", "최근 30분간"} {
		if !strings.Contains(over, want) {
			t.Errorf("넘침 제목에 %q 가 없습니다: %q", want, over)
		}
	}

	// No overflow + no time window: there must be no next-message notice.
	full := telegramBatchTitle(Message{}, items, 2)
	assertKorean(t, "telegramBatchTitle(전량)", full)
	if !strings.Contains(full, "취약점 요약 · 총 2건") {
		t.Errorf("전량 제목에 '취약점 요약 · 총 2건' 이 있어야 합니다: %q", full)
	}
	if strings.Contains(full, "다음 메시지") {
		t.Errorf("넘치지 않았는데 '다음 메시지' 안내가 들어갔습니다: %q", full)
	}
}

// TestTelegramValidateLocalized checks that the missing-required-field error is Korean and tells which field is
// empty. The technical terms Bot Token and Chat ID are preserved as they are.
func TestTelegramValidateLocalized(t *testing.T) {
	if err := (telegramChannel{}).Validate(map[string]any{}); err == nil {
		t.Fatal("bot_token 누락은 검증 실패여야 합니다")
	} else {
		assertKorean(t, "telegram Validate(bot_token)", err.Error())
		if !strings.Contains(err.Error(), "Bot Token") {
			t.Errorf("Bot Token 누락을 알려줘야 합니다: %q", err.Error())
		}
	}
	if err := (telegramChannel{}).Validate(map[string]any{"bot_token": "t"}); err == nil {
		t.Fatal("chat_id 누락은 검증 실패여야 합니다")
	} else {
		assertKorean(t, "telegram Validate(chat_id)", err.Error())
		if !strings.Contains(err.Error(), "Chat ID") {
			t.Errorf("Chat ID 누락을 알려줘야 합니다: %q", err.Error())
		}
	}
}

// TestWebhookValidateLocalized checks that the three generic Webhook config validation errors are Korean and that
// each error tells the cause of the problem.
func TestWebhookValidateLocalized(t *testing.T) {
	if err := (webhookChannel{}).Validate(map[string]any{}); err == nil {
		t.Fatal("url 누락은 검증 실패여야 합니다")
	} else {
		assertKorean(t, "webhook Validate(url)", err.Error())
		if !strings.Contains(err.Error(), "대상 URL") {
			t.Errorf("대상 URL 누락을 알려줘야 합니다: %q", err.Error())
		}
	}
	// Unsupported method: Korean + the allowed list is shown.
	if err := (webhookChannel{}).Validate(map[string]any{"url": "https://example.com/hook", "method": "DELETE"}); err == nil {
		t.Fatal("DELETE 는 검증 실패여야 합니다")
	} else {
		assertKorean(t, "webhook Validate(method)", err.Error())
		if !strings.Contains(err.Error(), "GET/POST/PUT/PATCH") {
			t.Errorf("허용 메서드 목록을 알려줘야 합니다: %q", err.Error())
		}
	}
	// Template syntax error: Korean (the wrapped cause is a Go template error, so it contains no Han characters).
	if err := (webhookChannel{}).Validate(map[string]any{"url": "https://example.com/hook", "body_template": "{{"}); err == nil {
		t.Fatal("깨진 템플릿은 검증 실패여야 합니다")
	} else {
		assertKorean(t, "webhook Validate(template)", err.Error())
		if !strings.Contains(err.Error(), "템플릿") {
			t.Errorf("템플릿 오류임을 알려줘야 합니다: %q", err.Error())
		}
	}
}

// TestValidateHTTPURLLocalized checks that the errors of the URL validation helper shared by the five channels (telegram, webhook, dingtalk, feishu, wecom)
// are Korean. If this helper stayed in Chinese, it would mix with the Korean prefix (the Korean "invalid API address: ..." prefix)
// and end up half Korean and half Chinese.
func TestValidateHTTPURLLocalized(t *testing.T) {
	// Unsupported scheme.
	if err := validateHTTPURL("ftp://example.com/x"); err == nil {
		t.Fatal("ftp 스킴은 거부되어야 합니다")
	} else {
		assertKorean(t, "validateHTTPURL(scheme)", err.Error())
		if !strings.Contains(err.Error(), "http") {
			t.Errorf("지원 스킴(http/https)을 알려줘야 합니다: %q", err.Error())
		}
	}
	// No host name.
	if err := validateHTTPURL("http://"); err == nil {
		t.Fatal("호스트 없는 주소는 거부되어야 합니다")
	} else {
		assertKorean(t, "validateHTTPURL(host)", err.Error())
	}
}

// TestHTTPLocalTargetBlockedLocalized checks that the local/link-local address blocking error is Korean and
// tells the workaround (the environment variable).
func TestHTTPLocalTargetBlockedLocalized(t *testing.T) {
	t.Setenv(AllowLocalTargetsEnv, "") // explicitly turned off, to take the blocking path
	err := blockInternalDial("tcp", "127.0.0.1:25", nil)
	if err == nil {
		t.Fatal("로컬 주소는 기본적으로 차단되어야 합니다")
	}
	assertKorean(t, "blockInternalDial", err.Error())
	if !strings.Contains(err.Error(), AllowLocalTargetsEnv) {
		t.Errorf("차단 오류는 우회 방법(%s)을 알려줘야 합니다: %q", AllowLocalTargetsEnv, err.Error())
	}
}

// TestHTTPRedactHelpersLocalized checks that the address-redaction (redact) placeholder and the "unknown error"
// branch of the transport error are Korean.
func TestHTTPRedactHelpersLocalized(t *testing.T) {
	if got := redactRequestTarget(""); !strings.Contains(got, "해석할 수 없") {
		t.Errorf("해석 불가 주소는 한국어 자리표시자여야 합니다, 받은 값 %q", got)
	}
	// The branch where Err of *url.Error is nil.
	got := redactTransportError(&url.Error{Op: "Get", URL: "http://api.example", Err: nil})
	if hasHan(got) {
		t.Errorf("전송 오류 탈감 결과에 한자가 남았습니다: %q", got)
	}
	if !strings.Contains(got, "알 수 없는 오류") {
		t.Errorf("Err 가 nil 이면 '알 수 없는 오류' 여야 합니다, 받은 값 %q", got)
	}
}

// TestHTTPStatusErrorsLocalized uses a real doJSON round trip to check that the user-visible errors of the status-code classification (rejected/server
// error/throttled) are Korean. Transport to 127.0.0.1 httptest is blocked by default,
// so it is explicitly allowed only in this test.
func TestHTTPStatusErrorsLocalized(t *testing.T) {
	t.Setenv(AllowLocalTargetsEnv, "1")
	cases := []struct {
		code int
		want string
	}{
		{http.StatusForbidden, "요청을 거부했습니다"},
		{http.StatusInternalServerError, "오류가 발생했습니다"},
		{http.StatusTooManyRequests, "요청을 제한하거나"},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.code)
			_, _ = w.Write([]byte("body"))
		}))
		_, err := doJSON(context.Background(), http.MethodPost, srv.URL, nil, map[string]any{"a": 1})
		srv.Close()
		if err == nil {
			t.Fatalf("HTTP %d 는 오류여야 합니다", tc.code)
		}
		assertKorean(t, "doJSON(HTTP status)", err.Error())
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("HTTP %d 오류에 %q 가 있어야 합니다, 받은 값 %q", tc.code, tc.want, err.Error())
		}
	}
}
