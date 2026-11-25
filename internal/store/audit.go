package store

import (
	"context"
	"encoding/json"
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
	AuditSessionRevoke     = "session_revoke"
	AuditMemberRoleChange  = "member_role_change"
	AuditMemberRemove      = "member_remove"
	AuditInviteCreate      = "invite_create"
	AuditInviteAccept      = "invite_accept"
	AuditInviteRevoke      = "invite_revoke"
	AuditHouseholdCreate   = "household_create"
	AuditAdminUserCreate   = "admin_user_create"
	AuditAdminSettings     = "admin_settings"
	AuditAISuggest         = "ai_suggest" // a member asked the AI for chore suggestions (count only, never the text)
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
