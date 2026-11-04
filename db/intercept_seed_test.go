package db

import (
	"regexp"
	"testing"
)

// 内置「删除类接口路径」规则匹配的是整个 tool_input JSON 串，因此用例直接以
// JSON 形态给出，与 Interceptor 实际拿到的 subject 一致。
func TestDeleteEndpointPathPattern(t *testing.T) {
	re := regexp.MustCompile(deleteEndpointPathPattern)

	hit := []string{
		`{"command":"curl -s 'http://t.com/api/user/delete?id=1'"}`,    // GET 打删除接口
		`{"command":"curl -X POST http://t.com/admin/delete -d id=1"}`, // POST 打删除接口
		`{"command":"curl 'http://t.com/api/deleteAll'"}`,
		`{"command":"curl 'http://t.com/api/delete_user?id=1'"}`,
		`{"command":"curl 'http://t.com/api/delete-user?id=1'"}`,
		`{"url":"http://t.com/api/remove?id=1"}`,
		`{"command":"curl http://t.com/files/unlink/3"}`,
		`{"command":"curl http://t.com/api/del?id=2"}`,
		`{"command":"curl -X POST http://t/v1/erase"}`,
		`{"command":"curl http://t/admin/destroyAll"}`, // v1 的路径规则不允许后缀，这里补上
	}
	for _, s := range hit {
		if !re.MatchString(s) {
			t.Errorf("应命中却放行: %s", s)
		}
	}

	// 动词后必须跟分隔符，避免 /delivery、/details 这类只读路径被误拦。
	miss := []string{
		`{"command":"curl 'http://t.com/api/delivery?id=1'"}`,
		`{"command":"curl 'http://t.com/order/details'"}`,
		`{"command":"curl 'http://t.com/api/delta/sync'"}`,
		`{"command":"curl 'http://t.com/user/delegate'"}`,
		`{"command":"curl 'http://delete.example.com/'"}`, // 删除动词出现在域名而非路径
		`{"command":"curl 'http://t.com/remote/status'"}`,
		`{"command":"nmap -p80 10.0.0.1"}`,
	}
	for _, s := range miss {
		if re.MatchString(s) {
			t.Errorf("误拦: %s", s)
		}
	}
}

// The i18n migration must rewrite seeded Chinese rows to English only while the
// stored name+message still equal the original seeded values.
func TestMigrateInterceptRulesI18n(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	exact, edited := legacyInterceptRuleI18n[2], legacyInterceptRuleI18n[3]
	clean := func() {
		for _, e := range []struct{ oldName, newName, oldMsg, newMsg string }{exact, edited} {
			_, _ = d.Exec(`DELETE FROM intercept_rules WHERE name IN ($1,$2)`, e.oldName, e.newName)
		}
	}
	clean()
	t.Cleanup(clean)
	for _, e := range []struct{ oldName, newName, oldMsg, newMsg string }{exact, edited} {
		msg := e.oldMsg
		if e.oldName == edited.oldName {
			msg = "user edited message"
		}
		if _, err := d.Exec(`INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
VALUES ($1, true, 100, 'tool_input', 'regex', 'x', 'deny', $2, false, 60, 'deny')`, e.oldName, msg); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.SetSetting("intercept_default_rules_i18n_v1", ""); err != nil {
		t.Fatal(err)
	}
	if err := d.migrateInterceptRulesI18n(); err != nil {
		t.Fatal(err)
	}
	count := func(name, msg string) int {
		var n int
		if err := d.QueryRow(`SELECT count(*) FROM intercept_rules WHERE name=$1 AND message=$2`, name, msg).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(exact.newName, exact.newMsg) != 1 || count(exact.oldName, exact.oldMsg) != 0 {
		t.Errorf("seeded row was not migrated to English")
	}
	if count(edited.oldName, "user edited message") != 1 || count(edited.newName, edited.newMsg) != 0 {
		t.Errorf("user-edited row must be left untouched")
	}
}

func TestLegacyInterceptRuleI18nIsEnglish(t *testing.T) {
	han := regexp.MustCompile(`\p{Han}`)
	for _, e := range legacyInterceptRuleI18n {
		if han.MatchString(e.newName) || han.MatchString(e.newMsg) {
			t.Errorf("replacement for %q still contains Chinese", e.oldName)
		}
	}
}
