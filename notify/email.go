package notify

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// emailDialTimeout / emailSessionTimeout bound connection setup and the whole SMTP session respectively.
// net/smtp has no timeout mechanism of its own; without these two, a stuck peer would leave
// the delivery goroutine hanging forever - and the dispatcher processes serially in a single goroutine,
// which would bring the whole notification system to a halt.
const (
	emailDialTimeout    = 10 * time.Second
	emailSessionTimeout = 45 * time.Second
)

// emailChannel implements SMTP email delivery.
type emailChannel struct{}

func (emailChannel) Kind() string { return KindEmail }

// Email has no platform rate limit, but it should not be used to flood; give a generous default.
func (emailChannel) DefaultRatePerMin() int { return 60 }

// Only the password is masked. The SMTP host, account and recipients are not secrets; masking them would only make editing harder.
func (emailChannel) SecretKeys() []string { return []string{"password"} }

// host/port decide which server the password is handed to; tls decides whether the transport is encrypted. A change in any of the three
// requires re-confirming the password - which also forces "turning off TLS" to explicitly carry credentials rather than being a casual tweak.
func (emailChannel) DestinationKeys() []string { return []string{"host", "port", "tls"} }

func (emailChannel) Validate(cfg map[string]any) error {
	if cfgString(cfg, "host") == "" {
		return errors.New("SMTP 서버 주소가 없습니다")
	}
	port := cfgInt(cfg, "port")
	if port <= 0 || port > 65535 {
		return errors.New("SMTP 포트가 올바르지 않습니다 (1-65535 범위여야 합니다)")
	}
	if cfgString(cfg, "from") == "" {
		return errors.New("발신자 주소가 없습니다")
	}
	if len(cfgStrings(cfg, "to")) == 0 {
		return errors.New("수신자 주소가 최소 하나 필요합니다")
	}
	return nil
}

func (c emailChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	host := cfgString(cfg, "host")
	port := cfgInt(cfg, "port")
	from := cfgString(cfg, "from")
	to := cfgStrings(cfg, "to")
	username := cfgString(cfg, "username")
	password := cfgString(cfg, "password")
	implicitTLS := cfgBool(cfg, "tls")

	msg, err := buildEmailMessage(from, to, m)
	if err != nil {
		return 0, Permanent(err)
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	client, err := emailDial(ctx, addr, host, implicitTLS)
	if err != nil {
		return 0, err
	}
	defer client.Close()

	// STARTTLS: upgrade if the peer supports it. Credentials must not be sent over a plaintext session (see the auth note below).
	if !implicitTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return 0, fmt.Errorf("STARTTLS 실패: %w", err)
			}
		}
	}
	if username != "" {
		if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
			// smtp.PlainAuth refuses to send credentials over an unencrypted connection (unless the target is localhost).
			// This is **correct** security behavior and must not be bypassed, but the reason needs to be explained clearly -
			// otherwise users only see "unencrypted connection" and do not know what to do.
			if strings.Contains(err.Error(), "unencrypted connection") {
				return 0, Permanent(fmt.Errorf("자격 증명 전송을 거부했습니다: 연결이 암호화되지 않았습니다. TLS 를 켜거나, 465 포트(암시적 TLS)를 사용하거나, 「TLS 사용」을 체크하세요 (%w)", err))
			}
			return 0, Permanent(fmt.Errorf("SMTP 인증 실패: %w", err))
		}
	}
	if err := client.Mail(from); err != nil {
		return 0, smtpStageError(fmt.Sprintf("발신자 %s 주소가 거부되었습니다", from), err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return 0, smtpStageError(fmt.Sprintf("수신자 %s 주소가 거부되었습니다", rcpt), err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return 0, fmt.Errorf("SMTP DATA 실패: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return 0, fmt.Errorf("메일 본문 쓰기 실패: %w", err)
	}
	if err := w.Close(); err != nil {
		return 0, fmt.Errorf("메일 제출 실패: %w", err)
	}
	// A failed Quit does not affect the fact that "the mail was accepted by the server", so its error is ignored.
	_ = client.Quit()
	// Email has no length truncation (the whole HTML body is sent), so the entire batch counts as delivered.
	return len(m.Items), nil
}

// emailDial establishes the SMTP connection.
//
// implicitTLS=true uses the "TLS as soon as connected" mode such as port 465; false uses 25/587, connecting in plaintext and then
// STARTTLS. The two must not be mixed: sending a plaintext greeting to port 465 gets the connection dropped immediately.
//
// The session deadline is set **at dial time** (rather than afterwards), because net/smtp's Client hides the underlying
// connection in an unexported field that callers cannot reach; once the connection is handed over, only the pre-set deadline
// serves as a backstop. This also covers blocking during the handshake.
// Control hooks blockInternalDial, sharing the same guard with the HTTP-based channels. Without it, SMTP
// would be a hole in the whole SSRF protection: with host set to 169.254.169.254 or 127.0.0.1 it could connect directly,
// and when the smtp.NewClient handshake fails, the line returned by the peer is wrapped into the error and echoed back through last_error
// by the delivery history API, forming a semi-blind read primitive; the timing difference of "connection refused vs timeout" could also
// be used to probe ports. The dial stage is the final point of enforcement, and it also covers DNS rebinding.
func emailDial(ctx context.Context, addr, host string, implicitTLS bool) (*smtp.Client, error) {
	d := &net.Dialer{Timeout: emailDialTimeout, Control: blockInternalDial}
	var conn net.Conn
	var err error
	if implicitTLS {
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("SMTP 서버 연결 실패: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(emailSessionTimeout))
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SMTP 핸드셰이크 실패: %w", err)
	}
	return client, nil
}

// smtpStageError classifies a failure in a given stage as "retryable" or "permanent" by the SMTP reply code.
//
// Why the distinction is required: SMTP 4xx and 5xx have completely different semantics -
//   - 4xx (450 greylisting, 451 local error, 452 insufficient storage) are **temporary** rejections,
//     and the proper practice is to retry later; greylisting in particular is met on almost every first delivery.
//   - 5xx (550 user does not exist, 553 invalid address) are permanent rejections, and retrying is pointless.
//
// If everything were judged permanent, a mail server with greylisting enabled would make **every** push fall into failed
// after the first attempt - and such failures are exactly where automatic retry should do its job.
// The reply code is the first three digits of the error text; when no code can be obtained it is treated as retryable (better to try once more
// than to declare a possibly transient fault dead just because it could not be parsed).
func smtpStageError(what string, err error) error {
	code := smtpReplyCode(err.Error())
	if code >= 500 && code < 600 {
		return Permanent(fmt.Errorf("%s: %w", what, err))
	}
	return fmt.Errorf("%s: %w", what, err)
}

// smtpReplyCode extracts the leading three-digit reply code from SMTP error text, returning 0 if none is found.
// net/smtp does not export an error-code field, so it can only be taken from the text; the format is "450 4.7.1 ...".
func smtpReplyCode(text string) int {
	if len(text) < 3 {
		return 0
	}
	n, err := strconv.Atoi(text[:3])
	if err != nil {
		return 0
	}
	return n
}

// buildEmailMessage assembles a complete RFC 5322 email.
//
// The body is base64-encoded for two reasons: first, SMTP limits a single line to 1000 bytes, and HTML
// bodies (especially digest emails) easily contain overlong lines; second, base64 never produces a line starting with "."
// which spares the trouble of SMTP dot-stuffing.
func buildEmailMessage(from string, to []string, m Message) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	// A Chinese subject must be RFC 2047-encoded, otherwise clients will display it as garbled text.
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", htmlTitle(m)))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	// Email has no hard length limit, so the body is not truncated.
	b.WriteString("\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(htmlBody(m, 0)))
	// base64 wraps lines at 76 characters, in line with RFC 2045.
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded + "\r\n")
	return b.String(), nil
}
