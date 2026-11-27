package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// Audit event names. Stable strings: the UI translates them.
const (
	AuditSetup             = "setup"
	AuditLoginSuccess      = "login_success"
	AuditLoginFailure      = "login_failure"
	AuditLogout            = "logout"
	AuditPasswordChange    = "password_change"
	AuditPasswordReset     = "password_reset"
	AuditTOTPEnable        = "totp_enable"
	AuditTOTPDisable       = "totp_disable"
	AuditRecoveryCodeUse   = "recovery_code_use"
	AuditRecoveryCodesNew  = "recovery_codes_new" // the user replaced their recovery codes
	AuditSessionRevoke     = "session_revoke"
	AuditMemberRoleChange  = "member_role_change"
	AuditMemberRemove      = "member_remove"
	AuditInviteCreate      = "invite_create"
	AuditInviteAccept      = "invite_accept"
	AuditInviteRevoke      = "invite_revoke"
	AuditInviteLimit       = "invite_limit_reached" // a household hit the daily invite limit (security alert)
	AuditHouseholdCreate   = "household_create"
	AuditAdminUserCreate   = "admin_user_create"
	AuditAdminSettings     = "admin_settings"
	AuditAdminUpdate       = "admin_update_requested" // the update webhook was called
	AuditAISuggest         = "ai_suggest"             // a member asked the AI for chore suggestions (count only, never the text)
	AuditRegisterByInvite  = "register_invite"
	AuditSignup            = "signup"
	AuditEmailVerify       = "email_verify"
	AuditEmailChange       = "email_change"
	AuditEmailOTPEnable    = "email_otp_enable"
	AuditEmailOTPDisable   = "email_otp_disable"
	AuditHouseholdSettings = "household_settings"
	AuditChoreDelete       = "chore_delete"
	AuditChoreSkip         = "chore_skip"
	AuditCompletionRevert  = "completion_revert"
	AuditTokenCreate       = "token_create"
	AuditTokenRevoke       = "token_revoke"
	AuditTokenComplete     = "token_complete"    // a chore completed with an API token; meta token = its ID
	AuditOAuthGrant        = "oauth_grant"       // an OAuth client was given tokens; meta grant, client
	AuditOAuthTokenReuse   = "oauth_token_reuse" //nolint:gosec // G101: an event name, not a credential; a used code or rotated refresh token came back: the grant was revoked
	AuditOAuthRevoke       = "oauth_revoke"      // grants revoked (disconnect, revocation endpoint, password change...)
	AuditPasskeyAdd        = "passkey_add"       // meta passkey
	AuditPasskeyRemove     = "passkey_remove"    // meta passkey
	AuditSSOLink           = "sso_link"          // single sign-on identity linked to the account
	AuditSSOUnlink         = "sso_unlink"
	AuditSSOSignup         = "sso_signup"           // account created by a first single sign-on
	AuditAccountDelete     = "account_delete"       // actor-less: the account is gone
	AuditOwnerTransfer     = "owner_transfer"       // household_id, meta owner = new owner
	AuditWebhookCreate     = "webhook_create"       // household_id, meta webhook
	AuditWebhookUpdate     = "webhook_update"       // household_id, meta webhook
	AuditWebhookDelete     = "webhook_delete"       // household_id, meta webhook
	AuditWebhookRotate     = "webhook_rotate"       // the signing secret was replaced; household_id, meta webhook
	AuditWebhookAutoOff    = "webhook_auto_disable" // system: too many consecutive failures; household_id, meta webhook
	AuditInactiveWarn      = "inactive_warn"        // system, meta user = account
)

// AuditEvent is one audit-log entry. Meta must never contain secrets.
type AuditEvent struct {
	ID          string
	ActorUserID string // empty = system / unknown
	HouseholdID string
	Event       string
	IP          string
	Meta        map[string]string
	CreatedAt   time.Time
}

// Audit appends an entry to the audit log.
func (s *Store) Audit(ctx context.Context, e AuditEvent) error {
	return s.inTx(ctx, true, func(t *tx) error { return t.insertAudit(ctx, e, s.nowMS()) })
}

func (t *tx) insertAudit(ctx context.Context, e AuditEvent, now int64) error {
	meta := "{}"
	if len(e.Meta) > 0 {
		b, err := json.Marshal(e.Meta)
		if err != nil {
			return err
		}
		meta = string(b)
	}
	_, err := t.exec(ctx, `INSERT INTO audit_log (id, actor_user_id, household_id, event, ip, meta, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, ids.New(), nullStr(e.ActorUserID), nullStr(e.HouseholdID), e.Event, e.IP, meta, now)
	return err
}

// ListAuditForUser returns the newest limit events performed by the user
// (their own account history).
func (s *Store) ListAuditForUser(ctx context.Context, userID string, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	var out []AuditEvent
	err := s.inTx(ctx, false, func(t *tx) error {
		rows, err := t.query(ctx, `SELECT id, COALESCE(actor_user_id, ''), COALESCE(household_id, ''), event, ip, meta, created_at
			FROM audit_log WHERE actor_user_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, userID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e AuditEvent
			var meta string
			var created int64
			if err := rows.Scan(&e.ID, &e.ActorUserID, &e.HouseholdID, &e.Event, &e.IP, &meta, &created); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(meta), &e.Meta); err != nil {
				e.Meta = nil
			}
			e.CreatedAt = time.UnixMilli(created).UTC()
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// AlertEvents are the audit events that are security alerts: listed on the
// Administration page and, with security alerts on, emailed to the admins.
// Add a new alert's event here.
var AlertEvents = []string{AuditInviteLimit, AuditEmailFailing}

// AlertCountSince counts alert events of one kind and scope since a moment.
// The scope (stored as meta "scope") is what an alert is deduplicated by: a
// household, an account, an address, or "" for instance-wide.
func (s *Store) AlertCountSince(ctx context.Context, event, scope string, since time.Time) (int, error) {
	frag, _ := json.Marshal(map[string]string{"scope": scope}) // {"scope":"..."}
	var n int
	err := s.db.QueryRowContext(ctx, s.d.Rebind("SELECT count(*) FROM audit_log WHERE event = ? AND created_at > ? AND meta LIKE ?"),
		event, since.UnixMilli(), "%"+strings.Trim(string(frag), "{}")+"%").Scan(&n)
	return n, err
}

// SecurityEvent is a recent alert event for the Administration page.
type SecurityEvent struct {
	Event       string
	HouseholdID string
	Username    string // the account that caused it; empty if none or deleted
	Meta        map[string]string
	At          time.Time
}

// RecentSecurityEvents lists the alert events (AlertEvents) since a moment, newest first (at most 50).
func (s *Store) RecentSecurityEvents(ctx context.Context, since time.Time) ([]SecurityEvent, error) {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(AlertEvents)), ",")
	args := []any{since.UnixMilli()}
	for _, e := range AlertEvents {
		args = append(args, e)
	}
	rows, err := s.db.QueryContext(ctx, s.d.Rebind(`SELECT a.event, COALESCE(a.household_id, ''), COALESCE(u.username, ''), a.meta, a.created_at
		FROM audit_log a LEFT JOIN users u ON u.id = a.actor_user_id
		WHERE a.created_at > ? AND a.event IN (`+marks+`) ORDER BY a.created_at DESC LIMIT 50`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SecurityEvent{}
	for rows.Next() {
		var e SecurityEvent
		var meta string
		var at int64
		if err := rows.Scan(&e.Event, &e.HouseholdID, &e.Username, &meta, &at); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(meta), &e.Meta)
		e.At = time.UnixMilli(at).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// AlertRecipients are the instance admins with a confirmed email address.
func (s *Store) AlertRecipients(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+userCols+" FROM users WHERE is_admin = TRUE AND email_verified = TRUE AND email IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
