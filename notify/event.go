package notify

// Snapshot is the contract of the JSONB column notification_events.snapshot. The writer is the db layer's
// finding-persistence transaction, and the readers are the server layer's delivery engine and filter matching. It is defined in this package because it is a
// payload of the "notification domain": db only serializes it and does not understand what the fields mean.
//
// Why finding fields are stored redundantly instead of looked up at render time: findings can later be renamed, re-leveled or have their status changed,
// while the pushed content should reflect the conclusion **at the time of the event** — a lookup would give a dangerously misleading
// "later changed to low". It also means fan-out and rendering need not JOIN the findings/tasks/assets tables.
type Snapshot struct {
	// Event type: finding_created / finding_status_changed
	Kind      string  `json:"kind"`
	FindingID int64   `json:"finding_id"`
	TaskID    int64   `json:"task_id"`
	VulnClass string  `json:"vulnclass"`
	Name      string  `json:"name"`
	Severity  string  `json:"severity"`
	Summary   string  `json:"summary"`
	AssetIDs  []int64 `json:"asset_ids"`
	// Non-empty only when kind=finding_status_changed.
	FromStatus string `json:"from_status,omitempty"`
	ToStatus   string `json:"to_status,omitempty"`
}

// Item is one finding to be pushed, for channels to render.
type Item struct {
	FindingID int64
	Name      string
	VulnClass string
	Severity  string
	Summary   string
	// Assets are the resolved asset display names (such as domain/IP). Filled in by the server layer —
	// this package does not touch the database and cannot get names.
	Assets []string
	// DetailURL is the backlink to the finding detail; empty means public_base_url is not configured, and it is omitted when rendering.
	DetailURL string
	// Specific to status-change events; when both are non-empty it renders as "Pending → Fixed".
	FromStatus string
	ToStatus   string
}

// IsStatusChange reports whether this item is a status-change event.
func (i Item) IsStatusChange() bool { return i.FromStatus != "" || i.ToStatus != "" }

// Title returns the item's display title: prefer the manually assigned name, fall back to the vulnerability type vulnclass,
// and use a placeholder when both are empty — never output an empty title.
func (i Item) Title() string {
	if i.Name != "" {
		return i.Name
	}
	if i.VulnClass != "" {
		return i.VulnClass
	}
	return "(이름 없는 취약점)"
}

// Message is the complete content of one channel send.
type Message struct {
	// Length is 1 for a single push; for a digest push it is a whole batch.
	// An empty slice is invalid; callers must guarantee at least one item.
	Items []Item
	// When Batch=true it renders as a digest message (different title, with the time window and item count).
	Batch bool
	// WindowMinutes is the digest period in minutes, used only when Batch=true for the wording "last N minutes".
	// It is deliberately passed in explicitly from config rather than computed with time.Since at render time: rendering stays deterministic, which makes it testable.
	WindowMinutes int
	// HomeURL is the platform dashboard URL (global public_base_url); when empty, no dashboard entry point is included.
	HomeURL string
}
