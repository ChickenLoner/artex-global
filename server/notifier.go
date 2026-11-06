package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/notify"
)

// Global setting keys (stored in the settings key-value table; no table creation needed).
const (
	// settingNotifyEnabled is the master switch for pushing. On by default: it exists as a one-click stop during maintenance,
	// not as the condition for enabling the feature -- the real condition is "whether any channel is configured".
	settingNotifyEnabled = "notify_enabled"
	// settingNotifyPublicBaseURL is the externally reachable address used to generate vulnerability-detail back-links
	// (e.g. https://artex.example.com). If left empty, messages carry no back-link button.
	// The project has no reusable external-address config, so one is added here.
	settingNotifyPublicBaseURL = "notify_public_base_url"
	// settingNotifyDigestMinutes is the period (in minutes) of digest mode.
	settingNotifyDigestMinutes = "notify_digest_interval_min"
)

const (
	// notifyTick is the polling interval of the delivery engine. 3 seconds is the upper bound of this engine's real-time responsiveness
	// and also the main source of delay between "vulnerability persisted" and "message reaches the IM".
	notifyTick = 3 * time.Second
	// When notifyBackgroundDisabledEnv is set, the server does not start the background delivery loop
	// (newNotifier(s).Run). Integration tests call stepRealtime and stepDigest
	// directly to verify the dispatch results deterministically; if the 3-second background loop handled
	// the same channel concurrently, the aggregation would wobble with timing. This is a
	// test-only switch; without the variable the server starts as usual (production behavior unchanged).
	notifyBackgroundDisabledEnv = "ARTEX_NOTIFY_BACKGROUND_DISABLED"
	// notifyLease is the lease duration when claiming deliveries. It must be significantly larger than the worst-case time of a single delivery
	// (the notify package's HTTP client timeout is 15 seconds), otherwise the same row may be delivered by two
	// dispatchers at the same time.
	notifyLease = 3 * time.Minute
	// notifyFanOutPerTick limits the number of events dispatched per round, to avoid expanding the whole historical backlog
	// into delivery tasks in one go when a channel is first enabled.
	notifyFanOutPerTick = 200
	// notifyDefaultDigestMinutes is the default digest period.
	notifyDefaultDigestMinutes = 30
	// notifyUnlimitedBurstPerTick is the per-round delivery cap when a channel has no rate limit.
	// It exists to prevent "one channel configured as unlimited + a scan producing thousands of vulnerabilities" from
	// turning a single round of the loop into a long blocking operation.
	notifyUnlimitedBurstPerTick = 50
	// notifyMaxSendsPerChannelPerTick is the maximum number of deliveries per channel per round.
	//
	// This cap is derived backwards from the **lease duration**: claiming stamps the rows with a lease (notifyLease = 3 minutes),
	// and if so many deliveries are sent serially in one round that the worst-case time exceeds the lease, the lease expires before the last ones are sent.
	// Within a single process that does not matter (Run is a single goroutine running serially, ticks never re-enter), but with **two
	// processes connected to the same DB**, the peer re-claims rows whose lease expired and sends them again, also
	// double-incrementing attempts and judging them failed while the original process is still delivering.
	//
	// Value: 3-minute lease / 30-second single timeout = 6 would **use up the lease exactly**, with zero margin,
	// and cannot be used; 5 leaves a 30-second margin for a worst case of 150 seconds. This relationship is pinned by
	// TestNotifyTickBudgetFitsWithinLease -- changing any one of notifyLease,
	// notifySendTimeout or this value makes that assertion fail.
	notifyMaxSendsPerChannelPerTick = 5
	// notifySendTimeout is the timeout of a single delivery. It also determines the value of the previous constant;
	// their product must not exceed notifyLease, see TestNotifyTickBudgetFitsWithinLease.
	notifySendTimeout = 30 * time.Second
)

// notifyBackoff is the backoff sequence for failure retries, indexed by the number of attempts made.
// 3 chances (including the first) correspond to db.MaxNotifyAttempts; the two must be changed together.
var notifyBackoff = []time.Duration{
	time.Second,
	5 * time.Second,
	30 * time.Second,
}

// Delivery failure / delay reasons. Stored in notification_deliveries.last_error and, via the delivery-history response in
// notify_api.go, shown as-is in the "Delivery records" table (delivery-list.tsx) of the notifications page.
// They are therefore user-facing text and stay in Korean (diagnostic logs via log.Printf keep the original text, Z2).
const (
	errDeliveryChannelKindUnregistered = "등록되지 않은 채널 유형입니다: %q"
	errDeliverySnapshotUnrenderable    = "이벤트 스냅샷을 해석하지 못해 이 취약점을 메시지로 만들지 못했습니다"
	errDeliveryChannelLengthCapped     = "이 메시지가 채널 길이 상한에 도달해 앞의 %d건만 전송했고, 나머지는 다음 배치로 미룹니다"
	errDeliveryNoDeliveredCount        = "채널이 전송 건수를 보고하지 않았습니다(delivered=%d)"
	errDeliveryRetryExhausted          = "%d회 재시도 후에도 실패했습니다: %s"
	errDeliverySnapshotEmpty           = "전달 항목 #%d 의 이벤트 스냅샷이 비어 있습니다"
	errDeliverySnapshotParse           = "전달 항목 #%d 의 이벤트 스냅샷을 해석하지 못했습니다: %w"
	errDeliveryBatchAllUnparseable     = "요약 배치의 전달 %d건을 모두 해석하지 못했습니다"
)

// Notifier is the delivery engine for vulnerability pushes.
//
// It runs as an independent goroutine alongside Scheduler (see server.New). It deliberately does not reuse
// Scheduler's tick: the real-time requirement of pushing (3 seconds) differs from the business rhythm of triggers,
// and the failures of the two do not implicate each other -- a stuck push must not affect agent triggers.
type Notifier struct {
	s  *Server
	pg *db.DB

	// mu protects buckets. Channels are few and contention is low, so a single mutex is enough;
	// it is not worth introducing a finer-grained structure.
	mu      sync.Mutex
	buckets map[int64]*notifyBucket
}

// notifyBucket is the token bucket of a single channel.
//
// A token bucket is used rather than a sliding window that "counts per minute then resets", because the latter has terrible boundary effects:
// sending 20 at the end of a window and another 20 right after is 40 within one second as far as the platform is concerned,
// and would be rate-limited; a token bucket refills at a constant rate and naturally avoids such bursts.
type notifyBucket struct {
	tokens   float64
	lastFill time.Time
}

func newNotifier(s *Server) *Notifier {
	return &Notifier{s: s, pg: s.m.pg, buckets: map[int64]*notifyBucket{}}
}

// Run loops until ctx ends. Started once by server.New.
func (n *Notifier) Run(ctx context.Context) {
	if n.pg == nil {
		return
	}
	t := time.NewTicker(notifyTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n.step(ctx)
		}
	}
}

// step runs one round: first dispatch new events, then deliver the due tasks.
//
// Failure of any step is only logged and never breaks the loop -- a fault in the notification system must never escalate into a process-level problem.
// Each tick is independent, and the next round naturally retries.
func (n *Notifier) step(ctx context.Context) {
	if !n.enabled() {
		return
	}
	if _, _, err := n.pg.FanOutPendingEvents(ctx, notifyFanOutPerTick); err != nil {
		log.Printf("[notify] dispatching events failed: %v", err)
		return
	}
	channels, err := n.pg.ListNotificationChannels(ctx)
	if err != nil {
		log.Printf("[notify] reading channels failed: %v", err)
		return
	}
	baseURL := n.publicBaseURL()
	for _, ch := range channels {
		if !ch.IsEnabled() {
			continue
		}
		// The token bucket's unit of measurement is the **message count** (equivalent to the HTTP request count), not the vulnerability count.
		// In realtime mode the two are the same (one vulnerability, one message); in digest mode a whole batch of vulnerabilities is combined into
		// one message, so it consumes only one token.
		//
		// Both modes ask the token bucket first and then claim according to the quota -- the order must not be reversed, otherwise deliveries blocked by rate limiting
		// would already have consumed retry attempts.
		now := time.Now()
		if ch.Mode == db.NotifyModeDigest {
			tokens, claimLimit := digestTickPlan()
			if n.takeTokens(ch.ID, ch.RatePerMin, tokens, now) <= 0 {
				continue
			}
			n.stepDigest(ctx, ch, claimLimit, baseURL)
			continue
		}
		allow := n.takeTokens(ch.ID, ch.RatePerMin, notifyMaxSendsPerChannelPerTick, now)
		if allow <= 0 {
			continue
		}
		n.stepRealtime(ctx, ch, allow, baseURL)
	}
}

// digestTickPlan returns the token consumption and the batch-size upper bound of a digest channel for this round.
//
// The two return values are **two different dimensions**, which is exactly why this is a separate function:
//
//   - tokens is a message count. A batch of vulnerabilities is combined into one message and sent in one HTTP request, so it is always 1.
//     rate_per_min therefore still applies to digest (at most this many digest messages per minute).
//   - claimLimit is the maximum number of vulnerabilities in this batch. It is bounded only by memory and has nothing to do with the request budget.
//
// Previously, to make rate_per_min apply to digest, the per-round request budget
// (notifyMaxSendsPerChannelPerTick, derived backwards from the lease) was passed down directly as the batch size.
// As a result a channel with rate_per_min=20 only accumulated 1 token within a 3-second tick, so each digest
// message held only 1 vulnerability -- digest degraded into "real-time push with digest wording", and readers received a stream of
// "1 new vulnerability in the last 30 minutes", while db.MaxDigestBatchSize was never reachable.
//
// This symptom is hard to spot in end-to-end tests (the existing cases all manually pass a large enough limit to
// stepDigest, bypassing the quota computation in step), so the decision is gathered here and pinned directly by
// TestDigestTickPlanDecouplesBatchSizeFromSendBudget.
func digestTickPlan() (tokens, claimLimit int) {
	return 1, db.MaxDigestBatchSize
}

// stepRealtime claims and delivers the realtime tasks of a channel, one message per vulnerability.
func (n *Notifier) stepRealtime(ctx context.Context, ch *db.NotificationChannel, allow int, baseURL string) {
	deliveries, err := n.pg.ClaimRealtimeDeliveries(ctx, ch.ID, allow, notifyLease)
	if err != nil {
		log.Printf("[notify] claiming realtime deliveries failed channel=%d: %v", ch.ID, err)
		return
	}
	if len(deliveries) == 0 {
		return
	}
	channel, cfg, ok := n.adapt(ch)
	if !ok {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), fmt.Sprintf(trMsg(errDeliveryChannelKindUnregistered), ch.Kind))
		return
	}
	for _, dl := range deliveries {
		msg, err := n.renderSingle(ctx, dl, baseURL)
		if err != nil {
			// A render failure is a local data problem; retrying will not help.
			_ = n.pg.FailDeliveries(ctx, []int64{dl.ID}, err.Error())
			continue
		}
		n.send(ctx, channel, cfg, msg, []*db.NotificationDelivery{dl})
	}
}

// stepDigest aggregates a channel's pending deliveries into one message when the batch is due.
func (n *Notifier) stepDigest(ctx context.Context, ch *db.NotificationChannel, allow int, baseURL string) {
	window := n.digestInterval()
	due, err := n.pg.DigestBatchDue(ctx, ch.ID, window)
	if err != nil {
		log.Printf("[notify] checking digest batch due failed channel=%d: %v", ch.ID, err)
		return
	}
	if !due {
		return
	}
	deliveries, err := n.pg.ClaimDigestBatch(ctx, ch.ID, allow, notifyLease)
	if err != nil {
		log.Printf("[notify] claiming digest batch failed channel=%d: %v", ch.ID, err)
		return
	}
	if len(deliveries) == 0 {
		return
	}
	channel, cfg, ok := n.adapt(ch)
	if !ok {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), fmt.Sprintf(trMsg(errDeliveryChannelKindUnregistered), ch.Kind))
		return
	}
	msg, included, err := n.renderBatch(ctx, deliveries, baseURL, int(window.Minutes()))
	if err != nil {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), err.Error())
		return
	}
	// Deliveries whose snapshot is broken and that did not make it into the message must be explicitly judged failed. Otherwise they would stay
	// outside included, in neither the message nor the failure list -- when the send succeeds their status would be missed by the
	// later bulk marking, and stay in sending until the lease expires and they are claimed over and over.
	if skipped := excludeDeliveries(deliveries, included); len(skipped) > 0 {
		reason := errDeliverySnapshotUnrenderable
		if fErr := n.pg.FailDeliveries(ctx, deliveryIDs(skipped), reason); fErr != nil {
			log.Printf("[notify] marking bad-snapshot deliveries failed channel=%s ids=%v: %v", ch.Kind, deliveryIDs(skipped), fErr)
		}
		log.Printf("[notify] skipped %d deliveries with unparseable snapshots channel=%d", len(skipped), ch.ID)
	}
	// Hand only those that made it into the message to send: included[i] corresponds strictly to msg.Items[i],
	// and send relies on this correspondence to land "the channel reports that the first K were fitted" on the correct delivery rows.
	n.send(ctx, channel, cfg, msg, included)
}

// send delivers and transitions state according to the result.
//
// The same batch of deliveries (possibly dozens in digest mode) shares one send result: either delivered, or the whole batch is retried.
// No per-item retry -- a digest message is one message, and resending part of it would corrupt the batch semantics.
//
// The only exception is **segmentation caused by the channel length limit**: the channel reports that only the first K were fitted,
// so item K+1 onward must wait for the next batch rather than being marked successful along with the rest. Otherwise the truncated
// vulnerabilities would be neither in the message nor in the failure list, and vanish completely.
func (n *Notifier) send(ctx context.Context, channel notify.Channel, cfg map[string]any, msg notify.Message, deliveries []*db.NotificationDelivery) {
	// Cap a single delivery so that one stuck channel does not hold up all the remaining channels of this round.
	sendCtx, cancel := context.WithTimeout(ctx, notifySendTimeout)
	defer cancel()
	delivered, err := channel.Send(sendCtx, cfg, msg)
	if err == nil && delivered > 0 {
		if delivered > len(deliveries) {
			// The count a channel reports cannot exceed the number of deliveries; if it does, the render layer miscounted,
			// and treating everything as delivered while logging the problem beats corrupting the records.
			log.Printf("[notify] channel reported %d delivered, exceeding the %d deliveries channel=%s; treating all as delivered",
				delivered, len(deliveries), channel.Kind())
			delivered = len(deliveries)
		}
		sent, rest := deliveries[:delivered], deliveries[delivered:]
		if err := n.pg.MarkDeliveriesSent(ctx, deliveryIDs(sent)); err != nil {
			log.Printf("[notify] marking deliveries as delivered failed channel=%s ids=%v: %v", channel.Kind(), deliveryIDs(sent), err)
		}
		if len(rest) > 0 {
			// This message hit the channel length limit: requeue the rest immediately, to be continued by the next tick.
			// Use DeferDeliveries rather than RescheduleDeliveries -- this is not a failure,
			// so it must not consume the retry budget (claiming already optimistically did +1, which is decremented back there).
			if err := n.pg.DeferDeliveries(ctx, deliveryIDs(rest),
				fmt.Sprintf(trMsg(errDeliveryChannelLengthCapped), delivered)); err != nil {
				log.Printf("[notify] requeuing continuation segment failed channel=%s ids=%v: %v", channel.Kind(), deliveryIDs(rest), err)
			}
		}
		return
	}
	if err == nil {
		// The channel neither reported an error nor said how many were delivered. Treat as failure (take the backoff path),
		// so this delivery is not claimed over and over yet never marked.
		err = fmt.Errorf(trMsg(errDeliveryNoDeliveredCount), delivered)
	}

	// Failure handling is decided **per item**, rather than judging by the max attempt count of the whole batch.
	//
	// It used to be `if maxAttempts(deliveries) >= MaxNotifyAttempts` condemning the whole batch, but the attempt counts
	// of the items in a batch differ: an old delivery that already retried twice (attempts=2) would drag brand-new deliveries in the same batch
	// (attempts=1) into failed with it -- a new vulnerability permanently lost without using a single retry,
	// exactly the opposite of the original intent of "not letting old rows drag new rows down".
	permanent := notify.IsPermanent(err)
	var failIDs, exhaustedIDs []int64
	byDelay := map[time.Duration][]int64{}
	for _, dl := range deliveries {
		switch {
		case permanent:
			failIDs = append(failIDs, dl.ID)
		case dl.Attempts >= db.MaxNotifyAttempts:
			exhaustedIDs = append(exhaustedIDs, dl.ID)
		default:
			delay := notifyBackoff[min(dl.Attempts, len(notifyBackoff)-1)]
			byDelay[delay] = append(byDelay[delay], dl.ID)
		}
	}

	if len(failIDs) > 0 {
		if fErr := n.pg.FailDeliveries(ctx, failIDs, err.Error()); fErr != nil {
			log.Printf("[notify] marking failed status errored channel=%s ids=%v: %v", channel.Kind(), failIDs, fErr)
		}
	}
	if len(exhaustedIDs) > 0 {
		reason := fmt.Sprintf(trMsg(errDeliveryRetryExhausted), db.MaxNotifyAttempts, err)
		if fErr := n.pg.FailDeliveries(ctx, exhaustedIDs, reason); fErr != nil {
			log.Printf("[notify] marking failed status errored channel=%s ids=%v: %v", channel.Kind(), exhaustedIDs, fErr)
		}
	}
	// Regroup the reschedule by delay: there are only 3 backoff tiers, so the number of groups is naturally small; no need to issue a separate
	// UPDATE for each item (that would make a 500-item batch produce 500 round trips).
	for delay, group := range byDelay {
		if rErr := n.pg.RescheduleDeliveries(ctx, group, delay, err.Error()); rErr != nil {
			log.Printf("[notify] rescheduling deliveries failed channel=%s ids=%v: %v", channel.Kind(), group, rErr)
		}
	}
	if len(failIDs)+len(exhaustedIDs) > 0 {
		log.Printf("[notify] delivery failed channel=%d kind=%s permanent=%d exhausted=%d retrying=%d: %s",
			deliveries[0].ChannelID, channel.Kind(), len(failIDs), len(exhaustedIDs), len(byDelay), err)
	}
}

// excludeDeliveries returns those in all that are not in keep (compared by pointer identity).
// Used to find the deliveries that "did not make it into the message" -- they must be handled explicitly and not left in a gray zone.
func excludeDeliveries(all, keep []*db.NotificationDelivery) []*db.NotificationDelivery {
	inKeep := make(map[*db.NotificationDelivery]bool, len(keep))
	for _, dl := range keep {
		inKeep[dl] = true
	}
	var out []*db.NotificationDelivery
	for _, dl := range all {
		if !inKeep[dl] {
			out = append(out, dl)
		}
	}
	return out
}

// adapt obtains the channel implementation and parses its config.
// Returning ok=false means the type is not registered; the delivery should be judged failed directly rather than retried indefinitely.
func (n *Notifier) adapt(ch *db.NotificationChannel) (notify.Channel, map[string]any, bool) {
	channel, ok := notify.Get(ch.Kind)
	if !ok {
		return nil, nil, false
	}
	var cfg map[string]any
	if len(ch.Config) > 0 {
		// On config parse failure give an empty map: the channel's own Validate will report "which field is missing",
		// and that error guides the user to a fix better than a JSON parse error.
		_ = json.Unmarshal(ch.Config, &cfg)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return channel, cfg, true
}

// renderSingle renders a single-vulnerability message.
func (n *Notifier) renderSingle(ctx context.Context, dl *db.NotificationDelivery, baseURL string) (notify.Message, error) {
	snap, err := parseSnapshot(dl)
	if err != nil {
		return notify.Message{}, err
	}
	item, err := n.itemFor(ctx, snap, baseURL)
	if err != nil {
		return notify.Message{}, err
	}
	return notify.Message{Items: []notify.Item{item}, HomeURL: baseURL}, nil
}

// renderBatch renders a digest message. Snapshots are parsed one by one -- if a single one is broken only that one is skipped,
// so it does not drag down the whole digest.
//
// The return value included corresponds **strictly one-to-one** with msg.Items (the i-th delivery <-> the i-th item).
// This correspondence is a hard requirement: the caller decides, from "the channel reports that the first K were fitted", which first K deliveries
// to mark as delivered. If a bad snapshot were skipped here without removing the skipped delivery from included,
// the indices would be misaligned -- a bad item that should fail would be marked delivered, and good items misjudged as undelivered.
// The broken ones are explicitly marked failed by the caller, see stepDigest.
func (n *Notifier) renderBatch(ctx context.Context, deliveries []*db.NotificationDelivery, baseURL string, windowMinutes int) (notify.Message, []*db.NotificationDelivery, error) {
	items := make([]notify.Item, 0, len(deliveries))
	included := make([]*db.NotificationDelivery, 0, len(deliveries))
	for _, dl := range deliveries {
		snap, err := parseSnapshot(dl)
		if err != nil {
			// A bad snapshot enters neither the message nor included -- its handling is the caller's responsibility
			// (explicitly marked failed, rather than slipping through mixed in with "delivered").
			log.Printf("[notify] skipping unparseable snapshot in digest batch delivery=%d: %v", dl.ID, err)
			continue
		}
		item, err := n.itemFor(ctx, snap, baseURL)
		if err != nil {
			return notify.Message{}, nil, err
		}
		items = append(items, item)
		included = append(included, dl)
	}
	if len(items) == 0 {
		return notify.Message{}, nil, fmt.Errorf(trMsg(errDeliveryBatchAllUnparseable), len(deliveries))
	}
	return notify.Message{
		Items:         items,
		Batch:         true,
		WindowMinutes: windowMinutes,
		HomeURL:       baseURL,
	}, included, nil
}

// itemFor renders an event snapshot into an item to push, also resolving the asset name and the detail back-link.
func (n *Notifier) itemFor(ctx context.Context, snap notify.Snapshot, baseURL string) (notify.Item, error) {
	assets, err := n.pg.NotificationAssetNames(ctx, snap.AssetIDs)
	if err != nil {
		// A failure to resolve the asset name should not block the push: not being able to read a name is much lighter than not receiving the notification,
		// and the message merely lacks one asset line.
		log.Printf("[notify] resolving asset name failed finding=%d: %v", snap.FindingID, err)
	}
	item := notify.Item{
		FindingID:  snap.FindingID,
		Name:       snap.Name,
		VulnClass:  snap.VulnClass,
		Severity:   snap.Severity,
		Summary:    snap.Summary,
		Assets:     assets,
		FromStatus: snap.FromStatus,
		ToStatus:   snap.ToStatus,
	}
	if baseURL != "" {
		// For the detail page route see web/src/app/(main)/function/findings/detail/page.tsx,
		// which reads the vulnerability id from the query parameter id.
		item.DetailURL = fmt.Sprintf("%s/function/findings/detail?id=%d", baseURL, snap.FindingID)
	}
	return item, nil
}

// takeTokens takes **at most want** tokens from the channel's token bucket and returns the number actually taken.
//
// One token = one message (one HTTP request). In realtime mode the caller passes as many as it wants;
// in digest mode a whole batch of vulnerabilities sends just one message, so pass 1.
//
// The bucket capacity is the channel's per-minute limit, refilled at a constant rate. ratePerMin<=0 means no rate limit,
// and a finite but large enough value is returned, so a single round of the loop is not held up by an unbounded backlog.
//
// The want cap is necessary: without it the only option is to drain the whole bucket, while the caller has its own per-round cap,
// so the extra tokens taken are useless and vanish into thin air before the next refill -- the burst capacity saved up is never reachable,
// and even a round with "no pending deliveries at all" would still be charged.
func (n *Notifier) takeTokens(channelID int64, ratePerMin, want int, now time.Time) int {
	if want <= 0 {
		return 0
	}
	if ratePerMin <= 0 {
		return min(want, notifyUnlimitedBurstPerTick)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	b := n.buckets[channelID]
	if b == nil {
		b = &notifyBucket{tokens: float64(ratePerMin), lastFill: now}
		n.buckets[channelID] = b
	}
	// Refill by real elapsed time, at a rate of ratePerMin/60 per second.
	if elapsed := now.Sub(b.lastFill).Seconds(); elapsed > 0 {
		b.tokens = minF(float64(ratePerMin), b.tokens+elapsed*float64(ratePerMin)/60)
		b.lastFill = now
	}
	// Add a tiny epsilon before truncating: the token count is accumulated in floating point, and when refilled to full in two steps
	// 0.5 + 0.5 may give 0.9999999999, which a direct int() truncates to 0 --
	// a bucket that is mathematically full yields no token. 1e-9 is far smaller than one token and will not let a real shortfall through.
	take := min(int(b.tokens+1e-9), want)
	if take <= 0 {
		return 0
	}
	b.tokens -= float64(take)
	return take
}

// enabled reads the master switch.
func (n *Notifier) enabled() bool {
	return n.pg.GetBool(settingNotifyEnabled, true)
}

// publicBaseURL returns the external address for back-links, with the trailing slash removed.
func (n *Notifier) publicBaseURL() string {
	v, ok, err := n.pg.GetSetting(settingNotifyPublicBaseURL)
	if err != nil || !ok {
		return ""
	}
	return trimTrailingSlash(v)
}

// digestInterval returns the digest period, falling back to the default when invalid or unconfigured.
func (n *Notifier) digestInterval() time.Duration {
	v, ok, err := n.pg.GetSetting(settingNotifyDigestMinutes)
	if err != nil || !ok {
		return time.Duration(notifyDefaultDigestMinutes) * time.Minute
	}
	m := 0
	if _, err := fmt.Sscanf(v, "%d", &m); err != nil || m <= 0 {
		return time.Duration(notifyDefaultDigestMinutes) * time.Minute
	}
	return time.Duration(m) * time.Minute
}

// parseSnapshot parses the event snapshot corresponding to a delivery.
func parseSnapshot(dl *db.NotificationDelivery) (notify.Snapshot, error) {
	var snap notify.Snapshot
	if len(dl.Snapshot) == 0 {
		return snap, fmt.Errorf(trMsg(errDeliverySnapshotEmpty), dl.ID)
	}
	if err := json.Unmarshal(dl.Snapshot, &snap); err != nil {
		return snap, fmt.Errorf(trMsg(errDeliverySnapshotParse), dl.ID, err)
	}
	if snap.Kind == "" {
		// The event type is taken from the event row; the copy in the snapshot may have been written by an older version.
		snap.Kind = dl.EventKind
	}
	return snap, nil
}

func deliveryIDs(deliveries []*db.NotificationDelivery) []int64 {
	out := make([]int64, 0, len(deliveries))
	for _, dl := range deliveries {
		out = append(out, dl.ID)
	}
	return out
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
