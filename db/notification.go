package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Autumn-27/artex/notify"
)

// This file is the channel configuration and event layer for IM push. For claiming of delivery jobs and their state transitions see
// db/notification_delivery.go.
//
// Two invariants that must be preserved when changing this file:
//
//  1. The finding-write transaction (RecordFindingTx) only calls InsertNotificationEventTx to do a blind insert,
//     reading no notification-related table and doing no filter matching. Any read introduced here could pollute or even abort the finding-write transaction
//     because of a filter condition the user misconfigured.
//  2. Filter matching never errors: a malformed config is always treated as a "hit" (see notify.Match). Better to over-push
//     than to miss a push.

// ErrNotificationChannelNotFound: the channel does not exist.
var ErrNotificationChannelNotFound = errors.New("notification channel not found")

// Delivery states.
const (
	NotifyStatePending = "pending" // waiting to be sent
	NotifyStateSending = "sending" // claimed by a dispatcher, lease not yet expired
	NotifyStateSent    = "sent"    // delivered
	NotifyStateFailed  = "failed"  // retries exhausted or permanent failure; can be resent manually
	NotifyStateSkipped = "skipped" // channel disabled, no longer sent
)

// Push modes.
const (
	NotifyModeRealtime = "realtime"
	NotifyModeDigest   = "digest"
)

// ValidNotifyMode validates the push mode against an allowlist (same reasoning as findings.status: no DB CHECK is used,
// to ease later extension).
func ValidNotifyMode(m string) bool {
	return m == NotifyModeRealtime || m == NotifyModeDigest
}

// NotificationChannel is the configuration of one channel instance. Config and Filter are kept as raw JSON,
// and parsing is left to the notify package — the db layer does not understand the meaning of their fields.
type NotificationChannel struct {
	ID     int64           `json:"id"`
	Name   string          `json:"name"`
	Kind   string          `json:"kind"`
	Mode   string          `json:"mode"`
	Config json.RawMessage `json:"config"`
	Filter json.RawMessage `json:"filter"`
	// Enabled is a pointer to distinguish "this field was not passed" from "explicitly passed false" —
	// the frontend toggle control submits only the fields that were changed.
	Enabled    *bool     `json:"enabled,omitempty"`
	RatePerMin int       `json:"rate_per_min"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// IsEnabled reports whether the channel is enabled; when Enabled is nil (not loaded) it is treated as enabled.
func (c *NotificationChannel) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// NotificationEvent is one event fact.
type NotificationEvent struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	FindingID int64           `json:"finding_id"`
	Snapshot  json.RawMessage `json:"snapshot"`
	CreatedAt time.Time       `json:"created_at"`
}

const notificationChannelCols = `id, name, kind, enabled, config, mode, filter, rate_per_min, created_at, updated_at`

func scanNotificationChannel(sc interface{ Scan(...any) error }) (*NotificationChannel, error) {
	var c NotificationChannel
	var enabled bool
	if err := sc.Scan(&c.ID, &c.Name, &c.Kind, &enabled, &c.Config, &c.Mode, &c.Filter, &c.RatePerMin, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.Enabled = &enabled
	return &c, nil
}

// ListNotificationChannels returns all channel instances, enabled ones first, ties broken by id.
// The ordering lives in SQL so that the UI and the dispatcher see the same stable order.
func (d *DB) ListNotificationChannels(ctx context.Context) ([]*NotificationChannel, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+notificationChannelCols+` FROM notification_channels
ORDER BY enabled DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*NotificationChannel{}
	for rows.Next() {
		c, err := scanNotificationChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NotificationChannelByID fetches a single channel.
func (d *DB) NotificationChannelByID(ctx context.Context, id int64) (*NotificationChannel, error) {
	row := d.QueryRowContext(ctx, `SELECT `+notificationChannelCols+` FROM notification_channels WHERE id=$1`, id)
	c, err := scanNotificationChannel(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotificationChannelNotFound
	}
	return c, err
}

// SaveNotificationChannel creates or updates a channel.
//
// On update, only the fields explicitly given by the caller (non-nil / non-empty) are overwritten, so that the frontend can submit a drawer form
// with partial changes without sending back the config fields it did not display — sending them back would instead cause the accident of
// "the masked value overwriting the real secret".
func (d *DB) SaveNotificationChannel(ctx context.Context, c *NotificationChannel) (int64, error) {
	if c.Mode == "" {
		c.Mode = NotifyModeRealtime
	}
	// Deliberately do **not** apply any processing to 0 here: 0 is a valid config and means "no rate limit".
	//
	// It used to be written as `if c.RatePerMin <= 0 { c.RatePerMin = default value }`, intended as "give a safe
	// default when unspecified", but that also swallowed an "explicit 0" — the docs, UI hints and
	// takeTokens all interpret 0 as no rate limit, yet only this place quietly changed it to 20 (DingTalk/WeCom/Telegram)
	// or 100 (Feishu); the operator thought the limit was lifted but was actually held at 20/minute with no indication.
	//
	// The difference between "unspecified" and "explicit 0" is known only to the caller (field absent vs explicitly 0 in the request body),
	// so the default is filled in by the server layer when the field is absent; see notifyCreateChannel.
	if c.RatePerMin < 0 {
		return 0, errors.New("rate limit must not be negative")
	}
	if c.Config == nil {
		c.Config = json.RawMessage(`{}`)
	}
	if c.Filter == nil {
		c.Filter = json.RawMessage(`{}`)
	}
	enabled := c.IsEnabled()

	if c.ID == 0 {
		var id int64
		err := d.QueryRowContext(ctx, `INSERT INTO notification_channels(name,kind,enabled,config,mode,filter,rate_per_min)
VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
			c.Name, c.Kind, enabled, string(c.Config), c.Mode, string(c.Filter), c.RatePerMin).Scan(&id)
		return id, err
	}
	res, err := d.ExecContext(ctx, `UPDATE notification_channels
SET name=$2, kind=$3, enabled=$4, config=$5, mode=$6, filter=$7, rate_per_min=$8
WHERE id=$1`,
		c.ID, c.Name, c.Kind, enabled, string(c.Config), c.Mode, string(c.Filter), c.RatePerMin)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotificationChannelNotFound
	}
	return c.ID, nil
}

// SetNotificationChannelEnabled toggles enabled/disabled.
//
// When disabling a channel, its not-yet-sent deliveries are also marked skipped: otherwise after re-enabling
// the user would suddenly receive a batch of old findings "backlogged while disabled", which are no longer timely and easily mistaken for new ones.
func (d *DB) SetNotificationChannelEnabled(ctx context.Context, id int64, enabled bool) error {
	return d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE notification_channels SET enabled=$2 WHERE id=$1`, id, enabled)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotificationChannelNotFound
		}
		if !enabled {
			if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET state=$2, last_error=$3
WHERE channel_id=$1 AND state IN ($4,$5)`,
				id, NotifyStateSkipped, "渠道已停用", NotifyStatePending, NotifyStateSending); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteNotificationChannel deletes a channel. Its delivery history is cascade-deleted via the foreign key
// (with the channel config gone, the history can no longer be interpreted).
func (d *DB) DeleteNotificationChannel(ctx context.Context, id int64) error {
	res, err := d.ExecContext(ctx, `DELETE FROM notification_channels WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotificationChannelNotFound
	}
	return nil
}

// RecordNotificationEventTx writes a push event **on a best-effort basis** inside the caller's transaction.
//
// This is the only notification-related change on the finding-write path: a single INSERT that reads no table, knows no channel,
// and runs no filter. Committing the transaction guarantees that "the finding is persisted" and "the push job exists" are atomically consistent,
// with no window in which the commit succeeds but nothing is enqueued and the message is lost forever.
//
// Two key design points, neither written casually:
//
//  1. **Why a SAVEPOINT**: in PostgreSQL, an error in any statement inside a transaction puts the whole transaction into the
//     aborted state, after which all statements (including COMMIT) fail. So "ignore the error of this INSERT
//     and let the caller keep committing" is impossible in PG — unless a savepoint isolates the error to
//     this one statement. Without a savepoint, the only option left is "roll back the whole thing".
//
//  2. **Why rolling back the whole thing is wrong**: push is a convenience feature, while the finding record is the product itself. A problem in the notification
//     table (an unmigrated old DB, a transient disk fault) must not stop a high-severity finding from being saved. So here the
//     error is isolated, logged, and false is returned, letting the finding write commit as usual — at the cost of losing this one push.
//     Returning bool rather than error is deliberate: the caller should not treat it as an error that affects whether the write succeeds.
func RecordNotificationEventTx(ctx context.Context, tx *sql.Tx, kind string, findingID int64, snap notify.Snapshot) bool {
	raw, err := json.Marshal(snap)
	if err != nil {
		log.Printf("[notify] failed to serialize push event finding=%d: %v", findingID, err)
		return false
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT notify_event`); err != nil {
		log.Printf("[notify] failed to create savepoint finding=%d: %v", findingID, err)
		return false
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO notification_events(kind,finding_id,snapshot) VALUES($1,$2,$3)`,
		kind, findingID, string(raw)); err != nil {
		log.Printf("[notify] failed to write push event finding=%d (finding record unaffected): %v", findingID, err)
		// Roll back to the savepoint to rescue the transaction from the aborted state.
		if _, rbErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT notify_event`); rbErr != nil {
			log.Printf("[notify] failed to roll back to savepoint finding=%d: %v", findingID, rbErr)
		}
		return false
	}
	// Release the savepoint, to avoid accumulating useless savepoints in a long transaction.
	_, _ = tx.ExecContext(ctx, `RELEASE SAVEPOINT notify_event`)
	return true
}

// AddNotificationEvent is the standalone-transaction version of InsertNotificationEventTx, for call sites that are not
// inside an existing transaction (such as a channel's "send test message", which has no real finding).
func (d *DB) AddNotificationEvent(ctx context.Context, kind string, findingID int64, snap notify.Snapshot) (int64, error) {
	raw, err := json.Marshal(snap)
	if err != nil {
		return 0, fmt.Errorf("serialize notification event snapshot: %w", err)
	}
	var id int64
	err = d.QueryRowContext(ctx, `INSERT INTO notification_events(kind,finding_id,snapshot) VALUES($1,$2,$3) RETURNING id`,
		kind, findingID, string(raw)).Scan(&id)
	return id, err
}

// FanOutPendingEvents expands not-yet-dispatched finding events into delivery jobs according to the currently enabled channels,
// and returns the number of events processed this round and the number of deliveries newly created.
//
// The whole round runs in one transaction: events are claimed with FOR UPDATE SKIP LOCKED, so several processes running at the same time
// each claim different rows (the archive queue in this project claims jobs with the same technique, see
// completeNextArchiveJob in db/task_archives.go).
//
// Filter matching is deliberately placed on the Go side rather than in SQL: a channel's filter condition is a JSONB of optional fields,
// and expressing the six combinations in SQL would make the query hard to maintain, while the number of channels is "a few configured by hand";
// loading them all and comparing one by one in memory is faster and easier to test.
//
// Events that hit no channel are also marked fanned_out — otherwise they would remain in the pending-dispatch set forever
// and be rescanned on every tick.
func (d *DB) FanOutPendingEvents(ctx context.Context, limit int) (eventCount, deliveryCount int, err error) {
	if limit <= 0 {
		limit = 200
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback() //nolint:errcheck // 提交成功后是 no-op

	channels, err := listEnabledNotificationChannelsTx(ctx, tx)
	if err != nil {
		return 0, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, kind, finding_id, snapshot FROM notification_events
WHERE NOT fanned_out ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return 0, 0, err
	}
	var (
		events      []NotificationEvent
		parsedSnaps []notify.Snapshot
	)
	for rows.Next() {
		var ev NotificationEvent
		if err := rows.Scan(&ev.ID, &ev.Kind, &ev.FindingID, &ev.Snapshot); err != nil {
			rows.Close()
			return 0, 0, err
		}
		var snap notify.Snapshot
		// The snapshot is written by ourselves, so in theory it must be parseable; a parse failure does not block the delivery flow,
		// but this event will be skipped by all channels with filter conditions because its fields are all empty — better to push one fewer
		// than to let a bad row stall the whole queue.
		_ = json.Unmarshal(ev.Snapshot, &snap)
		// kind is taken from the in-row value: the one in the snapshot is a copy for rendering and may have been written by an older version.
		snap.Kind = ev.Kind
		events = append(events, ev)
		parsedSnaps = append(parsedSnaps, snap)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(events) == 0 {
		return 0, 0, tx.Commit()
	}

	type pending struct {
		eventID   int64
		channelID int64
	}
	var toInsert []pending
	for i, snap := range parsedSnaps {
		for _, ch := range channels {
			if !notify.Match(notify.ParseFilter(ch.Filter), snap) {
				continue
			}
			toInsert = append(toInsert, pending{eventID: events[i].ID, channelID: ch.ID})
		}
	}
	if len(toInsert) > 0 {
		var (
			vals []string
			args []any
		)
		for _, p := range toInsert {
			vals = append(vals, fmt.Sprintf("($%d,$%d)", len(args)+1, len(args)+2))
			args = append(args, p.eventID, p.channelID)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notification_deliveries(event_id,channel_id) VALUES `+strings.Join(vals, ","), args...); err != nil {
			return 0, 0, err
		}
	}

	// Mark this round's events as dispatched. Events that hit no channel are marked too (see the function comment).
	ids := make([]string, 0, len(events))
	markArgs := make([]any, 0, len(events))
	for _, ev := range events {
		markArgs = append(markArgs, ev.ID)
		ids = append(ids, fmt.Sprintf("$%d", len(markArgs)))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_events SET fanned_out=true WHERE id IN (`+strings.Join(ids, ",")+`)`, markArgs...); err != nil {
		return 0, 0, err
	}
	return len(events), len(toInsert), tx.Commit()
}

// listEnabledNotificationChannelsTx fetches the enabled channels within a transaction. There are very few of them,
// so no pagination and no cache — a cache would introduce the extra timing question of "when does a config change take effect".
func listEnabledNotificationChannelsTx(ctx context.Context, tx *sql.Tx) ([]*NotificationChannel, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, name, kind, config, mode, filter, rate_per_min
FROM notification_channels WHERE enabled ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*NotificationChannel{}
	for rows.Next() {
		var c NotificationChannel
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.Config, &c.Mode, &c.Filter, &c.RatePerMin); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// NotificationAssetNames resolves asset ids into short display names, for use in push messages.
//
// The return order matches the input, and the length may be shorter than the input (non-existent ids are skipped). Keeping the input order is
// so that the asset order in messages for the same finding stays stable across multiple deliveries — otherwise the message received after a retry
// would have a different asset order and be misread as "the assets changed".
func (d *DB) NotificationAssetNames(ctx context.Context, ids []int64) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph, args := placeholders(1, ids)
	rows, err := d.QueryContext(ctx, `SELECT id, type, domain, ip, url, app_name, bundle_id FROM assets WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	labels := map[int64]string{}
	for rows.Next() {
		var (
			id                int64
			typ               string
			domain, ip, url   sql.NullString
			appName, bundleID sql.NullString
		)
		if err := rows.Scan(&id, &typ, &domain, &ip, &url, &appName, &bundleID); err != nil {
			return nil, err
		}
		labels[id] = assetDisplayName(typ, domain.String, ip.String, url.String, appName.String, bundleID.String)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if label, ok := labels[id]; ok && label != "" {
			out = append(out, label)
		}
	}
	return out, nil
}

// assetDisplayName picks the most recognizable identifier according to the asset type.
// As a fallback it returns an empty string, and the caller decides how to present "an asset whose name cannot be obtained" — this function does not invent placeholders,
// otherwise noise like "asset #42" would get mixed into push messages and readers would take it for a real domain.
func assetDisplayName(typ, domain, ip, url, appName, bundleID string) string {
	pick := func(vals ...string) string {
		for _, v := range vals {
			if strings.TrimSpace(v) != "" {
				return v
			}
		}
		return ""
	}
	switch typ {
	case "root_domain", "subdomain":
		return domain
	case "ip":
		return ip
	case "app":
		return pick(appName, bundleID)
	case "service", "endpoint":
		return pick(url, domain, ip)
	default:
		return pick(domain, ip, url, appName)
	}
}

// SetFindingStatusWithNotify updates a finding's disposition status and, in the same transaction, registers a status-change
// push event.
//
// Returns from = the status before the change; found = whether the finding exists; notified = whether the event was registered successfully.
//
// Three deliberate behaviors:
//   - No event is registered when the status did not actually change. The frontend drawer resubmitting the same value, or an automation script
//     replaying idempotently, should not produce push noise.
//   - When the finding does not exist it returns found=false and writes nothing; the caller translates that into a 404.
//   - A failure to register the event does not affect the status update (see the savepoint note on RecordNotificationEventTx),
//     so when notified=false the status was already changed successfully and the caller should not report an error because of it.
func (d *DB) SetFindingStatusWithNotify(ctx context.Context, id int64, status string) (from string, found bool, notified bool, err error) {
	err = d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		var txErr error
		from, found, _, notified, txErr = SetFindingStatusTx(ctx, tx, id, status)
		return txErr
	})
	return from, found, notified, err
}

// SetFindingStatusTx updates a finding's status and registers a status-change push event **inside the caller's transaction**.
//
// It is extracted as a transaction-level function so that all paths that change status share the same semantics — previously only
// patchFinding used the notifying version, while **when a retest concludes "fixed"** (the
// `UPDATE findings SET status=...` in finding_retests) the DB was written directly, so channels configured with
// `on_status_change` received no push at all for this kind of status transition: the status quietly changed in the UI, and
// operators only found out when they opened the platform.
//
// Returns from = the status before the change, found = whether the finding exists, changed = whether the status really changed,
// notified = whether the event was registered successfully (a registration failure does not affect the status update, see RecordNotificationEventTx).
func SetFindingStatusTx(ctx context.Context, tx *sql.Tx, id int64, status string) (from string, found bool, changed bool, notified bool, err error) {
	var (
		vulnclass, name, severity, summary string
		taskID                             sql.NullInt64
		assetIDs                           []byte
	)
	scanErr := tx.QueryRowContext(ctx, `SELECT vulnclass, name, severity, summary, task_id, asset_ids, status
FROM findings WHERE id=$1 FOR UPDATE`, id).
		Scan(&vulnclass, &name, &severity, &summary, &taskID, &assetIDs, &from)
	if scanErr == sql.ErrNoRows {
		return "", false, false, false, nil
	}
	if scanErr != nil {
		return "", false, false, false, scanErr
	}
	found = true
	if from == status {
		// If the status did not really change, no event is registered: resubmitting the same value or an idempotent replay should not
		// produce push noise.
		return from, true, false, false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE findings SET status=$2 WHERE id=$1`, id, status); err != nil {
		return from, true, false, false, err
	}
	var assets []int64
	_ = json.Unmarshal(assetIDs, &assets)
	notified = RecordNotificationEventTx(ctx, tx, notify.EventFindingStatusChanged, id, notify.Snapshot{
		Kind:       notify.EventFindingStatusChanged,
		FindingID:  id,
		TaskID:     taskID.Int64,
		VulnClass:  vulnclass,
		Name:       name,
		Severity:   severity,
		Summary:    summary,
		AssetIDs:   assets,
		FromStatus: from,
		ToStatus:   status,
	})
	return from, true, true, notified, nil
}

// NotificationStats is the overview count at the top of the notification page.
type NotificationStats struct {
	Channels     int   `json:"channels"`
	ChannelsOn   int   `json:"channels_on"`
	Pending      int   `json:"pending"`
	Failed       int   `json:"failed"`
	SentToday    int   `json:"sent_today"`
	BacklogAgeMS int64 `json:"backlog_age_ms"` // age in milliseconds of the oldest pending delivery
}

// NotificationStatsSnapshot summarizes the health of the notification system.
// BacklogAgeMS is the most direct indicator of "is the push stuck" — far more useful than the pending count,
// because the same backlog of 3 items can mean anything from 3 seconds to 3 hours of lag.
func (d *DB) NotificationStatsSnapshot(ctx context.Context) (*NotificationStats, error) {
	var s NotificationStats
	if err := d.QueryRowContext(ctx, `SELECT
    (SELECT count(*) FROM notification_channels),
    (SELECT count(*) FROM notification_channels WHERE enabled),
    (SELECT count(*) FROM notification_deliveries WHERE state IN ($1,$2)),
    (SELECT count(*) FROM notification_deliveries WHERE state=$3),
    (SELECT count(*) FROM notification_deliveries WHERE state=$4 AND sent_at >= date_trunc('day', now())),
    COALESCE((SELECT EXTRACT(EPOCH FROM (now() - min(created_at))) * 1000 FROM notification_deliveries WHERE state=$1), 0)::bigint`,
		NotifyStatePending, NotifyStateSending, NotifyStateFailed, NotifyStateSent).
		Scan(&s.Channels, &s.ChannelsOn, &s.Pending, &s.Failed, &s.SentToday, &s.BacklogAgeMS); err != nil {
		return nil, err
	}
	return &s, nil
}
