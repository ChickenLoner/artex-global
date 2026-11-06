package server

import "testing"

// Translating log lines to English must not change how the Logs page classifies
// them: each English phrase has to land on the same level as the Chinese one.
func TestLevelOfEnglishAndLegacyChineseAgree(t *testing.T) {
	cases := []struct{ msg, want string }{
		{"[traffic] write index failed: boom", "error"},
		{"[traffic] 写索引失败: boom", "error"},
		{"[enrich] queue full, task dropped", "error"},
		{"[enrich] 队列已满，丢弃任务", "error"},
		{"[notify] delivery rejected by gateway", "error"},
		{"[notify] 网关拒绝", "error"},
		{"[proxy] target unreachable", "error"},
		{"[proxy] 目标不可达", "error"},
		{"[llm] will retry in 5s", "warn"},
		{"[llm] 稍后重试", "warn"},
		{"[engine] task reset", "info"},
	}
	for _, c := range cases {
		if got := levelOf(c.msg); got != c.want {
			t.Errorf("levelOf(%q) = %q, want %q", c.msg, got, c.want)
		}
	}
}
