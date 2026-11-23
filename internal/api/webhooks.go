package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/webhook"
)

// Outgoing webhooks (ADR 0017). Household owners manage them; a route here is
// 404 until the admin turns webhooks on. The URL and signing secret are
// sealed: the secret is returned once, on create and on rotation, and the URL
// never (views carry its host).

func (s *Server) registerWebhookRoutes() {
	owner := household(store.RoleOwner).webhooksOnly().noToken()
	const base = "/api/v1/households/{hh}/webhooks"
	s.handle("GET "+base, owner, s.listWebhooks)
	s.handle("POST "+base, owner.withStepUp(), s.createWebhook)
	s.handle("GET "+base+"/{id}", owner, s.getWebhook)
	s.handle("PATCH "+base+"/{id}", owner.withStepUp(), s.patchWebhook) // can redirect signed events
	s.handle("DELETE "+base+"/{id}", owner, s.deleteWebhook)
	s.handle("POST "+base+"/{id}/rotate-secret", owner.withStepUp(), s.rotateWebhookSecret)
	s.handle("POST "+base+"/{id}/test", owner, s.testWebhook)
	s.handle("GET "+base+"/{id}/deliveries", owner, s.listWebhookDeliveries)
}

// WebhookSender is the sender of webhook deliveries, shared with the
// scheduler so test sends and deliveries obey the same egress setting.
func (s *Server) WebhookSender() *webhook.Sender { return s.webhooks }

type webhookView struct {
	ID             string    `json:"id"`
	Host           string    `json:"host"`
	Description    string    `json:"description"`
	Events         []string  `json:"events"`
	Active         bool      `json:"active"`
	AutoDisabled   bool      `json:"autoDisabled"` // switched off after repeated failures
	Failures       int       `json:"failures"`     // consecutive failed attempts
	AutoDisabledAt time.Time `json:"autoDisabledAt,omitzero"`
	LastSuccessAt  time.Time `json:"lastSuccessAt,omitzero"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type webhookCreated struct {
	webhookView
	Secret string `json:"secret"` // shown once
}

func viewWebhook(w store.Webhook) webhookView {
	return webhookView{ID: w.ID, Host: w.URLHint, Description: w.Description, Events: w.Events, Active: w.Active,
		AutoDisabled: !w.AutoDisabledAt.IsZero(), Failures: w.Failures, AutoDisabledAt: w.AutoDisabledAt, LastSuccessAt: w.LastSuccessAt,
		CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt}
}

// webhookError maps validation errors to API errors; none carries the URL.
func webhookError(err error) error {
	if errors.Is(err, webhook.ErrInvalidURL) {
		return errf(http.StatusBadRequest, "invalid_webhook_url")
	}
	return err
}

func (s *Server) auditWebhook(ctx context.Context, c *call, event, id string) {
	s.audit(ctx, c, c.user.ID, event, c.hh, map[string]string{"webhook": id})
}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request, c *call) error {
	ws, err := s.st.ListWebhooks(r.Context(), c.actor(), c.hh)
	if err != nil {
		return err
	}
	out := make([]webhookView, 0, len(ws))
	for _, x := range ws {
		out = append(out, viewWebhook(x))
	}
	return ok(w, out)
}

func (s *Server) getWebhook(w http.ResponseWriter, r *http.Request, c *call) error {
	x, err := s.st.Webhook(r.Context(), c.actor(), c.hh, r.PathValue("id"))
	if err != nil {
		return err
	}
	return ok(w, viewWebhook(x))
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		URL         string   `json:"url"`
		Description string   `json:"description"`
		Events      []string `json:"events"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	max, err := s.limits.Limit(ctx, limits.WebhooksPerHousehold, c.hh)
	if err != nil {
		return err
	}
	p, err := webhook.Prepare(s.keyring, in.URL, in.Description, in.Events, s.EgressGuardEnabled(ctx))
	if err != nil {
		return webhookError(err)
	}
	p.Max = max
	x, err := s.st.CreateWebhook(ctx, c.actor(), c.hh, p.NewWebhook)
	if err != nil {
		return err
	}
	s.auditWebhook(ctx, c, store.AuditWebhookCreate, x.ID)
	writeJSON(w, http.StatusCreated, webhookCreated{viewWebhook(x), p.Secret})
	return nil
}

func (s *Server) patchWebhook(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		URL         *string  `json:"url"`
		Description *string  `json:"description"`
		Events      []string `json:"events"`
		Active      *bool    `json:"active"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	ctx, id := r.Context(), r.PathValue("id")
	u := store.WebhookUpdate{Description: in.Description, Events: in.Events, Active: in.Active}
	if in.URL != nil {
		var err error
		if u.URLCiphertext, u.URLHint, err = webhook.SealURL(s.keyring, id, *in.URL, s.EgressGuardEnabled(ctx)); err != nil {
			return webhookError(err)
		}
	}
	x, err := s.st.UpdateWebhook(ctx, c.actor(), c.hh, id, u)
	if err != nil {
		return err
	}
	s.auditWebhook(ctx, c, store.AuditWebhookUpdate, id)
	return ok(w, viewWebhook(x))
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request, c *call) error {
	id := r.PathValue("id")
	if err := s.st.DeleteWebhook(r.Context(), c.actor(), c.hh, id); err != nil {
		return err
	}
	s.auditWebhook(r.Context(), c, store.AuditWebhookDelete, id)
	return noContent(w)
}

// rotateWebhookSecret replaces the signing secret and returns the new one,
// once. The old secret stops signing at once.
func (s *Server) rotateWebhookSecret(w http.ResponseWriter, r *http.Request, c *call) error {
	ctx, id := r.Context(), r.PathValue("id")
	secret := webhook.NewSecret()
	x, err := s.st.UpdateWebhook(ctx, c.actor(), c.hh, id, store.WebhookUpdate{SecretCiphertext: webhook.SealSecret(s.keyring, id, secret)})
	if err != nil {
		return err
	}
	s.auditWebhook(ctx, c, store.AuditWebhookRotate, id)
	return ok(w, webhookCreated{viewWebhook(x), secret})
}

type webhookDeliveryView struct {
	ID            string    `json:"id"`
	EventID       string    `json:"eventId"`
	Type          string    `json:"type"`
	Status        string    `json:"status"`
	Attempts      int       `json:"attempts"`
	HTTPStatus    int       `json:"httpStatus,omitempty"`
	DurationMS    int       `json:"durationMs"`
	Error         string    `json:"error,omitempty"` // a fixed category, never the URL or the response
	CreatedAt     time.Time `json:"createdAt"`
	FinishedAt    time.Time `json:"finishedAt,omitzero"`
	NextAttemptAt time.Time `json:"nextAttemptAt,omitzero"`
}

func viewWebhookDelivery(d store.WebhookDelivery) webhookDeliveryView {
	return webhookDeliveryView{ID: d.ID, EventID: d.EventID, Type: d.EventType, Status: d.Status, Attempts: d.Attempts, HTTPStatus: d.HTTPStatus,
		DurationMS: d.DurationMS, Error: d.Error, CreatedAt: d.CreatedAt, FinishedAt: d.FinishedAt, NextAttemptAt: d.NextAttemptAt}
}

func (s *Server) listWebhookDeliveries(w http.ResponseWriter, r *http.Request, c *call) error {
	ds, err := s.st.WebhookDeliveries(r.Context(), c.actor(), c.hh, r.PathValue("id"))
	if err != nil {
		return err
	}
	out := make([]webhookDeliveryView, 0, len(ds))
	for _, d := range ds {
		out = append(out, viewWebhookDelivery(d))
	}
	return ok(w, map[string]any{"items": out})
}

// testWebhook sends a ping event now, through the same guarded sender and
// signature as a real delivery, and logs it. It is not retried and does not
// count towards the automatic switch-off.
func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request, c *call) error {
	if !s.allow(w, rateTest+":user:"+c.user.ID) {
		return nil
	}
	ctx, id := r.Context(), r.PathValue("id")
	urlCT, secretCT, err := s.st.WebhookSealed(ctx, c.actor(), c.hh, id)
	if err != nil {
		return err
	}
	rawURL, err := webhook.OpenURL(s.keyring, id, urlCT)
	if err != nil {
		return err
	}
	secret, err := webhook.OpenSecret(s.keyring, id, secretCT)
	if err != nil {
		return err
	}
	evID := ids.New()
	body, err := json.Marshal(store.WebhookPayload{ID: evID, Type: store.EventPing, Version: store.WebhookPayloadVersion,
		CreatedAt: s.now().UTC().Format(time.RFC3339), HouseholdID: c.hh})
	if err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, testSendTimeout)
	defer cancel()
	res, _ := s.webhooks.Send(sctx, rawURL, secret, webhook.Delivery{ID: evID, EventType: store.EventPing, Body: body})
	d, err := s.st.RecordWebhookTest(ctx, c.actor(), c.hh, id, evID, res)
	if err != nil {
		return err
	}
	return ok(w, viewWebhookDelivery(d))
}
