package api

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/i18n"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/notify"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// --- Calendar feeds ---
//
// Any member may have feeds, guests included: a feed is read-only and shows
// only what a guest can already see (chore names, areas and due dates; never
// notes or links). The token is returned once, on create and regenerate.

type feedView struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
}

type feedCreated struct {
	feedView
	URL string `json:"url"` // shown once; relative when BASE_URL is unset
}

func (s *Server) feedURL(token string) string { return s.baseURL + "/ical/" + token + ".ics" }

func (s *Server) listFeeds(w http.ResponseWriter, r *http.Request, c *call) error {
	fs, err := s.st.ListFeeds(r.Context(), c.actor(), c.hh)
	if err != nil {
		return err
	}
	out := make([]feedView, 0, len(fs))
	for _, f := range fs {
		out = append(out, feedView{ID: f.ID, CreatedAt: f.CreatedAt})
	}
	return ok(w, out)
}

func (s *Server) createFeed(w http.ResponseWriter, r *http.Request, c *call) error {
	max, err := s.limits.Limit(r.Context(), limits.CalendarFeedsPerUser, "")
	if err != nil {
		return err
	}
	f, token, err := s.st.CreateFeed(r.Context(), c.actor(), c.hh, max)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, feedCreated{feedView{ID: f.ID, CreatedAt: f.CreatedAt}, s.feedURL(token)})
	return nil
}

func (s *Server) deleteFeed(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.st.RevokeFeed(r.Context(), c.actor(), c.hh, r.PathValue("id")); err != nil {
		return err
	}
	return noContent(w)
}

func (s *Server) regenerateFeed(w http.ResponseWriter, r *http.Request, c *call) error {
	id := r.PathValue("id")
	token, err := s.st.RegenerateFeed(r.Context(), c.actor(), c.hh, id)
	if err != nil {
		return err
	}
	return ok(w, map[string]string{"id": id, "url": s.feedURL(token)})
}

// --- Notification channels ---
//
// A channel URL is a secret (bot token, webhook key). It is accepted on
// create and replace, sealed, and never returned: views carry the service
// kind, the label and a masked hint ("…abcd").

type channelView struct {
	ID        string    `json:"id"`
	Scope     string    `json:"scope"`
	Kind      string    `json:"kind"`
	Label     string    `json:"label"`
	Hint      string    `json:"hint"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func viewChannel(c store.Channel) channelView {
	return channelView{ID: c.ID, Scope: c.Scope, Kind: c.Service, Label: c.Label, Hint: c.Hint, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func viewChannels(cs []store.Channel) []channelView {
	out := make([]channelView, 0, len(cs))
	for _, c := range cs {
		out = append(out, viewChannel(c))
	}
	return out
}

// channelURLError maps notify's URL validation errors to API errors. None of
// them carries the URL.
func channelURLError(err error) error {
	switch {
	case errors.Is(err, notify.ErrServiceDisabled):
		return errf(http.StatusBadRequest, "service_disabled")
	case errors.Is(err, notify.ErrInvalidURL), errors.Is(err, notify.ErrUnknownService), errors.Is(err, notify.ErrHostOverride):
		return errf(http.StatusBadRequest, "invalid_channel_url")
	}
	return err
}

type channelInput struct {
	URL   string `json:"url"`
	Label string `json:"label"`
}

func (s *Server) newChannel(ctx context.Context, r *http.Request, kind limits.Kind, hh string) (store.NewChannel, error) {
	var in channelInput
	if err := decode(r, &in); err != nil {
		return store.NewChannel{}, err
	}
	kinds, err := s.EnabledKinds(ctx)
	if err != nil {
		return store.NewChannel{}, err
	}
	nc, err := notify.Prepare(s.keyring, in.URL, in.Label, kinds)
	if err != nil {
		return store.NewChannel{}, channelURLError(err)
	}
	if nc.Max, err = s.limits.Limit(ctx, kind, hh); err != nil {
		return store.NewChannel{}, err
	}
	return nc, nil
}

func (s *Server) channelUpdate(ctx context.Context, r *http.Request, channelID string) (store.ChannelUpdate, error) {
	var in struct {
		URL   *string `json:"url"`
		Label *string `json:"label"`
	}
	if err := decode(r, &in); err != nil {
		return store.ChannelUpdate{}, err
	}
	var u store.ChannelUpdate
	if in.URL != nil {
		kinds, err := s.EnabledKinds(ctx)
		if err != nil {
			return store.ChannelUpdate{}, err
		}
		if u, err = notify.Reseal(s.keyring, channelID, *in.URL, kinds); err != nil {
			return store.ChannelUpdate{}, channelURLError(err)
		}
	}
	u.Label = in.Label
	return u, nil
}

func (s *Server) listUserChannels(w http.ResponseWriter, r *http.Request, c *call) error {
	cs, err := s.st.ListUserChannels(r.Context(), c.actor())
	if err != nil {
		return err
	}
	return ok(w, viewChannels(cs))
}

func (s *Server) createUserChannel(w http.ResponseWriter, r *http.Request, c *call) error {
	nc, err := s.newChannel(r.Context(), r, limits.ChannelsPerUser, "")
	if err != nil {
		return err
	}
	ch, err := s.st.CreateUserChannel(r.Context(), c.actor(), nc)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, viewChannel(ch))
	return nil
}

func (s *Server) patchUserChannel(w http.ResponseWriter, r *http.Request, c *call) error {
	id := r.PathValue("id")
	u, err := s.channelUpdate(r.Context(), r, id)
	if err != nil {
		return err
	}
	ch, err := s.st.UpdateUserChannel(r.Context(), c.actor(), id, u)
	if err != nil {
		return err
	}
	return ok(w, viewChannel(ch))
}

func (s *Server) deleteUserChannel(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.st.DeleteUserChannel(r.Context(), c.actor(), r.PathValue("id")); err != nil {
		return err
	}
	return noContent(w)
}

func (s *Server) listHouseholdChannels(w http.ResponseWriter, r *http.Request, c *call) error {
	cs, err := s.st.ListHouseholdChannels(r.Context(), c.actor(), c.hh)
	if err != nil {
		return err
	}
	return ok(w, viewChannels(cs))
}

func (s *Server) createHouseholdChannel(w http.ResponseWriter, r *http.Request, c *call) error {
	nc, err := s.newChannel(r.Context(), r, limits.ChannelsPerHousehold, c.hh)
	if err != nil {
		return err
	}
	ch, err := s.st.CreateHouseholdChannel(r.Context(), c.actor(), c.hh, nc)
	if err != nil {
		return err
	}
	s.channelCreated(r.Context(), c, ch.ID)
	writeJSON(w, http.StatusCreated, viewChannel(ch))
	return nil
}

func (s *Server) patchHouseholdChannel(w http.ResponseWriter, r *http.Request, c *call) error {
	id := r.PathValue("id")
	u, err := s.channelUpdate(r.Context(), r, id)
	if err != nil {
		return err
	}
	ch, err := s.st.UpdateHouseholdChannel(r.Context(), c.actor(), c.hh, id, u)
	if err != nil {
		return err
	}
	return ok(w, viewChannel(ch))
}

func (s *Server) deleteHouseholdChannel(w http.ResponseWriter, r *http.Request, c *call) error {
	if err := s.st.DeleteHouseholdChannel(r.Context(), c.actor(), c.hh, r.PathValue("id")); err != nil {
		return err
	}
	return noContent(w)
}

// testSendTimeout bounds a test send; the egress client has its own, shorter
// per-request timeout.
const testSendTimeout = 30 * time.Second

// testChannel sends a test message through the real sender (egress guard and
// kind allowlist included) and records the outcome in the delivery log. The
// error text in both the log and the response is redacted (notify.SafeError).
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request, c *call, hh string) error {
	if !s.allow(w, rateTest+":user:"+c.user.ID) {
		return nil
	}
	ctx, id := r.Context(), r.PathValue("id")
	var ct string
	var err error
	if hh == "" {
		ct, err = s.st.UserChannelCiphertext(ctx, c.actor(), id)
	} else {
		ct, err = s.st.HouseholdChannelCiphertext(ctx, c.actor(), hh, id)
	}
	if err != nil {
		return err
	}
	rawURL, err := notify.Open(s.keyring, id, ct)
	if err != nil {
		return err
	}
	var sendErr error
	if kinds, kerr := s.EnabledKinds(ctx); kerr != nil {
		return kerr
	} else if _, sendErr = notify.ValidateURL(rawURL, kinds); sendErr == nil {
		sctx, cancel := context.WithTimeout(ctx, testSendTimeout)
		defer cancel()
		sendErr = s.sender.Send(sctx, rawURL, notify.Message{
			Title: i18n.T(c.user.Locale, "notify.test_title", nil),
			Body:  i18n.T(c.user.Locale, "notify.test_body", nil),
		})
	}
	safe := notify.SafeError(sendErr, rawURL)
	if err := s.st.RecordTestDelivery(ctx, c.actor(), hh, id, sendErr == nil, safe); err != nil {
		return err
	}
	return ok(w, map[string]any{"sent": sendErr == nil, "error": safe})
}

func (s *Server) testUserChannel(w http.ResponseWriter, r *http.Request, c *call) error {
	return s.testChannel(w, r, c, "")
}

func (s *Server) testHouseholdChannel(w http.ResponseWriter, r *http.Request, c *call) error {
	return s.testChannel(w, r, c, c.hh)
}

// --- Delivery log ---

type deliveryView struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"` // before, on_due, overdue or test
	ChoreID       string    `json:"choreId,omitempty"`
	ChoreName     string    `json:"choreName,omitempty"`
	ChannelID     string    `json:"channelId,omitempty"`
	ChannelLabel  string    `json:"channelLabel,omitempty"`
	Kind          string    `json:"kind,omitempty"` // channel service kind
	Status        string    `json:"status"`
	Attempts      int       `json:"attempts"`
	Error         string    `json:"error,omitempty"` // redacted when it was stored (notify.SafeError)
	CreatedAt     time.Time `json:"createdAt"`
	SentAt        time.Time `json:"sentAt,omitzero"`
	NextAttemptAt time.Time `json:"nextAttemptAt,omitzero"`
}

type deliveryList struct {
	Items []deliveryView `json:"items"`
	Next  string         `json:"next,omitempty"` // pass as ?before= for the next page
}

func deliveryType(k schedule.ReminderKind) string {
	switch k {
	case schedule.Before:
		return "before"
	case schedule.OnDue:
		return "on_due"
	case schedule.Overdue:
		return "overdue"
	}
	return "test"
}

const defaultDeliveryPage, maxDeliveryPage = 50, 200

// deliveryPage parses ?limit= and ?before= (an opaque cursor from "next").
func deliveryPage(r *http.Request) (store.DeliveryPage, error) {
	p := store.DeliveryPage{Limit: defaultDeliveryPage}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxDeliveryPage {
			return p, errInvalid
		}
		p.Limit = n
	}
	if v := r.URL.Query().Get("before"); v != "" {
		msStr, id, found := strings.Cut(v, ".")
		n, err := strconv.ParseInt(msStr, 10, 64)
		if !found || err != nil || id == "" || len(id) > 64 {
			return p, errInvalid
		}
		p.BeforeAt, p.BeforeID = time.UnixMilli(n).UTC(), id
	}
	return p, nil
}

func viewDeliveries(ds []store.Delivery, limit int) deliveryList {
	out := deliveryList{Items: []deliveryView{}}
	if len(ds) > limit {
		ds = ds[:limit]
		last := ds[limit-1]
		out.Next = strconv.FormatInt(last.CreatedAt.UnixMilli(), 10) + "." + last.ID
	}
	for _, d := range ds {
		out.Items = append(out.Items, deliveryView{ID: d.ID, Type: deliveryType(d.Kind), ChoreID: d.ChoreID, ChoreName: d.ChoreName,
			ChannelID: d.ChannelID, ChannelLabel: d.ChannelLabel, Kind: d.Service, Status: d.Status, Attempts: d.Attempts,
			Error: d.LastError, CreatedAt: d.CreatedAt, SentAt: d.SentAt, NextAttemptAt: d.NextAttemptAt})
	}
	return out
}

func (s *Server) listUserDeliveries(w http.ResponseWriter, r *http.Request, c *call) error {
	p, err := deliveryPage(r)
	if err != nil {
		return err
	}
	limit := p.Limit
	p.Limit++ // one extra row tells whether another page exists
	ds, err := s.st.ListUserDeliveriesPage(r.Context(), c.actor(), p)
	if err != nil {
		return err
	}
	return ok(w, viewDeliveries(ds, limit))
}

func (s *Server) listHouseholdDeliveries(w http.ResponseWriter, r *http.Request, c *call) error {
	p, err := deliveryPage(r)
	if err != nil {
		return err
	}
	limit := p.Limit
	p.Limit++
	ds, err := s.st.ListHouseholdDeliveriesPage(r.Context(), c.actor(), c.hh, p)
	if err != nil {
		return err
	}
	return ok(w, viewDeliveries(ds, limit))
}

// --- Instance notification settings ---

// EnabledKinds is the admin's notification service allowlist as a set. It is
// read on every use, so an admin change applies at once to channel
// validation, test sends and the scheduler. Never set means
// notify.DefaultKinds.
func (s *Server) EnabledKinds(ctx context.Context) (map[string]bool, error) {
	st, err := s.st.InstanceSettings(ctx)
	if err != nil {
		return nil, err
	}
	return kindSet(st), nil
}

// listNotificationKinds tells any signed-in user which services they may add
// channels for, so the channel form can offer them. Admin settings (which also
// list the kinds that could be enabled) stay admin-only.
func (s *Server) listNotificationKinds(w http.ResponseWriter, r *http.Request, _ *call) error {
	kinds, err := s.EnabledKinds(r.Context())
	if err != nil {
		return err
	}
	return ok(w, map[string][]string{"enabledNotificationKinds": append([]string{}, slices.Sorted(maps.Keys(kinds))...)})
}

func kindSet(st store.InstanceSettings) map[string]bool {
	if st.NotificationKinds == nil {
		return notify.DefaultKinds()
	}
	m := make(map[string]bool, len(st.NotificationKinds))
	for _, k := range st.NotificationKinds {
		m[k] = true
	}
	return m
}

// EgressGuardEnabled reports whether outbound notification traffic is
// guarded against non-public addresses: always in hosted mode, otherwise per
// the admin setting (default on). It fails closed: on a read error it is on.
func (s *Server) EgressGuardEnabled(ctx context.Context) bool {
	if s.hosted {
		return true
	}
	st, err := s.st.InstanceSettings(ctx)
	return err != nil || st.EgressGuard
}

// Sender is the notification sender, shared with the scheduler so test sends
// and reminders obey the same egress setting.
func (s *Server) Sender() notify.Sender { return s.sender }
