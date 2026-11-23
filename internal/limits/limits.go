// Package limits is the single source of usage limits (ADR 0010). Defaults
// are the table in docs/PRD.md; admins override them instance-wide (settings
// table, "limit.<kind>") and per household through a plan.
package limits

import (
	"context"
	"errors"
)

// Kind names a limit. Stable strings: they are the settings and plan keys.
type Kind string

const (
	HouseholdsPerUser          Kind = "households_per_user"
	MembersPerHousehold        Kind = "members_per_household"
	ChoresPerHousehold         Kind = "chores_per_household"
	RemindersPerChore          Kind = "reminders_per_chore"
	ChannelsPerUser            Kind = "channels_per_user"
	ChannelsPerHousehold       Kind = "channels_per_household"
	NotificationsHouseholdHour Kind = "notifications_household_hour"
	NotificationsHouseholdDay  Kind = "notifications_household_day"
	NotificationsUserDay       Kind = "notifications_user_day"
	EmailsAccountHour          Kind = "emails_account_hour"
	EmailCodesAccountHour      Kind = "email_codes_account_hour"
	EmailsIPHour               Kind = "emails_ip_hour"
	EmailsGlobalHour           Kind = "emails_global_hour"
	ActiveInvitesPerHousehold  Kind = "active_invites_per_household"
	CalendarFeedsPerUser       Kind = "calendar_feeds_per_user"
	PushSubscriptionsPerUser   Kind = "push_subscriptions_per_user"
	SignupsPerIPHour           Kind = "signups_per_ip_hour"
	LoginAttemptsPerMinute     Kind = "login_attempts_per_minute"
	AreasPerHousehold          Kind = "areas_per_household"
	CompletionsPerChore        Kind = "completions_per_chore"
	APITokensPerUser           Kind = "api_tokens_per_user" //nolint:gosec // G101: a limit name, not a credential
	OAuthClients               Kind = "oauth_clients"       // registered OAuth clients, instance-wide
	OAuthGrantsPerUser         Kind = "oauth_grants_per_user"
	OAuthUnusedClientsPerIP    Kind = "oauth_unused_clients_per_ip" // registered, never approved clients per registering IP
	WebhooksPerHousehold       Kind = "webhooks_per_household"
)

// Defaults is the PRD limits table.
var Defaults = map[Kind]int{
	HouseholdsPerUser:          20,
	MembersPerHousehold:        50,
	ChoresPerHousehold:         2000,
	RemindersPerChore:          10,
	ChannelsPerUser:            10,
	ChannelsPerHousehold:       10,
	NotificationsHouseholdHour: 100,
	NotificationsHouseholdDay:  250,
	NotificationsUserDay:       500,
	EmailsAccountHour:          10,
	EmailCodesAccountHour:      20,
	EmailsIPHour:               5,
	EmailsGlobalHour:           500,
	ActiveInvitesPerHousehold:  20,
	CalendarFeedsPerUser:       10,
	PushSubscriptionsPerUser:   10,
	SignupsPerIPHour:           5,
	LoginAttemptsPerMinute:     5,
	AreasPerHousehold:          100,
	CompletionsPerChore:        1000,
	APITokensPerUser:           20,
	OAuthClients:               1000,
	OAuthGrantsPerUser:         20,
	OAuthUnusedClientsPerIP:    20,
	WebhooksPerHousehold:       10,
}

// Per-IP request rates of the unauthenticated link endpoints. Fixed, not
// admin-tunable (the login rate above stays strict and separate).
const (
	MarkDonePerMinute = 30
	ICalPerMinute     = 60
	// MetricsPerMinute caps /metrics requests per client IP (a scraper polls
	// every few seconds; guessing the token must not be free).
	MetricsPerMinute = 30
	// TransfersPerMinute caps data exports and imports per client IP.
	TransfersPerMinute = 6
	// ImportMaxBytes is the upload cap of the import routes (the default
	// request cap is 1 MiB).
	ImportMaxBytes = 5 << 20
	// ImportMaxCompletions caps the completions in one import file, over all
	// its chores (the per-chore cap alone allows chores x 1000).
	ImportMaxCompletions = 20000
	// Authenticated write throttles: a token bucket per user over all unsafe
	// /api/v1 requests, and one per household over its write routes.
	UserWritesPerMinute      = 120
	UserWriteBurst           = 60
	HouseholdWritesPerMinute = 300
	HouseholdWriteBurst      = 100
	// API tokens: a bucket per token, and one per client IP over all bearer
	// requests (it also bounds token guessing).
	TokenRequestsPerMinute = 120
	TokenBurst             = 60
	TokenIPPerMinute       = 300
	TokenIPBurst           = 100
	// OAuth client endpoints, per client IP: dynamic registration is the
	// strictest (it creates rows for anonymous callers).
	OAuthRegisterPerMinute  = 10
	OAuthTokenPerMinute     = 60
	OAuthAuthorizePerMinute = 60
)

// ErrExceeded is returned by Check when current has reached the limit.
var ErrExceeded = errors.New("limits: limit reached")

// Source supplies overrides: instance-wide and the household's plan (nil
// when householdID is empty or it has no plan). *store.Store implements it.
type Source interface {
	LimitOverrides(ctx context.Context, householdID string) (instance, plan map[string]int, err error)
}

// Service resolves effective limits. Safe for concurrent use.
type Service struct{ src Source }

// New returns a Service reading overrides from src.
func New(src Source) *Service { return &Service{src: src} }

// Known reports whether k is a defined limit kind.
func Known(k Kind) bool { _, ok := Defaults[k]; return ok }

// Limit returns the effective value of k: the household plan override, else
// the instance override, else the default. householdID may be empty for
// per-user limits.
func (s *Service) Limit(ctx context.Context, k Kind, householdID string) (int, error) {
	inst, plan, err := s.src.LimitOverrides(ctx, householdID)
	if err != nil {
		return 0, err
	}
	if v, ok := plan[string(k)]; ok && v > 0 {
		return v, nil
	}
	if v, ok := inst[string(k)]; ok && v > 0 {
		return v, nil
	}
	return Defaults[k], nil
}

// Check returns ErrExceeded if adding one more to current would exceed k.
func (s *Service) Check(ctx context.Context, k Kind, householdID string, current int) error {
	max, err := s.Limit(ctx, k, householdID)
	if err != nil {
		return err
	}
	if current >= max {
		return ErrExceeded
	}
	return nil
}
