package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"
)

// dingTalkChannel implements the DingTalk custom robot.
//
// Platform characteristics (which drive the implementation trade-offs here):
//   - Each robot is rate-limited to 20 messages/minute; excess is silently dropped (HTTP may still return 200),
//     so rate limiting must be done client-side, see DefaultRatePerMin.
//   - Security settings are pick-one-of-three: signing / custom keyword / IP allowlist. Signing is the only scheme that does not depend on
//     message content, so only signing is supported (a bare webhook with none of the three enabled also works).
//   - Success and failure both return HTTP 200, distinguished by the errcode in the body — not checking errcode
//     would record delivery failures as successes.
type dingTalkChannel struct{}

func (dingTalkChannel) Kind() string { return KindDingTalk }

func (dingTalkChannel) DefaultRatePerMin() int { return 20 }

// A DingTalk webhook URL carries access_token and is itself a credential, so it is masked as a whole.
func (dingTalkChannel) SecretKeys() []string { return []string{"webhook", "secret"} }

// The destination is the DingTalk webhook URL itself; changing the URL requires re-stating the signing secret for the new URL.
func (dingTalkChannel) DestinationKeys() []string { return []string{"webhook"} }

func (dingTalkChannel) Validate(cfg map[string]any) error {
	hook := cfgString(cfg, "webhook")
	if hook == "" {
		return errors.New("Webhook 주소가 없습니다")
	}
	if err := validateHTTPURL(hook); err != nil {
		return fmt.Errorf("Webhook 주소가 올바르지 않습니다: %w", err)
	}
	return nil
}

// Send delivers one message. With a backlink and a single item it uses ActionCard (with a button), otherwise markdown.
func (c dingTalkChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	hook := cfgString(cfg, "webhook")
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	endpoint, err := dingTalkSignedURL(hook, cfgString(cfg, "secret"), time.Now())
	if err != nil {
		return 0, Permanent(err)
	}

	title := markdownTitle(m)
	// The DingTalk markdown body has no explicit byte limit, but a cap is still applied to keep evidence fields from growing abnormally.
	text, kept := markdownBody(m, 20000)

	var payload any
	if !m.Batch && len(m.Items) == 1 && m.Items[0].DetailURL != "" {
		payload = map[string]any{
			"msgtype": "actionCard",
			"actionCard": map[string]any{
				"title":          title,
				"text":           text,
				"btnOrientation": "0",
				"singleTitle":    "상세 보기",
				"singleURL":      m.Items[0].DetailURL,
			},
		}
	} else {
		payload = map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"title": title, "text": text},
		}
	}

	raw, err := doJSON(ctx, "POST", endpoint, nil, payload)
	if err != nil {
		return 0, err
	}
	// DingTalk hides business errors inside 200 responses.
	var res struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("DingTalk 응답을 해석하지 못했습니다: %w (%s)", err, snippet(raw))
	}
	if res.ErrCode != 0 {
		// 301000 is a signature verification failure and 310000 is a keyword mismatch — both are configuration errors,
		// and retrying will not self-heal.
		return 0, Permanent(fmt.Errorf("DingTalk에서 오류가 발생했습니다 (%d): %s", res.ErrCode, res.ErrMsg))
	}
	return kept, nil
}

// dingTalkSignedURL appends the timestamp and sign parameters to the webhook per the official signing rules.
//
// Rule: string to sign = timestamp + "\n" + secret, the HMAC-SHA256 **key is also the secret**,
// and the result is base64-encoded and then URL-encoded. timestamp is in milliseconds. When secret is empty the URL is returned unchanged,
// to support robots that do not have signing enabled.
func dingTalkSignedURL(hook, secret string, now time.Time) (string, error) {
	if secret == "" {
		return hook, nil
	}
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + secret))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	u, err := url.Parse(hook)
	if err != nil {
		// err is not passed through: the url.Parse error text contains the full address (including access_token).
		return "", fmt.Errorf("Webhook 주소를 해석하지 못했습니다: %s", redactRequestTarget(hook))
	}
	q := u.Query()
	q.Set("timestamp", ts)
	q.Set("sign", sign)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// validateHTTPURL checks that the address is usable and the scheme is supported, and does an internal-network check on literal-IP targets.
//
// Two points of note:
//
//  1. **Error messages must be redacted**. The *url.Error returned by url.Parse has an Error() that carries the
//     **full original address**, and the addresses of these integrations embed credentials (DingTalk access_token,
//     WeCom key, Telegram bot token, Feishu hook id). This code used to `return err` directly,
//     so the "invalid address format" error leaked the credentials into the 400 response of the test endpoint,
//     the last_error persisted on every delivery, server logs, and the delivery-history endpoint.
//
//  2. **Literal IPs are judged as internal directly**; domain names are left to the dial stage (blockInternalDial is the final
//     point of enforcement and also covers DNS rebinding). This is done once here so that a hint appears when the config is saved,
//     rather than only after the first delivery fails.
//
// Restricting the scheme is defensive: file:///gopher:// and the like would make http.Client behave
// unexpectedly (already blocked by the scheme check, but there is no reason to open up this surface).
func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("주소를 해석할 수 없습니다 (%s)", redactRequestTarget(raw))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("http 또는 https 만 지원합니다. 받은 값: %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("호스트 이름이 없습니다")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && isBlockedDialIP(ip) && !allowLocalTargets() {
		return fmt.Errorf("로컬 또는 링크 로컬 주소 %s 로는 전송하지 않습니다 (로컬 서비스로 보내야 하면 %s=1 로 설정하세요)", ip, AllowLocalTargetsEnv)
	}
	return nil
}
