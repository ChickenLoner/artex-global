package notify

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Filter is the contract of the notification_channels.filter JSONB column: the filter conditions of a channel instance.
// All fields are optional; the default means "no filtering" -- this is exactly the fallback semantics for malformed config, see ParseFilter.
type Filter struct {
	// MinSeverity is the minimum severity threshold (low/medium/high/critical); empty = no threshold.
	MinSeverity string `json:"min_severity"`
	// An empty TaskIDs / AssetIDs array means unrestricted; if non-empty, the event must intersect it.
	TaskIDs  []int64 `json:"task_ids"`
	AssetIDs []int64 `json:"asset_ids"`
	// An empty VulnClassInclude means accept all; if non-empty, vulnclass must match any one of the keywords.
	// A VulnClassExclude match on any keyword excludes the event (exclusion takes precedence over inclusion).
	// Matching is a case-insensitive substring -- safer than regex: a user's mistyped regex will not silently disable the channel.
	VulnClassInclude []string `json:"vulnclass_include"`
	VulnClassExclude []string `json:"vulnclass_exclude"`
	// OnStatusChange decides whether this channel receives vulnerability status-change events (only meaningful in realtime mode).
	OnStatusChange bool `json:"on_status_change"`
}

// ParseFilter parses a channel filter configuration.
//
// **Never returns an error.** This is a deliberate design choice: a malformed filter config always degrades to the zero-value
// Filter (= no filtering = match everything), because for a vulnerability notification system, **pushing one too many is far better than
// silently missing a critical one**. Turning a parse failure into "push nothing" would give the user a channel that looks configured
// but actually pushes nothing -- the worst failure mode.
func ParseFilter(raw []byte) Filter {
	var f Filter
	if len(raw) == 0 {
		return f
	}
	// On parse failure f stays at its zero value, i.e. no filtering.
	_ = json.Unmarshal(raw, &f)
	return f
}

// ValidMinSeverity reports whether s is a legal severity threshold (an empty string means no threshold).
func ValidMinSeverity(s string) bool {
	if s == "" {
		return true
	}
	_, ok := severityRank[s]
	return ok
}

// Validate checks the **value-restricted** fields in the filter config; call it when saving a channel.
//
// Why it must be blocked at write time: Match evaluates an unknown threshold as `rank >= 0`, which is always true --
// i.e. a single typo in min_severity ("hgih") makes the filter **silently ineffective** and turns it into
// "push everything". This is consistent with the package's "better to over-push than miss" trade-off (nothing is missed),
// but the consequence is that the user thinks they are doing tiered pushing while actually flooding the group with every vulnerability,
// with no sign that it is misconfigured. Such "silent degradation" is exactly what should be blocked at the entry point.
//
// Note that Validate is only for the **write** path. The read path still uses ParseFilter's lenient semantics,
// so bad values already present in historical data do not make the whole channel unreadable.
func (f Filter) Validate() error {
	if !ValidMinSeverity(f.MinSeverity) {
		return fmt.Errorf("올바르지 않은 최소 심각도입니다: %q. low / medium / high / critical 중에서 선택하거나, 비워 두면 제한하지 않습니다", f.MinSeverity)
	}
	return nil
}

// Match decides whether an event should be delivered to a channel with this filter.
//
// **Never returns an error**, for the same reason as ParseFilter: any internal anomaly is treated as a "match".
// Decision order: event type -> severity threshold -> task/asset scope -> vulnerability-class keywords.
func Match(f Filter, s Snapshot) bool {
	// Status-change events are received only by channels that explicitly opt in. Off by default, because most users
	// expect "push" to mean "a new vulnerability was found", not a running log following every state transition.
	if s.Kind == EventFindingStatusChanged && !f.OnStatusChange {
		return false
	}
	if !AtLeast(s.Severity, f.MinSeverity) {
		return false
	}
	if len(f.TaskIDs) > 0 && !slices.Contains(f.TaskIDs, s.TaskID) {
		return false
	}
	if len(f.AssetIDs) > 0 && !intersectsInt(f.AssetIDs, s.AssetIDs) {
		return false
	}
	// Exclusion first: matching any exclude keyword drops the event, even if it also matches the include list.
	if len(f.VulnClassExclude) > 0 && containsAnyFold(s.VulnClass, f.VulnClassExclude) {
		return false
	}
	if len(f.VulnClassInclude) > 0 && !containsAnyFold(s.VulnClass, f.VulnClassInclude) {
		return false
	}
	return true
}

func intersectsInt(a, b []int64) bool {
	// A linear scan of small sets is enough; both sides are on the order of "a few dozen hand-ticked items",
	// so building a map costs more than it gains.
	for _, v := range b {
		if slices.Contains(a, v) {
			return true
		}
	}
	return false
}

// containsAnyFold reports whether s contains any of the keywords (case-insensitive).
func containsAnyFold(s string, keywords []string) bool {
	lower := strings.ToLower(s)
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw != "" && strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
