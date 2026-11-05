package notify

import (
	"strings"
	"testing"
)

// hasHan returns true if the string contains even one CJK unified Han character.
// If a translated label reverts to Chinese (regression), this check catches it.
func hasHan(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// hasHangul returns true if the string contains even one Hangul syllable.
func hasHangul(s string) bool {
	for _, r := range s {
		if r >= 0xAC00 && r <= 0xD7A3 {
			return true
		}
	}
	return false
}

// assertKorean asserts that a label contains Hangul and no Chinese Han characters.
func assertKorean(t *testing.T, where, got string) {
	t.Helper()
	if hasHan(got) {
		t.Errorf("%s: 중국어 한자가 남아 있습니다: %q", where, got)
	}
	if !hasHangul(got) {
		t.Errorf("%s: 한글이 없습니다: %q", where, got)
	}
}

// TestSeverityLabelLocalized checks that every severity enum comes out as a Korean label.
// The labels are shared by all notification channels (telegram, email, html, markdown, webhook, feishu, dingtalk, wecom),
// so if any one reverts to Chinese, messages on all channels become mixed.
func TestSeverityLabelLocalized(t *testing.T) {
	// Chinese labels (critical/high/medium/low) → Korean (critical/high/medium/low), consistent with UI status.severity.
	want := map[string]string{
		"critical": "심각",
		"high":     "높음",
		"medium":   "중간",
		"low":      "낮음",
	}
	for sev, label := range want {
		got := SeverityLabel(sev)
		assertKorean(t, "SeverityLabel("+sev+")", got)
		if !strings.Contains(got, label) {
			t.Errorf("SeverityLabel(%q)=%q, %q 를 포함해야 합니다", sev, got, label)
		}
	}
	// An unknown severity returns the original text as is (does not invent values that do not exist).
	if got := SeverityLabel("made_up"); got != "made_up" {
		t.Errorf("알 수 없는 심각도는 원문 유지여야 합니다, 받은 값 %q", got)
	}
}

// TestStatusLabelLocalized checks that all 9 handling statuses come out in Korean.
// They must be spelled identically to the UI status.finding namespace (B4a) so that status-change notifications and the screen do not disagree.
func TestStatusLabelLocalized(t *testing.T) {
	want := map[string]string{
		"pending":        "처리 대기",
		"in_progress":    "처리 중",
		"confirmed":      "확인됨",
		"resolved":       "처리됨",
		"fixed":          "수정됨",
		"false_positive": "오탐",
		"ignored":        "무시",
		"duplicate":      "중복",
		"risk_accepted":  "위험 수용",
	}
	for status, label := range want {
		got := StatusLabel(status)
		assertKorean(t, "StatusLabel("+status+")", got)
		if got != label {
			t.Errorf("StatusLabel(%q)=%q, %q 를 기대했습니다", status, got, label)
		}
	}
	// An unknown status is returned as is (does not invent labels that do not exist).
	if got := StatusLabel("weird_status"); got != "weird_status" {
		t.Errorf("알 수 없는 상태는 원문 유지여야 합니다, 받은 값 %q", got)
	}
}

// TestItemTitlePlaceholderLocalized checks that the fallback title of an item with both name and type empty
// is the Korean placeholder ("unnamed vulnerability"). An empty title is never emitted.
func TestItemTitlePlaceholderLocalized(t *testing.T) {
	got := Item{}.Title()
	assertKorean(t, "Item{}.Title()", got)
	// If a name exists, use the name; if only a type exists, prefer the type (confirms the fallback logic does not regress).
	if n := (Item{Name: "로그인 SQLi"}).Title(); n != "로그인 SQLi" {
		t.Errorf("이름 우선 로직이 깨졌습니다, 받은 값 %q", n)
	}
	if v := (Item{VulnClass: "XSS"}).Title(); v != "XSS" {
		t.Errorf("유형 대체 로직이 깨졌습니다, 받은 값 %q", v)
	}
}

// TestAssetLineLocalized checks that the separator and total-count notation of the asset listing wording are Korean/ASCII.
// If Chinese punctuation (the enumeration comma) or the Chinese "etc." / counter characters come back, the notification body becomes mixed.
func TestAssetLineLocalized(t *testing.T) {
	// At or below the limit: list all, ASCII comma separator.
	if got := assetLine([]string{"a.example.com", "b.example.com"}, 3); got != "a.example.com, b.example.com" {
		t.Errorf("구분자가 ASCII 쉼표여야 합니다, 받은 값 %q", got)
	}
	// Above the limit: the first limit items + Korean "and N more" (N is the total count).
	got := assetLine([]string{"a", "b", "c", "d", "e"}, 2)
	if hasHan(got) {
		t.Errorf("자산 나열에 중국어 한자가 남았습니다: %q", got)
	}
	if !strings.Contains(got, "등 5개") {
		t.Errorf("전체 개수 5 를 '등 5개'로 표기해야 합니다, 받은 값 %q", got)
	}
}
