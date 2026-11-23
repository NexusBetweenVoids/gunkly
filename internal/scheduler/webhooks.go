package scheduler

import (
	"context"
	"sync"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/webhook"
)

// WebhookSender sends one signed webhook delivery (webhook.Sender).
type WebhookSender interface {
	Send(ctx context.Context, rawURL, secret string, d webhook.Delivery) (res store.WebhookResult, retry bool)
}

// sendWebhooks claims and sends due webhook deliveries. While the admin
// setting is off nothing is sent; pending deliveries are dropped once when it
// turns (or is found) off, so stale events are not replayed later.
func (w *Worker) sendWebhooks(ctx context.Context) {
	st := w.cfg.Store
	if w.cfg.Webhooks == nil {
		return
	}
	set, err := st.InstanceSettings(ctx)
	if err != nil {
		w.log.Error("scheduler: webhook settings", "err", err)
		return
	}
	was := w.webhooksOn
	w.webhooksOn = &set.WebhooksEnabled
	if !set.WebhooksEnabled {
		if was == nil || *was {
			if err := st.DropPendingWebhookDeliveries(ctx); err != nil {
				w.log.Error("scheduler: drop webhook deliveries", "err", err)
			}
		}
		return
	}
	sendCtx := context.WithoutCancel(ctx)
	sem := make(chan struct{}, w.cfg.Concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	for range maxClaimRounds {
		if ctx.Err() != nil {
			return
		}
		ds, err := st.ClaimWebhookDeliveries(ctx, w.cfg.Now(), claimLease, claimBatch)
		if err != nil {
			if ctx.Err() == nil {
				w.log.Error("scheduler: claim webhook deliveries", "err", err)
			}
			return
		}
		for _, d := range ds {
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer func() { <-sem; wg.Done() }()
				w.deliverWebhook(sendCtx, d)
			}()
		}
		if len(ds) < claimBatch {
			return
		}
		wg.Wait()
	}
}

func (w *Worker) deliverWebhook(ctx context.Context, d store.ClaimedWebhookDelivery) {
	log := w.log.With("webhook_delivery", d.ID, "webhook", d.WebhookID)
	finish := func(res store.WebhookResult, retry bool) {
		if !res.OK && retry && d.Attempts+1 < maxAttempts {
			res.RetryAt = w.cfg.Now().Add(Backoff(d.Attempts + 1))
		}
		off, err := w.cfg.Store.FinishWebhookDelivery(ctx, d.ID, res)
		if err != nil {
			log.Error("scheduler: record webhook delivery", "err", err)
			return
		}
		if off {
			log.Warn("scheduler: webhook switched off after repeated failures")
		}
		if w.cfg.OnDelivery != nil {
			w.cfg.OnDelivery("webhook", res.OK)
		}
	}
	rawURL, err := webhook.OpenURL(w.cfg.Keyring, d.WebhookID, d.URLCiphertext)
	if err != nil {
		log.Error("scheduler: cannot decrypt webhook URL", "err", err)
		finish(store.WebhookResult{Error: "cannot decrypt webhook"}, false)
		return
	}
	secret, err := webhook.OpenSecret(w.cfg.Keyring, d.WebhookID, d.SecretCiphertext)
	if err != nil {
		log.Error("scheduler: cannot decrypt webhook secret", "err", err)
		finish(store.WebhookResult{Error: "cannot decrypt webhook"}, false)
		return
	}
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	res, retry := w.cfg.Webhooks.Send(sctx, rawURL, secret, webhook.Delivery{ID: d.ID, EventType: d.EventType, Body: []byte(d.Payload)})
	if !res.OK {
		log.Warn("scheduler: webhook failed", "attempt", d.Attempts+1, "http", res.HTTPStatus, "err", res.Error)
	}
	finish(res, retry)
}
