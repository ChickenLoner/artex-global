package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// allowLocalTargets decides whether messages may be delivered to loopback / link-local addresses.
//
// Denied by default. These address ranges are not where IM bots or public mail servers live, yet
// what they can reach is sensitive: the admin port of another service on the same host, and the
// cloud metadata endpoint (169.254.169.254, which can expose instance credentials). The delivery
// address is configured by an administrator, but an admin session hijacked via XSS/CSRF, or a
// second person sharing the same JWT, could change the config and read the response back
// -- doJSON writes the first 200 bytes of a 4xx/5xx response body into last_error, and the
// delivery-history API echoes it back, which makes this a semi-blind read primitive.
//
// However, a "local SMTP relay" (postfix on 127.0.0.1:25) is a common self-hosted mail setup, and
// a blanket ban would block those users. So instead of hard-coding an allowance, we leave an
// explicit escape hatch: setting ARTEX_NOTIFY_ALLOW_LOCAL=1 allows it.
//
// It is exported as AllowLocalTargetsEnv so that tests can explicitly turn it on -- many test
// cases in this package and the server package use httptest fake receivers on 127.0.0.1, and
// without turning it on they would all be stopped by the guard.
const AllowLocalTargetsEnv = "ARTEX_NOTIFY_ALLOW_LOCAL"

func allowLocalTargets() bool {
	v := strings.TrimSpace(os.Getenv(AllowLocalTargetsEnv))
	return v == "1" || strings.EqualFold(v, "true")
}

// isBlockedDialIP reports whether the target IP is in an address range that is "not allowed for
// delivery by default".
//
// Only loopback, link-local (including the cloud metadata address 169.254.169.254), unspecified
// and multicast are rejected.
// RFC1918 private networks are **not rejected**: a self-hosted Mattermost / SMTP relay on the
// intranet is a very common legitimate use, and blocking them as well would make the feature
// outright unusable in real environments. This trade-off is deliberate --
// the protection must block truly sensitive targets without wrecking normal deployments.
func isBlockedDialIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// IPv4-mapped IPv6 (::ffff:127.0.0.1) must be converted back to IPv4 before checking, otherwise the check is bypassed.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// blockInternalDial is the Control hook of the http.Transport dialer; it checks the target
// address **at connection time**.
//
// Why it lives in the dial phase rather than only validating when the config is saved: this is
// the point where enforcement actually takes effect. It covers two ways of bypassing config
// validation at once -- DNS rebinding (resolves to a public IP at validation time but to an
// internal address at actual connect time) and redirects (although we already reject cross-host
// redirects, a same-host redirect can still point the path somewhere else).
func blockInternalDial(_, address string, _ syscall.RawConn) error {
	if allowLocalTargets() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("대상 주소를 해석할 수 없습니다: %q", host)
	}
	if isBlockedDialIP(ip) {
		return fmt.Errorf("로컬 또는 링크 로컬 주소 %s 로는 전송하지 않습니다 (로컬 서비스로 보내야 하면 %s=1 로 설정하세요)", ip, AllowLocalTargetsEnv)
	}
	return nil
}

// notifyTransport adds only a dial guard on top of the default Transport.
// It uses Clone to keep all of the default tuning (connection pool, HTTP/2, timeouts, proxy, etc.),
// to avoid changing other behavior just to add one check.
var notifyTransport = func() *http.Transport {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{}
	}
	clone := t.Clone()
	clone.DialContext = (&net.Dialer{Timeout: 10 * time.Second, Control: blockInternalDial}).DialContext
	return clone
}()

// httpClient is the client shared by all channel deliveries.
//
// It deliberately does **not** reuse the project's global egress proxy (GlobalProxy on the server
// side): that proxy is for penetration-target traffic and is often an unstable tunnel, and
// notification availability should not be held hostage to jitter in the target network.
// IM pushes can just connect directly. The timeout is 15 seconds -- a peer slower than that is
// effectively already in a failure state.
//
// Cross-host redirects are rejected: every delivery address of this feature is a "single fixed
// endpoint", which normally never redirects to another host; and these providers' credentials
// (DingTalk's access_token, WeCom's key, Telegram's bot token) are **in the URL**, so following a
// cross-host redirect would hand the credentials to the redirect target. Same-host redirects
// (such as appending a trailing slash) are still allowed.
var httpClient = &http.Client{
	Timeout:   15 * time.Second,
	Transport: notifyTransport,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("리다이렉트가 너무 많습니다")
		}
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			return fmt.Errorf("다른 호스트로의 리다이렉트는 허용하지 않습니다 (%s → %s)", via[0].URL.Host, req.URL.Host)
		}
		return nil
	},
}

// respBodyLimit caps how much of the response body is read. A misbehaving peer may send back huge
// content, while all we need is the error code and a short error description to show in the
// delivery history.
const respBodyLimit = 8 << 10

// doJSON sends one request and returns the response body (length-capped).
//
// When payload is nil an empty body is sent (for GET or platforms that do not require a body).
// The key/values in headers are attached as-is, for the custom headers of the generic Webhook.
//
// Error classification is the core responsibility of this function: network-layer failures and
// 5xx/408/429 are "retryable", while the remaining 4xx are "permanent failures" -- retrying a 403
// would only spam the same error into the log 3 times.
func doJSON(ctx context.Context, method, url string, headers map[string]string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			// A serialization failure is a local bug (wrong config field type); retrying won't help.
			return nil, Permanent(fmt.Errorf("요청 본문을 만들지 못했습니다: %w", err))
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		// Invalid URL -- most likely the user mistyped the address, so this is a permanent failure.
		// err must not be passed through here either: the error text of url.Parse contains the full address.
		return nil, Permanent(fmt.Errorf("요청 주소가 올바르지 않습니다: %s", redactRequestTarget(url)))
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		// Connection refused, DNS failure, timeout -- mostly transient faults, left to backoff retry.
		//
		// The error text must be redacted before being passed outward. Reason: http.Client.Do returns a
		// *url.Error whose Error() is `Op "full URL": underlying error`, and these providers'
		// credentials are **in the URL** (DingTalk access_token, WeCom key, Feishu hook id,
		// Telegram /bot<token>/). Without redaction the credentials would flow along this error string
		// to four places: last_error of notification_deliveries (persisted in plaintext), the
		// delivery-history API response (**bypassing the channel config's masking**), the server log,
		// and the 502 text the test-send API returns to the frontend.
		return nil, fmt.Errorf("요청에 실패했습니다: %s", redactTransportError(err))
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, respBodyLimit))
	if readErr != nil {
		return nil, fmt.Errorf("응답을 읽지 못했습니다: %w", readErr)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return raw, nil
	}
	// 429 (rate limited) and 408 (timeout) are worth retrying; the remaining 4xx are config or permission problems, so retrying is pointless.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout {
		return nil, fmt.Errorf("상대 서버가 요청을 제한하거나 응답이 지연되었습니다 (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("상대 서버에 오류가 발생했습니다 (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	return nil, Permanent(fmt.Errorf("상대 서버가 요청을 거부했습니다 (HTTP %d): %s", resp.StatusCode, snippet(raw)))
}

// snippet squeezes the response body into one line of short text for error messages. The response
// may contain newlines and lots of whitespace, and stuffing it directly into last_error would break
// the layout of the delivery-history page.
func snippet(raw []byte) string {
	return OneLine(string(raw), 200)
}

// redactRequestTarget squeezes the delivery address into "scheme://host/…" for error messages.
//
// This is the only address-redaction policy in this package, and it is deliberately made
// **crude enough**: everything except the scheme and host is dropped. The reason is that there is
// no "generic and safe" way to tell which part of a URL is the credential:
//
//	DingTalk  credential in query             /robot/send?access_token=xxx
//	WeCom     credential in query             /cgi-bin/webhook/send?key=xxx
//	Feishu    credential in the **last path segment**   /open-apis/bot/v2/hook/<hook_id>
//	Telegram  credential in the **middle of the path**  /bot<token>/sendMessage
//
// Trying to "keep only the useful parts" would require per-channel patches, and missing any one
// provider is a credential leak. Keeping the host is already enough for troubleshooting (DNS
// failures, connection failures and certificate problems can all be located), and which bot it
// was is identified from the masked tail hint in the channel config.
//
// On parse failure a fixed placeholder is returned -- the raw string is never echoed back.
func redactRequestTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(주소를 해석할 수 없음)"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

// redactTransportError strips the address from a transport-layer error, keeping only the
// underlying cause.
//
// *url.Error has the structure {Op, URL, Err}, and Error() prints the URL along with it.
// Here we explicitly take the Err field and bypass its Error() -- more reliable than doing string
// replacement afterwards, because replacement must correctly handle all the variants of URL
// encoding/escaping and is easy to miss.
func redactTransportError(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		host := ""
		if u, parseErr := url.Parse(uerr.URL); parseErr == nil {
			host = u.Host
		}
		if uerr.Err != nil {
			return fmt.Sprintf("%s %s: %s", uerr.Op, host, uerr.Err)
		}
		return fmt.Sprintf("%s %s: 알 수 없는 오류", uerr.Op, host)
	}
	// Errors that are not *url.Error (such as those returned by the redirect policy) may also carry an address, so they all go through redaction.
	return redactURLsInText(err.Error())
}

// redactURLsInText replaces http(s) addresses appearing in a piece of text with their redacted form.
//
// It is a fallback for errors whose structured fields are unavailable (redirect-policy errors,
// custom errors from third-party libraries). It only recognizes the http/https prefixes and splits
// on whitespace and quotes -- an address never contains those two kinds of characters.
func redactURLsInText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		rest := s[i:]
		if strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://") {
			end := len(rest)
			if j := strings.IndexAny(rest, " \t\n\"'"); j >= 0 {
				end = j
			}
			b.WriteString(redactRequestTarget(rest[:end]))
			i += end
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
