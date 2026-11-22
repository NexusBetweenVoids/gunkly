package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// API token scopes (ADR 0014). There is no implication between them.
const (
	ScopeRead     = "chores:read"
	ScopeComplete = "chores:complete"
	ScopeWrite    = "chores:write"
)

// ValidScope reports whether s is a defined scope.
func ValidScope(s string) bool { return s == ScopeRead || s == ScopeComplete || s == ScopeWrite }

// TokenHousehold is a household a token is bound to, with the token owner's
// current role in it. Only households the owner still belongs to are listed.
type TokenHousehold struct {
	ID   string
	Name string
	Role Role
}

// APIToken is a token's metadata; the secret is never stored.
type APIToken struct {
	ID         string
	UserID     string
	Name       string
	Scopes     []string
	Households []TokenHousehold
	CreatedAt  time.Time
	ExpiresAt  time.Time // zero = never
	LastUsedAt time.Time // zero = never
	OAuth      bool      // an OAuth access token: ID is the grant id
}

// HasScope reports whether the token carries scope.
func (t APIToken) HasScope(scope string) bool {
	for _, s := range t.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// Household returns the bound household with the given ID, if the owner is
// still a member of it.
func (t APIToken) Household(id string) (TokenHousehold, bool) {
	for _, h := range t.Households {
		if h.ID == id {
			return h, true
		}
	}
	return TokenHousehold{}, false
}

// NewAPIToken is the input to CreateAPIToken. TokenHash is the SHA-256 of the
// secret; Max is limits.APITokensPerUser, resolved by the caller.
type NewAPIToken struct {
	Name         string
	TokenHash    string
	Scopes       []string
	HouseholdIDs []string
	ExpiresAt    time.Time // zero = never
	Max          int
}

// TokenLastUsedEvery is how often a token's last-used time is written.
const TokenLastUsedEvery = time.Minute

// CreateAPIToken stores a token for the actor, bound to households the actor
// belongs to (any role; the role is applied at use). ErrNotFound if the actor
// is not a member of one of them, ErrLimit when the actor already has Max
// active tokens.
func (s *Store) CreateAPIToken(ctx context.Context, a Actor, in NewAPIToken) (APIToken, error) {
	if in.Max <= 0 || in.TokenHash == "" || len(in.Scopes) == 0 || len(in.HouseholdIDs) == 0 {
		return APIToken{}, ErrInvalid
	}
	tok := APIToken{ID: ids.New(), UserID: a.UserID, Name: in.Name, Scopes: in.Scopes, ExpiresAt: in.ExpiresAt}
	err := s.inTx(ctx, true, func(t *tx) error {
		now := s.nowMS()
		var n int
		if err := t.queryRow(ctx, `SELECT count(*) FROM api_tokens WHERE user_id = ? AND revoked_at IS NULL
			AND (expires_at IS NULL OR expires_at > ?)`, a.UserID, now).Scan(&n); err != nil {
			return err
		}
		if n >= in.Max {
			return ErrLimit
		}
		tok.CreatedAt = time.UnixMilli(now).UTC()
		if _, err := t.exec(ctx, `INSERT INTO api_tokens (id, token_hash, user_id, name, scopes, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, tok.ID, in.TokenHash, a.UserID, in.Name, strings.Join(in.Scopes, " "), now, ms(in.ExpiresAt)); err != nil {
			return err
		}
		for _, hid := range in.HouseholdIDs {
			if _, err := t.authorize(ctx, a, hid, RoleGuest, true); err != nil {
				return err
			}
			if _, err := t.exec(ctx, "INSERT INTO api_token_households (token_id, household_id) VALUES (?, ?)", tok.ID, hid); err != nil {
				return conflict(err)
			}
		}
		var err error
		tok.Households, err = t.tokenHouseholds(ctx, tok.ID, a.UserID)
		return err
	})
	if err != nil {
		return APIToken{}, err
	}
	return tok, nil
}

// tokenHouseholds lists the households bound to the token that the owner
// still belongs to.
func (t *tx) tokenHouseholds(ctx context.Context, tokenID, userID string) ([]TokenHousehold, error) {
	rows, err := t.query(ctx, `SELECT h.id, h.name, m.role FROM api_token_households th
		JOIN households h ON h.id = th.household_id
		JOIN memberships m ON m.household_id = th.household_id AND m.user_id = ?
		WHERE th.token_id = ? ORDER BY h.name, h.id`, userID, tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TokenHousehold
	for rows.Next() {
		var h TokenHousehold
		if err := rows.Scan(&h.ID, &h.Name, &h.Role); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

//nolint:gosec // G101: column names, not a credential
const tokenCols = "id, user_id, name, scopes, created_at, expires_at, last_used_at"

func scanTokenRow(row interface{ Scan(...any) error }) (APIToken, error) {
	var k APIToken
	var scopes string
	var created int64
	var exp, used sql.NullInt64
	if err := row.Scan(&k.ID, &k.UserID, &k.Name, &scopes, &created, &exp, &used); err != nil {
		return APIToken{}, notFound(err)
	}
	k.Scopes = strings.Fields(scopes)
	k.CreatedAt, k.ExpiresAt, k.LastUsedAt = time.UnixMilli(created).UTC(), fromMS(exp), fromMS(used)
	return k, nil
}

// ListAPITokens lists the user's active (not revoked, not expired) tokens,
// newest first.
func (s *Store) ListAPITokens(ctx context.Context, userID string) ([]APIToken, error) {
	var out []APIToken
	err := s.inTx(ctx, false, func(t *tx) error {
		rows, err := t.query(ctx, "SELECT "+tokenCols+` FROM api_tokens WHERE user_id = ? AND revoked_at IS NULL
			AND (expires_at IS NULL OR expires_at > ?) ORDER BY created_at DESC, id`, userID, s.nowMS())
		if err != nil {
			return err
		}
		var tokens []APIToken
		defer rows.Close()
		for rows.Next() {
			k, err := scanTokenRow(rows)
			if err != nil {
				return err
			}
			tokens = append(tokens, k)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		_ = rows.Close()
		for _, k := range tokens {
			k.Households, err = t.tokenHouseholds(ctx, k.ID, userID)
			if err != nil {
				return err
			}
			out = append(out, k)
		}
		return nil
	})
	return out, err
}

// RevokeAPIToken revokes one of the user's tokens at once. ErrNotFound if it
// is not theirs or already revoked.
func (s *Store) RevokeAPIToken(ctx context.Context, userID, tokenID string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		return t.execOne(ctx, "UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL",
			s.nowMS(), tokenID, userID)
	})
}

// APITokenByHash resolves a token secret's hash to the token and its owner.
// Revoked and expired tokens are ErrNotFound.
func (s *Store) APITokenByHash(ctx context.Context, hash string) (APIToken, User, error) {
	var k APIToken
	var u User
	err := s.inTx(ctx, false, func(t *tx) error {
		var err error
		k, err = scanTokenRow(t.queryRow(ctx, "SELECT "+tokenCols+` FROM api_tokens WHERE token_hash = ? AND revoked_at IS NULL
			AND (expires_at IS NULL OR expires_at > ?)`, hash, s.nowMS()))
		if err != nil {
			return err
		}
		if k.Households, err = t.tokenHouseholds(ctx, k.ID, k.UserID); err != nil {
			return err
		}
		u, err = scanUser(t.queryRow(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", k.UserID))
		return err
	})
	return k, u, err
}

// TouchAPIToken records use of a token (and activity of its owner) unless it
// was already recorded within TokenLastUsedEvery.
func (s *Store) TouchAPIToken(ctx context.Context, tokenID, userID string) error {
	now := s.nowMS()
	return s.inTx(ctx, true, func(t *tx) error {
		res, err := t.exec(ctx, "UPDATE api_tokens SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at <= ?)",
			now, tokenID, now-TokenLastUsedEvery.Milliseconds())
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		return t.markActive(ctx, userID, now)
	})
}

// revokeUserTokens revokes every active API token of the user (a password
// change or reset, or a second factor being removed: whoever caused it may
// have held the old credentials, so tokens minted with them must not outlive
// them). When any were revoked it audits token_revoke with via, the count and
// the token ids (never secrets).
func (t *tx) revokeUserTokens(ctx context.Context, userID, via string, now int64) error {
	if err := t.revokeUserOAuth(ctx, userID, via, now); err != nil {
		return err
	}
	idList, err := t.activeTokenIDs(ctx, userID)
	if err != nil || len(idList) == 0 {
		return err
	}
	if _, err := t.exec(ctx, "UPDATE api_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL", now, userID); err != nil {
		return err
	}
	return t.insertAudit(ctx, AuditEvent{ActorUserID: userID, Event: AuditTokenRevoke,
		Meta: map[string]string{"via": via, "count": strconv.Itoa(len(idList)), "tokens": strings.Join(idList, ",")}}, now)
}

func (t *tx) activeTokenIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := t.query(ctx, "SELECT id FROM api_tokens WHERE user_id = ? AND revoked_at IS NULL ORDER BY id", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
