package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateBytesKeepsValidUTF8(t *testing.T) {
	// This is the most important invariant in this package. WeCom limits length by **bytes**, and Chinese is 3 bytes per character,
	// so any implementation that hard-cuts by bytes would split a Chinese character in half, producing illegal UTF-8 that the platform rejects.
	// Mixed Chinese/English inputs of coprime lengths are used to hit every possible cut point.
	inputs := []string{
		"中文测试内容",
		"混合 mixed 内容 content",
		"a中b文c测d试e",
		"🔴🟠🟡🔵", // 4-byte emoji, a wrong cut is more obvious
		strings.Repeat("漏洞", 100),
	}
	for _, in := range inputs {
		for max := 1; max <= len(in)+2; max++ {
			got := TruncateBytes(in, max)
			if !utf8.ValidString(got) {
				t.Fatalf("输入 %q max=%d: 产出非法 UTF-8 %q", in, max, got)
			}
			if len(got) > max {
				t.Fatalf("输入 %q max=%d: 结果 %d 字节超出上限", in, max, len(got))
			}
			// Content must not be altered when it was not truncated.
			if len(in) <= max && got != in {
				t.Fatalf("输入 %q max=%d: 未超限却改动了内容 -> %q", in, max, got)
			}
		}
	}
}

func TestTruncateBytesZeroMeansUnlimited(t *testing.T) {
	long := strings.Repeat("x", 10000)
	if got := TruncateBytes(long, 0); got != long {
		t.Fatal("max=0 应表示不限制")
	}
	if got := TruncateBytes(long, -5); got != long {
		t.Fatal("max<0 应表示不限制")
	}
}

func TestTruncateBytesEllipsisBudget(t *testing.T) {
	// When max is smaller than the ellipsis itself, appending the ellipsis must not push it over the limit.
	got := TruncateBytes("abcdefgh", 1)
	if len(got) > 1 {
		t.Fatalf("max=1 时结果 %q 长度 %d 超限", got, len(got))
	}
	// Normally it should carry an ellipsis.
	if got := TruncateBytes("abcdefgh", 5); !strings.HasSuffix(got, ellipsis) {
		t.Fatalf("期望带省略号，得到 %q", got)
	}
}

func TestTruncateRunesCountsCharactersNotBytes(t *testing.T) {
	// The difference from TruncateBytes' measure must be preserved: Telegram limits by characters,
	// and using the byte measure would cut a Chinese message down to one third.
	s := "一二三四五六七八九十"
	got := TruncateRunes(s, 5)
	if n := utf8.RuneCountInString(got); n != 5 {
		t.Fatalf("期望 5 个字符，得到 %d 个 (%q)", n, got)
	}
	// The same string should be clearly shorter under the byte measure.
	if utf8.RuneCountInString(TruncateBytes(s, 5)) >= 5 {
		t.Fatal("字节口径不应产出与字符口径相同的字符数")
	}
}

func TestOneLineCollapsesWhitespace(t *testing.T) {
	got := OneLine("第一行\n\n第二行\t带制表   多空格", 0)
	if strings.ContainsAny(got, "\n\t") {
		t.Fatalf("应折叠所有空白，得到 %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Fatalf("不应保留连续空格，得到 %q", got)
	}
	// After truncation it must still be readable and valid.
	got = OneLine("一二三四五六七八九十", 4)
	if n := utf8.RuneCountInString(got); n != 4 {
		t.Fatalf("期望 4 字符，得到 %d (%q)", n, got)
	}
}

func TestTruncateHTMLNeverCutsTagInHalf(t *testing.T) {
	// Truncating HTML directly would cut out fragments like `<a href="htt`, and the platform would reject the whole message.
	s := `<b>标题</b>正文正文正文<a href="https://example.com/very/long/path">查看详情</a>`
	for max := 1; max <= utf8.RuneCountInString(s)+2; max++ {
		got := TruncateHTML(s, max)
		if n := utf8.RuneCountInString(got); max > 0 && n > max {
			t.Fatalf("max=%d: 结果 %d 字符超限", max, n)
		}
		// There must be no unclosed `<` at the tail (i.e. a `<` in the last segment with no `>`).
		if lt := strings.LastIndex(got, "<"); lt >= 0 && !strings.Contains(got[lt:], ">") {
			t.Fatalf("max=%d: 尾部标签被切断 -> %q", max, got)
		}
	}
}

func TestAssetLineOmitsExcess(t *testing.T) {
	if got := assetLine(nil, 3); got != "" {
		t.Fatalf("无资产应返回空串，得到 %q", got)
	}
	if got := assetLine([]string{"a", "b"}, 3); got != "a, b" {
		t.Fatalf("未超限应全列，得到 %q", got)
	}
	// When the limit is exceeded the total count must be stated, otherwise readers do not know how many assets are not listed.
	got := assetLine([]string{"a", "b", "c", "d", "e"}, 2)
	if !strings.Contains(got, "등 5개") {
		t.Fatalf("应标注总数 5，得到 %q", got)
	}
}

func TestSeverityAndStatusLabels(t *testing.T) {
	if AtLeast("", "low") {
		t.Fatal("空级别序数为 0，应被任何门槛挡住")
	}
	if !AtLeast("critical", "") {
		t.Fatal("空门槛应放行")
	}
	if got := StatusLabel("fixed"); got != "수정됨" {
		t.Fatalf("未知状态映射，得到 %q", got)
	}
	// An unknown status is echoed as is, without inventing a label.
	if got := StatusLabel("weird_status"); got != "weird_status" {
		t.Fatalf("未知状态应原样回显，得到 %q", got)
	}
}

// TestTruncateHTMLNeverCutsEntity covers an omission pointed out by the audit: truncation must avoid not only
// half tags but also HTML entities that get cut.
//
// After `&amp;` is cut into `&amp`, a parser that only recognizes entities may reject the **whole** message -
// and overlong digest messages are common to begin with, so the cost is too high.
func TestTruncateHTMLNeverCutsEntity(t *testing.T) {
	s := "aaaa&amp;bbbb&lt;cccc&quot;dddd"
	for max := 1; max <= utf8.RuneCountInString(s)+2; max++ {
		got := TruncateHTML(s, max)
		// There must be no entity fragment at the tail that has an & without a matching ;.
		if amp := strings.LastIndex(got, "&"); amp >= 0 && !strings.Contains(got[amp:], ";") {
			t.Fatalf("max=%d: 尾部留下实体残片 %q", max, got[amp:])
		}
		if strings.Contains(got, "&amp\x00") {
			t.Fatalf("max=%d: 出现畸形实体", max)
		}
	}
}
