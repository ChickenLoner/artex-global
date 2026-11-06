package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// weComMarkdownLimit is the hard limit of the WeCom group bot's markdown content (bytes, not characters).
// This is the tightest limit among all six channels and the main reason TruncateBytes exists.
const weComMarkdownLimit = 4096

// weComChannel implements the WeCom (Enterprise WeChat) group bot.
//
// Platform characteristics:
//   - Authenticated solely by the key in the URL; request signing is not supported — so the webhook address itself is the entire credential.
//   - markdown content is capped at 4096 **bytes**, and an over-long message is rejected whole (not truncated). Chinese takes 3 bytes per character,
//     which means only a bit over a thousand characters can be written in the body, so the client must truncate.
//   - Rate limit of 20 messages/minute, likewise held in check by client-side rate limiting.
type weComChannel struct{}

func (weComChannel) Kind() string { return KindWeCom }

func (weComChannel) DefaultRatePerMin() int { return 20 }

// WeCom has only one credential, the Webhook (the key in the URL), and it does not support signing —
// the whole address is the entire credential, and there are no other fields that need masking.
func (weComChannel) SecretKeys() []string { return []string{"webhook"} }

// WeCom has only one field, the Webhook, which is both the destination and the credential, so there is no "credential left over after changing the address".
func (weComChannel) DestinationKeys() []string { return []string{"webhook"} }

func (weComChannel) Validate(cfg map[string]any) error {
	hook := cfgString(cfg, "webhook")
	if hook == "" {
		return errors.New("Webhook 주소가 없습니다")
	}
	if err := validateHTTPURL(hook); err != nil {
		return fmt.Errorf("Webhook 주소가 올바르지 않습니다: %w", err)
	}
	return nil
}

func (c weComChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	// A digest batch can be very long (50 items × one line each + a prefix), so 4096 bytes is easily exceeded.
	// Truncation is done here rather than relying on a platform error: being rejected means the whole batch is lost, whereas truncation at least delivers the first several items.
	content, kept := markdownBody(m, weComMarkdownLimit)
	payload := map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]any{"content": content},
	}
	raw, err := doJSON(ctx, "POST", cfgString(cfg, "webhook"), nil, payload)
	if err != nil {
		return 0, err
	}
	var res struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("WeCom 응답을 해석하지 못했습니다: %w (%s)", err, snippet(raw))
	}
	if res.ErrCode != 0 {
		// 45009 means the API call limit was exceeded — the platform's rate-limit window rolls, so retrying after backoff is effective,
		// hence it is explicitly classified as retryable. Reaching here means the client's rate_per_min is configured too aggressively;
		// retrying is only a backstop, and the real fix is to lower that channel's rate limit value.
		if res.ErrCode == 45009 {
			return 0, fmt.Errorf("WeCom 요청이 제한되었습니다 (%d): %s", res.ErrCode, res.ErrMsg)
		}
		// 93000 means the webhook key is invalid — a permanent failure that retrying will not heal.
		return 0, Permanent(fmt.Errorf("WeCom에서 오류가 발생했습니다 (%d): %s", res.ErrCode, res.ErrMsg))
	}
	return kept, nil
}
