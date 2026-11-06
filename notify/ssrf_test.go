package notify

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// This file covers two related hardenings:
//   ① a delivery address must not use the server as a springboard to hit the internal network / cloud metadata (SSRF)
//   ② the error message of address validation must not leak credentials contained in the address
//
// About the test environment: many cases in this package use httptest fake receivers on 127.0.0.1, which the guard
// blocks by default. So TestMain turns on AllowLocalTargetsEnv globally, while each SSRF case below
// explicitly clears it, to assert the **deny-by-default** behavior.

func TestMain(m *testing.M) {
	// Let regular cases connect to the local fake receiver; SSRF cases temporarily clear it themselves.
	_ = os.Setenv(AllowLocalTargetsEnv, "1")
	os.Exit(m.Run())
}

// TestDialGuardRejectsLoopbackByDefault is the core assertion of SSRF protection:
// under the default configuration, delivery to a loopback address must be rejected at the **connection layer**.
func TestDialGuardRejectsLoopbackByDefault(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = io.WriteString(w, `{"errcode":0}`)
	}))
	defer srv.Close()

	t.Setenv(AllowLocalTargetsEnv, "") // turn off the escape hatch = default behavior
	_, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"}, Message{Items: []Item{{Severity: "high"}}})
	if err == nil {
		t.Fatal("默认不应允许投递到环回地址")
	}
	if hit {
		t.Fatal("请求已经打到了本机服务——守卫没生效")
	}
	// The error message must guide the user on how to open it up (a local SMTP relay is a legitimate configuration).
	if !strings.Contains(err.Error(), AllowLocalTargetsEnv) {
		t.Errorf("拒绝信息应说明如何显式放开: %v", err)
	}
}

// TestDialGuardAllowsLoopbackWhenOptedIn is the reverse case: it must work once explicitly enabled,
// otherwise legitimate deployments such as a local postfix / internal relay would be killed wholesale.
func TestDialGuardAllowsLoopbackWhenOptedIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer srv.Close()

	t.Setenv(AllowLocalTargetsEnv, "1")
	if _, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"}, Message{Items: []Item{{Severity: "high"}}}); err != nil {
		t.Fatalf("显式放开后应可投递: %v", err)
	}
}

func TestIsBlockedDialIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.1.2.3", "::1",
		"169.254.169.254", // cloud metadata endpoint — the main reason this function exists
		"169.254.1.1", "fe80::1",
		"0.0.0.0", "::",
		"224.0.0.1", "ff02::1",
		"::ffff:127.0.0.1", // the IPv4-mapped form must be unmapped before judging, otherwise it is a bypass
		"",
	}
	for _, s := range blocked {
		if !isBlockedDialIP(net.ParseIP(s)) {
			t.Errorf("%s 应被拒绝", s)
		}
	}
	// RFC1918 private networks are **deliberately allowed**: a self-hosted internal Mattermost / SMTP relay is a common legitimate use.
	// This assertion pins that trade-off down — if someone later adds a private-network check in passing, this fails,
	// forcing a conscious decision (rather than silently breaking a batch of deployments).
	allowed := []string{"10.0.0.5", "172.16.3.4", "192.168.1.10", "8.8.8.8", "2606:4700::1111"}
	for _, s := range allowed {
		if isBlockedDialIP(net.ParseIP(s)) {
			t.Errorf("%s 应被放行（私网是常见的合法投递目标）", s)
		}
	}
}

// TestValidateHTTPURLRejectsLiteralPrivateTargets covers the up-front hint at configuration time:
// a literal IP should be rejected at save time, rather than waiting for the first delivery to fail.
func TestValidateHTTPURLRejectsLiteralPrivateTargets(t *testing.T) {
	t.Setenv(AllowLocalTargetsEnv, "")
	for _, raw := range []string{
		"http://127.0.0.1:8080/hook",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]:8080/hook",
	} {
		if err := validateHTTPURL(raw); err == nil {
			t.Errorf("%s 应在配置阶段被拒绝", raw)
		}
	}
	// Public and private addresses pass as usual (private ones are left to the dial phase, which does not block them).
	for _, raw := range []string{"https://oapi.dingtalk.com/robot/send", "http://10.0.0.9/hook"} {
		if err := validateHTTPURL(raw); err != nil {
			t.Errorf("%s 应通过校验: %v", raw, err)
		}
	}
}

// TestValidateHTTPURLErrorNeverLeaksCredentials covers a branch the audit pointed out I missed in the previous round.
//
// When url.Parse **fails** it returns a *url.Error whose Error() contains the full original address. Last round I only
// sanitized the error returned by http.Client.Do and missed this one; and the "permanent failure path" cases added back then
// (file://, gopher://, ftp://) can in fact all be parsed successfully by url.Parse and go through the scheme branch,
// so being all green could not prove this path safe — a false guarantee.
func TestValidateHTTPURLErrorNeverLeaksCredentials(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/%zz?access_token=" + leakProbeToken,         // illegal percent escape
		"https://a.example.com:port/x?access_token=" + leakProbeToken, // non-numeric port
		"http://[::1?access_token=" + leakProbeToken,                  // unbalanced brackets
	}
	for _, raw := range cases {
		// First confirm this input **really** makes url.Parse fail. Without this step, a case could unknowingly
		// take a different branch (that is how last round's false guarantee came about).
		if _, err := url.Parse(raw); err == nil {
			t.Errorf("%q 本应解析失败，否则这条用例没有覆盖到目标分支", raw)
			continue
		}
		err := validateHTTPURL(raw)
		if err == nil {
			t.Errorf("%q 应校验失败", raw)
			continue
		}
		assertNoSecret(t, err.Error(), leakProbeToken)
	}
	// Confirm the channel-layer wrapping does not carry the address out either.
	t.Setenv(AllowLocalTargetsEnv, "")
	err := (dingTalkChannel{}).Validate(map[string]any{"webhook": cases[0]})
	if err == nil {
		t.Fatal("非法地址应校验失败")
	}
	assertNoSecret(t, err.Error(), leakProbeToken)
}

// TestEmailDialGuardRejectsLoopbackByDefault covers the dial guard of the SMTP channel.
//
// The email channel used to use a bare net.Dialer, the only gap in the whole SSRF protection: setting host to
// 169.254.169.254 or 127.0.0.1 could connect directly, and when the smtp.NewClient handshake fails it wraps the
// line returned by the peer into the error, echoed through last_error by the delivery history API — exactly the half-blind read primitive
// that the other channels have already closed; the timing difference between "connection refused vs timeout" could also be used to probe ports.
//
// This package's TestMain turns on AllowLocalTargetsEnv globally (many cases use fake receivers on 127.0.0.1),
// so this case must clear it itself — otherwise it passes whether or not the guard is there,
// which is also exactly why the gap was not caught by any test back then.
func TestEmailDialGuardRejectsLoopbackByDefault(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)

	t.Setenv(AllowLocalTargetsEnv, "") // turn off the escape hatch = default behavior
	_, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil {
		t.Fatal("默认不应允许把邮件投递到环回地址")
	}
	// The connection should never be established: the guard blocks it in the Control hook, so EHLO is never sent.
	if f.sawCommand("EHLO") || f.sawCommand("HELO") {
		t.Fatal("SMTP 会话已经建立——守卫没生效")
	}
	// The error message must guide the user on how to open it up (a local postfix relay is a legitimate configuration).
	if !strings.Contains(err.Error(), AllowLocalTargetsEnv) {
		t.Errorf("拒绝信息应说明如何显式放开: %v", err)
	}
}

// TestEmailDialGuardAllowsLoopbackWhenOptedIn is the paired reverse case: it must deliver normally
// once explicitly enabled. A self-hosted internal SMTP / local relay is a very common deployment, and the guard cannot be one-size-fits-all.
func TestEmailDialGuardAllowsLoopbackWhenOptedIn(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)

	t.Setenv(AllowLocalTargetsEnv, "1")
	if _, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg()); err != nil {
		t.Fatalf("显式放开后本机 SMTP 应可投递: %v", err)
	}
	if !f.sawCommand("EHLO") {
		t.Fatal("未看到 EHLO——会话没真正建立")
	}
}
