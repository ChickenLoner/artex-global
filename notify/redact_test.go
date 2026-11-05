package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// This file is an invariant test: error text emerging from **any** channel implementation must not carry credentials.
//
// Why it is a separate file: the initial channel test cases only covered the success path and platform business errors,
// and did not look at transport-layer failures at all. Yet transport-layer errors (connection refused/DNS failure/timeout) are the most dangerous —
// the *url.Error returned by http.Client.Do puts the **full URL** into the error text, and the credentials of this feature's
// providers are in the URL. The credentials flowed along this string out through four exits:
//
//	notification_deliveries.last_error  → persisted in plaintext
//	GET /api/notify/deliveries response → bypasses the channel config's masking, echoed back to the browser
//	server log                          → often retained after being shipped out
//	the 502 response of the test-send API → pops up directly in the frontend
//
// So this does not test just one function; it actually sends one request that is bound to fail through each channel, and asserts that the credential cannot be found
// in the error text.

// credentialCases covers all channel shapes where "the credential is in the URL":
// DingTalk/WeCom have it in the query, Feishu in the last path segment, and Telegram in the middle of the path.
var credentialCases = []struct {
	name   string
	ch     Channel
	cfg    map[string]any
	secret string
}{
	{
		name:   "钉钉 access_token 在 query",
		ch:     dingTalkChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/robot/send?access_token=" + leakProbeToken},
		secret: leakProbeToken,
	},
	{
		name:   "企业微信 key 在 query",
		ch:     weComChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/cgi-bin/webhook/send?key=" + leakProbeToken},
		secret: leakProbeToken,
	},
	{
		name:   "飞书 hook id 在路径末段",
		ch:     feishuChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/open-apis/bot/v2/hook/" + leakProbeToken},
		secret: leakProbeToken,
	},
	{
		name:   "Telegram bot token 在路径中段",
		ch:     telegramChannel{},
		cfg:    map[string]any{"bot_token": leakProbeToken, "chat_id": "1", "base_url": "http://127.0.0.1:1"},
		secret: leakProbeToken,
	},
	{
		name:   "钉钉加签密钥",
		ch:     dingTalkChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/robot/send", "secret": leakProbeToken},
		secret: leakProbeToken,
	},
}

// leakProbeToken is a sentinel value that can never be a real credential, used to search for it in the error text.
const leakProbeToken = "LEAKPROBE0123456789abcdef"

// TestChannelErrorsNeverLeakCredentials is the core invariant.
func TestChannelErrorsNeverLeakCredentials(t *testing.T) {
	for _, tc := range credentialCases {
		t.Run(tc.name, func(t *testing.T) {
			// A peer that is bound to fail: nobody listens on 127.0.0.1:1, so this goes down the connection-refused path.
			_, err := tc.ch.Send(context.Background(), tc.cfg, Message{
				Items: []Item{{FindingID: 1, Severity: "high", Name: "泄露探针"}},
			})
			if err == nil {
				t.Fatal("对不可达地址应报错")
			}
			assertNoSecret(t, err.Error(), tc.secret)
		})
	}
}

// TestChannelErrorsNeverLeakCredentialsInPermanentPath covers the permanent-failure branch:
// URL validation failures, platform business errors, etc. also pass the error text outward, and it must not carry credentials either.
func TestChannelErrorsNeverLeakCredentialsInPermanentPath(t *testing.T) {
	cases := []struct {
		name string
		ch   Channel
		cfg  map[string]any
	}{
		// The address carries a credential but is malformed → triggers the validateHTTPURL / url.Parse branch.
		{"钉钉地址非法", dingTalkChannel{}, map[string]any{"webhook": "file:///" + leakProbeToken}},
		{"企微地址非法", weComChannel{}, map[string]any{"webhook": "gopher://" + leakProbeToken}},
		{"飞书地址非法", feishuChannel{}, map[string]any{"webhook": "ftp://" + leakProbeToken + "/hook"}},
		{"Telegram API 地址非法", telegramChannel{}, map[string]any{"bot_token": "tok", "chat_id": "1", "base_url": "file://" + leakProbeToken}},
		{"通用 Webhook 地址非法", webhookChannel{}, map[string]any{"url": "javascript:" + leakProbeToken}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.ch.Send(context.Background(), tc.cfg, Message{Items: []Item{{Severity: "high"}}})
			if err == nil {
				t.Fatal("非法配置应报错")
			}
			assertNoSecret(t, err.Error(), leakProbeToken)
		})
	}
}

func assertNoSecret(t *testing.T, text, secret string) {
	t.Helper()
	if strings.Contains(text, secret) {
		t.Fatalf("错误文本泄露了凭据 %q:\n    %s", secret, text)
	}
}

func TestRedactRequestTargetKeepsOnlySchemeAndHost(t *testing.T) {
	cases := map[string]string{
		"https://oapi.dingtalk.com/robot/send?access_token=S1":    "https://oapi.dingtalk.com/…",
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=S2": "https://qyapi.weixin.qq.com/…",
		"https://open.feishu.cn/open-apis/bot/v2/hook/S3":         "https://open.feishu.cn/…",
		"https://api.telegram.org/botS4/sendMessage":              "https://api.telegram.org/…",
		"http://10.0.0.5:8080/hook":                               "http://10.0.0.5:8080/…",
	}
	for in, want := range cases {
		got := redactRequestTarget(in)
		if got != want {
			t.Errorf("redactRequestTarget(%q) = %q，期望 %q", in, got, want)
		}
		// The redacted result itself must not contain any path/query fragment of the original address.
		if parts := strings.SplitN(in, "://", 2); len(parts) == 2 {
			if hostAndRest := strings.SplitN(parts[1], "/", 2); len(hostAndRest) == 2 && hostAndRest[1] != "" {
				if strings.Contains(got, hostAndRest[1]) {
					t.Errorf("脱敏后仍含路径/查询片段 %q: %q", hostAndRest[1], got)
				}
			}
		}
	}
	// Unparseable input never echoes the original string.
	for _, bad := range []string{"", "://", "not a url", "http://"} {
		if got := redactRequestTarget(bad); strings.Contains(got, bad) && bad != "" {
			t.Errorf("不可解析输入 %q 被回显为 %q", bad, got)
		}
	}
}

// TestRedactTransportErrorStripsURL zeroes in on the concrete type *url.Error:
// it is the return type of http.Client.Do and also the first scene of the leak.
func TestRedactTransportErrorStripsURL(t *testing.T) {
	inner := errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	uerr := &url.Error{
		Op:  "Post",
		URL: "https://api.telegram.org/bot" + leakProbeToken + "/sendMessage",
		Err: inner,
	}
	got := redactTransportError(uerr)
	assertNoSecret(t, got, leakProbeToken)
	if !strings.Contains(got, "api.telegram.org") {
		t.Errorf("应保留 host 以便排查，得到 %q", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Errorf("应保留底层原因以便排查，得到 %q", got)
	}
	// Op must be kept too (whether it was POST or GET is meaningful for troubleshooting).
	if !strings.Contains(got, "Post") {
		t.Errorf("应保留操作名，得到 %q", got)
	}
}

// TestRedactURLsInTextHandlesFallback covers the fallback path: addresses inside custom errors that are not *url.Error
// (such as errors returned by the redirect policy) must be removed as well.
func TestRedactURLsInTextHandlesFallback(t *testing.T) {
	in := fmt.Sprintf("拒绝跨主机重定向（a.example → http://b.example/bot%s/send）", leakProbeToken)
	got := redactURLsInText(in)
	assertNoSecret(t, got, leakProbeToken)
	if !strings.Contains(got, "http://b.example/…") {
		t.Errorf("应把地址替换为脱敏形态，得到 %q", got)
	}
	// Text that contains no address is kept as it is.
	if plain := "dial tcp: connection refused"; redactURLsInText(plain) != plain {
		t.Error("不含地址的文本不应被改动")
	}
}

// TestCrossHostRedirectRefused covers "credential in the URL + following a cross-host redirect = handing over the credential".
// The two httptest servers listen on different ports of 127.0.0.1; a different port means a different Host,
// which constitutes exactly a cross-host redirect.
func TestCrossHostRedirectRefused(t *testing.T) {
	var hit bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/robot/send?access_token="+leakProbeToken, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	_, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": redirector.URL + "/robot/send?access_token=" + leakProbeToken},
		Message{Items: []Item{{Severity: "high"}}})
	if err == nil {
		t.Fatal("跨主机重定向应被拒绝")
	}
	if hit {
		t.Fatal("跳转目标被访问了——凭据已随重定向外泄")
	}
	assertNoSecret(t, err.Error(), leakProbeToken)
}

// TestSameHostRedirectAllowed is the reverse case: a same-host redirect (such as appending a trailing slash) must still work,
// otherwise normal workflows would be blocked along with it.
func TestSameHostRedirectAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robot/send" {
			// A redirect on the same host and the same port.
			http.Redirect(w, r, "/robot/send/", http.StatusTemporaryRedirect)
			return
		}
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer srv.Close()

	if _, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"},
		Message{Items: []Item{{Severity: "high"}}}); err != nil {
		t.Fatalf("同主机重定向不应被拒绝: %v", err)
	}
}
