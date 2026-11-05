package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// telegramTextLimit is the upper limit of the text field of Telegram sendMessage (in characters).
const telegramTextLimit = 4096

// telegramChannel implements the Telegram Bot API.
//
// Platform characteristics:
//   - Authentication is entirely in the URL path (/bot<token>/sendMessage); no signing is needed.
//   - Uses HTML parse mode instead of MarkdownV2: MarkdownV2 requires escaping `_*[]()~`>#+-=|{}.!`
//     (18 characters in total), and missing one gets the whole message rejected; HTML only needs & < > escaped.
//   - Business errors are also hidden inside HTTP 200 and are determined by the ok field.
type telegramChannel struct{}

func (telegramChannel) Kind() string { return KindTelegram }

// Telegram allows about 1 message/second in a private chat and 20 messages/minute in a group. A conservative value is used.
func (telegramChannel) DefaultRatePerMin() int { return 20 }

// The Bot Token is the full credential; chat_id is only the recipient and is not a secret (without the Token, having it is not enough to send messages).
func (telegramChannel) SecretKeys() []string { return []string{"bot_token"} }

// base_url determines which API endpoint the Token is sent to (e.g. a self-hosted reverse proxy); changing it requires re-confirming the Token.
func (telegramChannel) DestinationKeys() []string { return []string{"base_url"} }

func (telegramChannel) Validate(cfg map[string]any) error {
	if cfgString(cfg, "bot_token") == "" {
		return errors.New("Bot Token이 없습니다")
	}
	if cfgString(cfg, "chat_id") == "" {
		return errors.New("Chat ID가 없습니다")
	}
	if base := cfgString(cfg, "base_url"); base != "" {
		if err := validateHTTPURL(base); err != nil {
			return fmt.Errorf("API 주소가 올바르지 않습니다: %w", err)
		}
	}
	return nil
}

func (c telegramChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	endpoint, err := telegramEndpoint(cfg)
	if err != nil {
		return 0, Permanent(err)
	}
	text, kept := telegramHTML(m)
	payload := map[string]any{
		"chat_id":                  cfgString(cfg, "chat_id"),
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": false,
	}
	raw, err := doJSON(ctx, "POST", endpoint, nil, payload)
	if err != nil {
		return 0, err
	}
	var res struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("Telegram 응답을 해석하지 못했습니다: %w (%s)", err, snippet(raw))
	}
	if res.OK {
		return kept, nil
	}
	// 429 is rate limiting, where retrying after backoff works; the rest (400 bad parameters, 401 bad token, 403 blocked,
	// 404 chat does not exist) are configuration problems that retrying will not fix by itself.
	if res.ErrorCode == 429 {
		return 0, fmt.Errorf("Telegram 요청 제한에 걸렸습니다: %s", res.Description)
	}
	return 0, Permanent(fmt.Errorf("Telegram 응답 오류 %d: %s", res.ErrorCode, res.Description))
}

// telegramEndpoint assembles the sendMessage address. When base_url is empty the official API is used;
// when non-empty it is used for a self-hosted Bot API reverse proxy (a common need on networks in China).
func telegramEndpoint(cfg map[string]any) (string, error) {
	base := cfgString(cfg, "base_url")
	if base == "" {
		base = "https://api.telegram.org"
	}
	base = strings.TrimSuffix(base, "/")
	token := cfgString(cfg, "bot_token")
	raw := base + "/bot" + token + "/sendMessage"
	u, err := url.Parse(raw)
	if err != nil {
		// Do not pass err through: the address contains the Bot Token, and not even addr should be echoed at this point.
		return "", fmt.Errorf("API 주소를 조합하지 못했습니다 (API 주소: %s)", redactRequestTarget(base))
	}
	return u.String(), nil
}

// telegramHTML renders the HTML body and returns the body and the number of entries actually written (see Channel.Send).
func telegramHTML(m Message) (string, int) {
	var b strings.Builder
	b.WriteString("<b>" + telegramEscape(markdownTitle(m)) + "</b>\n")
	if m.Batch {
		// Telegram's limit is in **characters**, so packing is also measured in characters (runeSize).
		footer := ""
		if m.HomeURL != "" {
			footer = fmt.Sprintf("\n\n<a href=\"%s\">플랫폼에서 전체 보기</a>", telegramEscapeAttr(m.HomeURL))
		}
		kept := packItemCount(m.Items, telegramTextLimit, telegramReservedRunes, footer, runeSize, func(it Item, idx int) string {
			return telegramBatchLine(it, idx+1)
		})
		items := m.Items[:kept]
		b.Reset()
		b.WriteString("<b>" + telegramEscape(telegramBatchTitle(m, items, len(m.Items))) + "</b>")
		for i, it := range items {
			b.WriteString("\n" + telegramEscape(telegramBatchLine(it, i+1)))
		}
		b.WriteString(footer)
		return TruncateHTML(b.String(), telegramTextLimit), kept
	}
	if len(m.Items) == 0 {
		return b.String(), 0
	}
	it := m.Items[0]
	if it.IsStatusChange() {
		b.WriteString(fmt.Sprintf("\n<b>상태 변경</b>: %s → %s",
			telegramEscape(StatusLabel(it.FromStatus)), telegramEscape(StatusLabel(it.ToStatus))))
	}
	if it.VulnClass != "" && it.VulnClass != it.Title() {
		b.WriteString("\n<b>유형</b>: " + telegramEscape(it.VulnClass))
	}
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		b.WriteString("\n<b>자산</b>: " + telegramEscape(a))
	}
	if s := OneLine(it.Summary, maxSummaryRunes); s != "" {
		b.WriteString("\n<b>개요</b>: " + telegramEscape(s))
	}
	if it.DetailURL != "" {
		b.WriteString(fmt.Sprintf("\n\n<a href=\"%s\">상세 보기</a>", telegramEscapeAttr(it.DetailURL)))
	}
	return TruncateHTML(b.String(), telegramTextLimit), 1
}

// telegramReservedRunes is reserved for the message title and a possible truncation notice (counted in characters).
const telegramReservedRunes = 160

// telegramBatchLine renders one item of the summary (unescaped; escaping is done uniformly by the caller).
func telegramBatchLine(it Item, idx int) string {
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		return fmt.Sprintf("%d. %s · %s — %s", idx, SeverityLabel(it.Severity), it.Title(), a)
	}
	return fmt.Sprintf("%d. %s · %s", idx, SeverityLabel(it.Severity), it.Title())
}

// telegramBatchTitle renders the title line of the summary message. The count used is the number **actually contained in this message**,
// not the total of the batch — otherwise readers would think the number in the message header is the whole.
func telegramBatchTitle(m Message, items []Item, total int) string {
	title := fmt.Sprintf("취약점 요약 · 총 %d건", total)
	if extra := total - len(items); extra > 0 {
		title += fmt.Sprintf(" (앞 %d건만 표시, 나머지 %d건은 다음 메시지에서 이어집니다)", len(items), extra)
	}
	if m.WindowMinutes > 0 {
		title = fmt.Sprintf("최근 %d분간 · %s", m.WindowMinutes, title)
	}
	return title
}

// telegramEscape escapes HTML text content.
// Telegram only recognizes these three entities; after escaping, existing entities such as &amp; get escaped a second time — this is exactly the
// desired behavior: what we want to display is the raw characters, not to let users inject HTML.
func telegramEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// telegramEscapeAttr escapes HTML attribute values. In addition to text escaping, quotes must be handled —
// a quote in a URL would close the href attribute early and turn the content after it into an injection point.
func telegramEscapeAttr(s string) string {
	s = telegramEscape(s)
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
