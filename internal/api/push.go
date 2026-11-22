package api

import (
	"net/http"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/egress"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/notify"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/webpush"
)

// Web Push: a browser registers its PushSubscription (endpoint and keys, as
// PushSubscription.toJSON() gives them). The endpoint must be a known push
// service (egress.WebPushAllowed) and is never returned; the keys are sealed
// at rest. Delivery is the scheduler's job.

// Pusher is the Web Push sender, shared with the scheduler so reminders obey
// the same egress setting as everything else.
func (s *Server) Pusher() notify.Pusher { return s.push }

type pushView struct {
	ID           string     `json:"id"`
	EndpointHash string     `json:"endpointHash"` // hex SHA-256 of the endpoint: lets a device find its own entry
	UserAgent    string     `json:"userAgent"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastUsedAt   *time.Time `json:"lastUsedAt,omitempty"`
}

func viewPush(p store.PushSubscription) pushView {
	v := pushView{ID: p.ID, EndpointHash: p.EndpointHash, UserAgent: p.UserAgent, CreatedAt: p.CreatedAt}
	if !p.LastUsedAt.IsZero() {
		v.LastUsedAt = &p.LastUsedAt
	}
	return v
}

func (s *Server) vapidPublicKey(w http.ResponseWriter, r *http.Request, _ *call) error {
	k, err := s.push.PublicKey(r.Context())
	if err != nil {
		return err
	}
	return ok(w, map[string]string{"publicKey": k})
}

func (s *Server) listPushSubscriptions(w http.ResponseWriter, r *http.Request, c *call) error {
	ps, err := s.st.ListPushSubscriptions(r.Context(), c.actor())
	if err != nil {
		return err
	}
	out := make([]pushView, 0, len(ps))
	for _, p := range ps {
		out = append(out, viewPush(p))
	}
	return ok(w, out)
}

func (s *Server) createPushSubscription(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Endpoint string       `json:"endpoint"`
		Keys     webpush.Keys `json:"keys"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := egress.WebPushAllowed(in.Endpoint); err != nil {
		return errf(http.StatusBadRequest, "invalid_push_endpoint")
	}
	keys, err := in.Keys.Canonical()
	if err != nil {
		return errf(http.StatusBadRequest, "invalid_push_keys")
	}
	max, err := s.limits.Limit(r.Context(), limits.PushSubscriptionsPerUser, "")
	if err != nil {
		return err
	}
	id := ids.New()
	sealed, err := notify.SealPush(s.keyring, id, keys)
	if err != nil {
		return err
	}
	p, err := s.st.CreatePushSubscription(r.Context(), c.actor(), store.NewPushSubscription{
		ID: id, Endpoint: in.Endpoint, Ciphertext: sealed, UserAgent: r.UserAgent(), Max: max})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, viewPush(p))
	return nil
}

func (s *Server) deletePushSubscription(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.st.DeletePushSubscription(r.Context(), c.actor(), r.PathValue("id")); err != nil {
		return err
	}
	return noContent(w)
}
