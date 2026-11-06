package notify

import (
	"fmt"
	"strings"
)

// This file holds the message rendering shared by the "Markdown family" channels (DingTalk, WeCom).
// Feishu uses card JSON, Telegram uses HTML, and email uses HTML; each renders in its own adapter.

// maxAssetsShown is the maximum number of assets listed in a message. One vulnerability may anchor dozens of assets;
// listing them all would blow up the message with no informational value -- nobody reads the 4th-and-later domains in an IM.
const maxAssetsShown = 3

// maxSummaryRunes is the number of characters a summary is compressed to. An IM message is a "pointer to the details",
// not the report itself; the full content lives in the platform.
const maxSummaryRunes = 120

// markdownReservedBytes is reserved for the message header (summary line + severity distribution + possible truncation notice)
// and the footer (platform link). When packing the whole message, this part is deducted from the budget so the header and footer are never cut off --
// once they are cut, the reader cannot even tell "which batch this is and how many items were not shown".
const markdownReservedBytes = 320

// markdownEscape escapes markdown metacharacters.
//
// Why it is required: vulnerability titles, summaries, types and asset display names all come from **untrusted sources** --
// titles and summaries come from model output (the model reads responses from the target under test), and an asset's url is the full URL
// obtained by scanning (including target-controlled query strings). Without escaping, a vulnerability whose title is
//
//	Login SQL injection\n[URGENT: click to verify account](http://attacker.tld)
//
// would be rendered as a **clickable external link** in the security engineer's DingTalk/Feishu; and
// `![](http://attacker.tld/beacon)` would be fetched by the client at render time, which amounts to reporting
// "this vulnerability has been viewed" and leaking the reader's IP. Even for non-malicious content, injected bold or
// blockquotes can push the serious vulnerabilities below the fold.
//
// The escape set covers the characters of the heading/link/emphasis/list/quote/strikethrough classes that change structure or produce clickable
// elements. `\` must be handled first, otherwise it would re-escape the backslashes added afterwards.
func markdownEscape(s string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		"`", "\\`",
		"*", `\*`,
		"_", `\_`,
		"[", `\[`,
		"]", `\]`,
		"(", `\(`,
		")", `\)`,
		"!", `\!`,
		"#", `\#`,
		">", `\>`,
		"|", `\|`,
		"~", `\~`,
	)
	return replacer.Replace(s)
}

// markdownText flattens untrusted text to a single line and escapes it, for use in the markdown body.
// Flattening to one line is the other half beyond escaping: a newline alone can forge a new list item or blockquote,
// and escape characters cannot stop it.
func markdownText(s string, maxRunes int) string {
	return markdownEscape(OneLine(s, maxRunes))
}

// markdownTitle returns the message title (the IM platform's title bar / card title); the content is the **unescaped original text**.
//
// Escaping is deliberately not done here: this title is shared by renderers in four contexts -- the markdown body, Telegram's
// HTML, Feishu card plain_text, and the generic Webhook's JSON and email subject. Each context's
// escaping rules differ (markdown escapes put into HTML leave visible backslashes, and put into JSON they pollute the
// data), so escaping must be done by each output side, see writeItem / feishuItemLines /
// telegramEscape. Markdown escaping was once added in the shared function, and the result was visible backslashes like `\(1\)`
// showing up in Telegram messages.
func markdownTitle(m Message) string {
	if m.Batch {
		return fmt.Sprintf("취약점 요약 · 총 %d건", len(m.Items))
	}
	if len(m.Items) == 0 {
		return "취약점 알림"
	}
	it := m.Items[0]
	return fmt.Sprintf("[%s] %s", SeverityLabel(it.Severity), OneLine(it.Title(), 0))
}

// markdownBody renders the message body, returning the body and the **number of items actually written**.
//
// The return value kept is the number of items actually delivered this time; the caller uses it to mark only the first kept items
// as delivered -- items kept out by the channel length limit must wait for the next batch rather than being
// marked successful along with the rest. This is the source of "silent loss": the message was truncated, but the delivery record shows everything delivered,
// and nothing shows that the second half was never sent.
//
// maxBytes<=0 means unlimited.
func markdownBody(m Message, maxBytes int) (string, int) {
	if !m.Batch {
		if len(m.Items) == 0 {
			return "", 0
		}
		var b strings.Builder
		writeItem(&b, m.Items[0], "", true)
		// A single message is sent even if over-long (the final truncation is the safety net): partial information about one vulnerability
		// is better than sending none.
		return TruncateBytes(b.String(), maxBytes), 1
	}

	footer := ""
	if m.HomeURL != "" {
		footer = fmt.Sprintf("\n[플랫폼에서 전체 보기](%s)\n", m.HomeURL)
	}
	kept := packItemCount(m.Items, maxBytes, markdownReservedBytes, footer, byteSize, func(it Item, idx int) string {
		var b strings.Builder
		writeItem(&b, it, fmt.Sprintf("%d. ", idx+1), false)
		return b.String()
	})

	items := m.Items[:kept]
	var b strings.Builder
	b.WriteString(markdownBatchIntro(m, items, len(m.Items)))
	for i, it := range items {
		writeItem(&b, it, fmt.Sprintf("%d. ", i+1), false)
	}
	b.WriteString(footer)
	return TruncateBytes(b.String(), maxBytes), kept
}

// markdownBatchIntro renders the opening of a summary message: time window, item count and severity distribution.
// With these, the recipient of a summary can judge whether the batch needs immediate handling without opening the platform.
//
// items are the entries **actually fitted in**, total is the batch's full count. When they differ, it must say explicitly
// "how many more are in the next message" -- otherwise the reader assumes the number in the header is everything,
// and the entries that were never sent do not exist at all in the UI.
func markdownBatchIntro(m Message, items []Item, total int) string {
	var b strings.Builder
	if m.WindowMinutes > 0 {
		fmt.Fprintf(&b, "**최근 %d분간 신규 취약점 %d건**", m.WindowMinutes, total)
	} else {
		fmt.Fprintf(&b, "**신규 취약점 %d건**", total)
	}
	if extra := total - len(items); extra > 0 {
		fmt.Fprintf(&b, "(이 메시지에는 앞 %d건만 표시하며, 나머지 %d건은 다음 메시지에서 이어서 보냅니다)", len(items), extra)
	}
	// Give the distribution by severity so the reader sees at a glance whether there are serious items. Only count the entries **actually included in
	// this message**, so that "critical 3" matches the items that can be counted below.
	counts := map[string]int{}
	for _, it := range items {
		counts[it.Severity]++
	}
	var parts []string
	for _, sev := range []string{"critical", "high", "medium", "low"} {
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", SeverityLabel(sev), n))
		}
	}
	if len(parts) > 0 {
		b.WriteString("\n" + strings.Join(parts, " · "))
	}
	b.WriteString("\n\n")
	return b.String()
}

// writeItem renders a single vulnerability entry.
//
// prefix is the sequence number for summary lists; single=true renders the full version (with summary and back-link),
// while a summary list renders only a one-line digest -- otherwise a 50-item summary would turn into a long document.
//
// All externally sourced content (title/type/asset/summary) passes through markdownText:
// flattening + escaping. The back-link is built from the admin-configured public_base_url, is not untrusted content,
// and must be a clickable link, so it is output as is.
func writeItem(b *strings.Builder, it Item, prefix string, single bool) {
	line := fmt.Sprintf("%s**%s · %s**", prefix, SeverityLabel(it.Severity), markdownText(it.Title(), 0))
	if !single {
		// Summary mode: presented on a single line, with assets and summary compressed after it.
		var extras []string
		if a := assetLine(it.Assets, maxAssetsShown); a != "" {
			extras = append(extras, markdownText(a, 0))
		}
		if it.Summary != "" {
			extras = append(extras, markdownText(it.Summary, 60))
		}
		if len(extras) > 0 {
			line += " — " + strings.Join(extras, " · ")
		}
		b.WriteString(line + "\n")
		return
	}
	b.WriteString(line + "\n")
	if it.IsStatusChange() {
		fmt.Fprintf(b, "**상태 변경**: %s → %s\n",
			markdownText(StatusLabel(it.FromStatus), 0), markdownText(StatusLabel(it.ToStatus), 0))
	}
	if it.VulnClass != "" && it.VulnClass != it.Title() {
		fmt.Fprintf(b, "**유형**: %s\n", markdownText(it.VulnClass, 0))
	}
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		fmt.Fprintf(b, "**자산**: %s\n", markdownText(a, 0))
	}
	if it.Summary != "" {
		if s := markdownText(it.Summary, maxSummaryRunes); s != "" {
			fmt.Fprintf(b, "**개요**: %s\n", s)
		}
	}
	if it.DetailURL != "" {
		fmt.Fprintf(b, "[상세 보기](%s)\n", it.DetailURL)
	}
}
