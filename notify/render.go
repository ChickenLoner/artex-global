package notify

import (
	"strings"
	"unicode/utf8"
)

const ellipsis = "…"

// TruncateBytes truncates s to at most max bytes, guaranteeing the result is valid UTF-8 and no character is cut.
//
// Why it must cut on a character boundary: the WeCom group robot's markdown has a hard limit of 4096 **bytes** (not
// characters), and a Chinese character is 3 bytes. Slicing by bytes would cut a Chinese character in half and produce invalid
// UTF-8 — the platform either rejects the whole message or shows garbled boxes. The approach here is to step back from the budget position
// to the nearest rune start byte (utf8.RuneStart identifies continuation bytes 0b10xxxxxx).
//
// max<=0 means no limit. An ellipsis is appended after truncation, unless max is too small to fit the ellipsis.
func TruncateBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	budget := max - len(ellipsis)
	suffix := ellipsis
	if budget < 0 {
		// max is even shorter than the ellipsis: drop the ellipsis and truncate plainly, so the result does not exceed max.
		budget = max
		suffix = ""
	}
	cut := budget
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}

// OneLine squeezes multi-line text into a single line: collapses all whitespace, then truncates by character count.
// Used for the title line of IM messages — summaries often contain newlines, and putting them straight into a table/title breaks the layout.
// max<=0 means no length limit.
func OneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	return TruncateRunes(s, max)
}

// TruncateRunes truncates s to at most max characters (not bytes), appending an ellipsis when it overflows.
// max<=0 means no limit.
//
// The difference from TruncateBytes is the platform's measuring convention: WeCom limits length by bytes, Telegram by characters.
// Using the wrong convention raises no error; it just makes messages get cut much shorter than expected (a Chinese character = 3 bytes,
// so cutting at 4096 bytes leaves only about 1365 characters), so both functions must be kept and chosen per channel.
func TruncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + ellipsis
}

// TruncateHTML truncates an HTML fragment by character count, guaranteeing no half tag is produced.
//
// Truncating HTML by characters directly can leave a broken tag such as `<a href="htt`, and the platform parser either
// errors out and rejects the whole message, or swallows the following body text as an attribute value. The approach here: truncate by characters first,
// then check whether the tail has an unclosed `<`, and if so step back to before it.
//
// No tag balancing (adding </b> and the like): Telegram's HTML parser auto-closes unclosed tags,
// and implementing balancing ourselves would have to handle quotes in attributes, comments and self-closing tags — complexity out of proportion to the benefit.
func TruncateHTML(s string, max int) string {
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	cut := TruncateRunes(s, max)
	// If the tail is a fragment starting with `<` (no `>` after the last `<`), step back to before the `<`.
	if lt := strings.LastIndex(cut, "<"); lt >= 0 && !strings.Contains(cut[lt:], ">") {
		cut = cut[:lt]
	}
	// If the tail is a truncated HTML entity (such as `&amp;` cut into `&amp`), step back as well.
	// An entity fragment can get the **whole message** rejected by a parser that only accepts entities — a digest message over the
	// length limit is common anyway, and is not worth losing the whole notification over.
	if amp := strings.LastIndex(cut, "&"); amp >= 0 && !strings.Contains(cut[amp:], ";") {
		cut = cut[:amp]
	}
	return cut
}

// packItemCount computes how many items fit **completely** within the budget, so digest messages are packed by whole items.
//
// Why pack by whole items rather than render everything and then truncate: truncation makes the trailing items vanish without a trace,
// while their delivery records would still be marked as delivered — not visible in the message and not visible in the delivery history,
// and the findings are simply lost. With whole-item packing, items that do not fit stay in the DB as the next batch,
// and the kept the caller gets is exactly the number of items this message really delivered.
//
// Parameters: maxSize<=0 means no limit; reserve is the amount reserved for the message header/footer;
// size does the measuring (platform conventions differ: WeCom/DingTalk by bytes, Telegram by characters —
// the wrong convention raises no error; it just squeezes Chinese messages far below the limit);
// render renders item idx into its actual text — the length varies with content and cannot be estimated.
//
// Returns at least 1 (as long as items remain). Even when a single item is extremely long it must still be sent, with the caller's
// final truncation as the backstop; otherwise one overlong finding would stall the whole batch permanently.
func packItemCount(items []Item, maxSize, reserve int, footer string, size func(string) int, render func(Item, int) string) int {
	if maxSize <= 0 {
		return len(items)
	}
	budget := maxSize - reserve - size(footer)
	if budget < 0 {
		budget = 0
	}
	used := 0
	for i, it := range items {
		used += size(render(it, i))
		if used > budget && i > 0 {
			return i
		}
	}
	return len(items)
}

// byteSize / runeSize are the two measuring conventions for packItemCount; they are named so call sites
// do not show a bare func(s string) int closure, which would make it hard to tell at a glance which convention is used.
func byteSize(s string) int { return len(s) }
func runeSize(s string) int { return utf8.RuneCountInString(s) }

// assetLine renders the asset list into one line of display text; beyond limit entries it omits the rest and notes the total.
// A finding may be anchored to dozens of assets, and listing them all would swamp the message.
func assetLine(assets []string, limit int) string {
	if len(assets) == 0 {
		return ""
	}
	if limit <= 0 || len(assets) <= limit {
		return strings.Join(assets, ", ")
	}
	return strings.Join(assets[:limit], ", ") + " 등 " + itoa(len(assets)) + "개"
}

// itoa is a short alias for strconv.Itoa, used only for assembling display text, to avoid importing strconv everywhere.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
