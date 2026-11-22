package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// OAuth 2.1 for MCP clients (ADR 0016). Clients register dynamically and are
// public; a grant is one user's approval of one client for some households
// and scopes; authorization codes and access/refresh tokens hang off the
// grant and are stored only as SHA-256 hashes. Revoking the grant ends them
// all.

// Lifetimes of the OAuth artefacts.
const (
	OAuthCodeTTL       = 60 * time.Second
	OAuthAccessTTL     = time.Hour
	OAuthRefreshIdle   = 30 * 24 * time.Hour // a refresh token unused this long dies
	OAuthGrantLifetime = 90 * 24 * time.Hour // absolute: re-consent after this
	// OAuthUnusedClientAge is how long a registered client that never started
	// an authorization is kept; OAuthIdleClientAge how long one without any
	// grant is kept after its last use.
	OAuthUnusedClientAge = 24 * time.Hour
	OAuthIdleClientAge   = 90 * 24 * time.Hour
)

// OAuthClient is a dynamically registered public client.
type OAuthClient struct {
	ID           string
	Name         string
	RedirectURIs []string
	CreatedAt    time.Time
}

// CreateOAuthClient stores a client. ErrLimit when max clients exist already.
func (s *Store) CreateOAuthClient(ctx context.Context, name string, redirectURIs []string, max int) (OAuthClient, error) {
	if max <= 0 || len(redirectURIs) == 0 {
		return OAuthClient{}, ErrInvalid
	}
	raw, err := json.Marshal(redirectURIs)
	if err != nil {
		return OAuthClient{}, err
	}
	c := OAuthClient{ID: ids.New(), Name: name, RedirectURIs: redirectURIs}
	err = s.inTx(ctx, true, func(t *tx) error {
		var n int
		if err := t.queryRow(ctx, "SELECT count(*) FROM oauth_clients").Scan(&n); err != nil {
			return err
		}
		if n >= max {
			return ErrLimit
		}
		now := s.nowMS()
		c.CreatedAt = time.UnixMilli(now).UTC()
		_, err := t.exec(ctx, "INSERT INTO oauth_clients (id, name, redirect_uris, created_at) VALUES (?, ?, ?, ?)",
			c.ID, name, string(raw), now)
		return err
	})
	return c, err
}

// OAuthClientByID returns a registered client; ErrNotFound if unknown.
func (s *Store) OAuthClientByID(ctx context.Context, id string) (OAuthClient, error) {
	var c OAuthClient
	var raw string
	var created int64
	err := s.db.QueryRowContext(ctx, s.d.Rebind("SELECT id, name, redirect_uris, created_at FROM oauth_clients WHERE id = ?"), id).
		Scan(&c.ID, &c.Name, &raw, &created)
	if err != nil {
		return OAuthClient{}, notFound(err)
	}
	if err := json.Unmarshal([]byte(raw), &c.RedirectURIs); err != nil {
		return OAuthClient{}, err
	}
	c.CreatedAt = time.UnixMilli(created).UTC()
	return c, nil
}

// TouchOAuthClient records that the client started an authorization (keeps it
// out of the unused-client purge).
func (s *Store) TouchOAuthClient(ctx context.Context, id string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		_, err := t.exec(ctx, "UPDATE oauth_clients SET last_used_at = ? WHERE id = ?", s.nowMS(), id)
		return err
	})
}

// NewOAuthCode is the input to CreateOAuthCode. CodeHash is the SHA-256 of the
// authorization code; Max is limits.OAuthGrantsPerUser, resolved by the caller.
type NewOAuthCode struct {
	ClientID     string
	RedirectURI  string
	Challenge    string
	CodeHash     string
	Scopes       []string
	HouseholdIDs []string
	Max          int
}

// CreateOAuthCode records the user's consent: a (not yet active) grant bound to
// households the actor belongs to, and a single-use code for it. ErrNotFound
// if the actor is not a member of one of the households, ErrLimit at Max
// grants.
func (s *Store) CreateOAuthCode(ctx context.Context, a Actor, in NewOAuthCode) (grantID string, err error) {
	if in.Max <= 0 || in.CodeHash == "" || in.Challenge == "" || len(in.Scopes) == 0 || len(in.HouseholdIDs) == 0 {
		return "", ErrInvalid
	}
	grantID = ids.New()
	err = s.inTx(ctx, true, func(t *tx) error {
		now := s.nowMS()
		var n int
		if err := t.queryRow(ctx, `SELECT count(*) FROM oauth_grants WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ?`,
			a.UserID, now).Scan(&n); err != nil {
			return err
		}
		if n >= in.Max {
			return ErrLimit
		}
		if _, err := t.exec(ctx, `INSERT INTO oauth_grants (id, user_id, client_id, scopes, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?)`, grantID, a.UserID, in.ClientID, strings.Join(in.Scopes, " "), now,
			now+OAuthGrantLifetime.Milliseconds()); err != nil {
			return notFoundFK(err)
		}
		for _, hid := range in.HouseholdIDs {
			if _, err := t.authorize(ctx, a, hid, RoleGuest, true); err != nil {
				return err
			}
			if _, err := t.exec(ctx, "INSERT INTO oauth_grant_households (grant_id, household_id) VALUES (?, ?)", grantID, hid); err != nil {
				return conflict(err)
			}
		}
		_, err := t.exec(ctx, "INSERT INTO oauth_codes (code_hash, grant_id, redirect_uri, challenge, expires_at) VALUES (?, ?, ?, ?, ?)",
			in.CodeHash, grantID, in.RedirectURI, in.Challenge, now+OAuthCodeTTL.Milliseconds())
		return err
	})
	return grantID, err
}

// OAuthCode is a redeemed authorization code.
type OAuthCode struct {
	GrantID, UserID, ClientID, RedirectURI, Challenge string
}

// revokeGrantForReuse revokes a grant and audits that a single-use credential
// came back.
func (t *tx) revokeGrantForReuse(ctx context.Context, grantID, userID, what string, now int64) error {
	if _, err := t.exec(ctx, "UPDATE oauth_grants SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL", now, grantID); err != nil {
		return err
	}
	return t.insertAudit(ctx, AuditEvent{ActorUserID: userID, Event: AuditOAuthTokenReuse,
		Meta: map[string]string{"grant": grantID, "credential": what}}, now)
}

// ConsumeOAuthCode marks the code used, atomically. A second presentation of a
// used code revokes the whole grant (reused is true and err nil, so the
// revocation commits). Unknown, expired and revoked-grant codes are
// ErrNotFound. The caller checks client, redirect URI and PKCE on the result:
// the code is spent whether or not they match.
func (s *Store) ConsumeOAuthCode(ctx context.Context, codeHash string) (code OAuthCode, reused bool, err error) {
	err = s.inTx(ctx, true, func(t *tx) error {
		now := s.nowMS()
		var exp, gexp int64
		var used, revoked sql.NullInt64
		if err := t.queryRow(ctx, `SELECT c.grant_id, c.redirect_uri, c.challenge, c.expires_at, c.used_at,
			g.user_id, g.client_id, g.expires_at, g.revoked_at
			FROM oauth_codes c JOIN oauth_grants g ON g.id = c.grant_id WHERE c.code_hash = ?`, codeHash).
			Scan(&code.GrantID, &code.RedirectURI, &code.Challenge, &exp, &used, &code.UserID, &code.ClientID, &gexp, &revoked); err != nil {
			return notFound(err)
		}
		if revoked.Valid || gexp <= now || !used.Valid && exp <= now {
			return ErrNotFound
		}
		if !used.Valid {
			res, err := t.exec(ctx, "UPDATE oauth_codes SET used_at = ? WHERE code_hash = ? AND used_at IS NULL", now, codeHash)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil || n == 1 {
				return err
			}
		}
		reused = true
		return t.revokeGrantForReuse(ctx, code.GrantID, code.UserID, "code", now)
	})
	if reused || err != nil {
		code = OAuthCode{}
	}
	return code, reused, err
}

// OAuthIssued describes freshly issued tokens.
type OAuthIssued struct {
	GrantID, UserID string
	Scopes          []string
	AccessExpires   time.Time
}

func (t *tx) insertOAuthTokens(ctx context.Context, grantID, accessHash, refreshHash string, grantExp, now int64) (time.Time, error) {
	access := min(now+OAuthAccessTTL.Milliseconds(), grantExp)
	refresh := min(now+OAuthRefreshIdle.Milliseconds(), grantExp)
	for _, r := range []struct {
		hash, kind string
		exp        int64
	}{{accessHash, "access", access}, {refreshHash, "refresh", refresh}} {
		if _, err := t.exec(ctx, "INSERT INTO oauth_tokens (token_hash, kind, grant_id, created_at, expires_at) VALUES (?, ?, ?, ?, ?)",
			r.hash, r.kind, grantID, now, r.exp); err != nil {
			return time.Time{}, err
		}
	}
	return time.UnixMilli(access).UTC(), nil
}

// IssueOAuthTokens activates the grant (first redemption) and stores a new
// access and refresh token for it. ErrNotFound if the grant was revoked or
// expired meanwhile.
func (s *Store) IssueOAuthTokens(ctx context.Context, grantID, accessHash, refreshHash string) (OAuthIssued, error) {
	out := OAuthIssued{GrantID: grantID}
	err := s.inTx(ctx, true, func(t *tx) error {
		now := s.nowMS()
		var gexp int64
		var clientID, scopes string
		if err := t.queryRow(ctx, "SELECT user_id, client_id, scopes, expires_at FROM oauth_grants WHERE id = ? AND revoked_at IS NULL AND expires_at > ?",
			grantID, now).Scan(&out.UserID, &clientID, &scopes, &gexp); err != nil {
			return notFound(err)
		}
		out.Scopes = strings.Fields(scopes)
		if _, err := t.exec(ctx, "UPDATE oauth_grants SET activated_at = COALESCE(activated_at, ?), last_used_at = ? WHERE id = ?", now, now, grantID); err != nil {
			return err
		}
		var err error
		out.AccessExpires, err = t.insertOAuthTokens(ctx, grantID, accessHash, refreshHash, gexp, now)
		if err != nil {
			return err
		}
		if err := t.insertAudit(ctx, AuditEvent{ActorUserID: out.UserID, Event: AuditOAuthGrant,
			Meta: map[string]string{"grant": grantID, "client": clientID}}, now); err != nil {
			return err
		}
		_, err = t.exec(ctx, "UPDATE oauth_clients SET last_used_at = ? WHERE id = ?", now, clientID)
		return err
	})
	return out, err
}

// RotateOAuthRefresh trades a refresh token of clientID for a new pair. Each
// refresh token works once: presenting a rotated one again revokes the grant
// (reused is true and err nil). Unknown, expired, wrong-client and
// revoked-grant tokens are ErrNotFound.
func (s *Store) RotateOAuthRefresh(ctx context.Context, refreshHash, clientID, newAccessHash, newRefreshHash string) (out OAuthIssued, reused bool, err error) {
	err = s.inTx(ctx, true, func(t *tx) error {
		now := s.nowMS()
		var kind, gClient, scopes string
		var exp, gexp int64
		var used, revoked sql.NullInt64
		if err := t.queryRow(ctx, `SELECT t.kind, t.grant_id, t.expires_at, t.used_at, g.user_id, g.client_id, g.scopes, g.expires_at, g.revoked_at
			FROM oauth_tokens t JOIN oauth_grants g ON g.id = t.grant_id WHERE t.token_hash = ?`, refreshHash).
			Scan(&kind, &out.GrantID, &exp, &used, &out.UserID, &gClient, &scopes, &gexp, &revoked); err != nil {
			return notFound(err)
		}
		out.Scopes = strings.Fields(scopes)
		if kind != "refresh" || gClient != clientID || revoked.Valid || gexp <= now {
			return ErrNotFound
		}
		if !used.Valid {
			if exp <= now {
				return ErrNotFound
			}
			res, err := t.exec(ctx, "UPDATE oauth_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL", now, refreshHash)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n == 1 {
				if out.AccessExpires, err = t.insertOAuthTokens(ctx, out.GrantID, newAccessHash, newRefreshHash, gexp, now); err != nil {
					return err
				}
				_, err = t.exec(ctx, "UPDATE oauth_grants SET last_used_at = ? WHERE id = ?", now, out.GrantID)
				return err
			}
		}
		reused = true
		return t.revokeGrantForReuse(ctx, out.GrantID, out.UserID, "refresh_token", now)
	})
	if reused || err != nil {
		out = OAuthIssued{}
	}
	return out, reused, err
}

// OAuthAccessByHash resolves an access token to a token description the MCP
// endpoint can use like an API token: its ID is the grant ID, its households
// are the grant's that the owner still belongs to. Expired tokens and tokens
// of revoked or expired grants are ErrNotFound.
func (s *Store) OAuthAccessByHash(ctx context.Context, hash string) (APIToken, User, error) {
	var k APIToken
	var u User
	err := s.inTx(ctx, false, func(t *tx) error {
		now := s.nowMS()
		var scopes string
		var created, texp int64
		var used sql.NullInt64
		if err := t.queryRow(ctx, `SELECT g.id, g.user_id, c.name, g.scopes, g.created_at, g.last_used_at, t.expires_at
			FROM oauth_tokens t JOIN oauth_grants g ON g.id = t.grant_id JOIN oauth_clients c ON c.id = g.client_id
			WHERE t.token_hash = ? AND t.kind = 'access' AND t.expires_at > ? AND g.revoked_at IS NULL AND g.expires_at > ?`,
			hash, now, now).Scan(&k.ID, &k.UserID, &k.Name, &scopes, &created, &used, &texp); err != nil {
			return notFound(err)
		}
		k.Scopes = strings.Fields(scopes)
		k.CreatedAt, k.LastUsedAt, k.ExpiresAt = time.UnixMilli(created).UTC(), fromMS(used), time.UnixMilli(texp).UTC()
		var err error
		if k.Households, err = t.grantHouseholds(ctx, k.ID, k.UserID); err != nil {
			return err
		}
		u, err = scanUser(t.queryRow(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", k.UserID))
		return err
	})
	return k, u, err
}

// TouchOAuthGrant records use of a grant (and activity of its owner), at most
// once per TokenLastUsedEvery.
func (s *Store) TouchOAuthGrant(ctx context.Context, grantID, userID string) error {
	now := s.nowMS()
	return s.inTx(ctx, true, func(t *tx) error {
		res, err := t.exec(ctx, "UPDATE oauth_grants SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at <= ?)",
			now, grantID, now-TokenLastUsedEvery.Milliseconds())
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		return t.markActive(ctx, userID, now)
	})
}

// grantHouseholds lists the households bound to the grant that the owner
// still belongs to.
func (t *tx) grantHouseholds(ctx context.Context, grantID, userID string) ([]TokenHousehold, error) {
	rows, err := t.query(ctx, `SELECT h.id, h.name, m.role FROM oauth_grant_households gh
		JOIN households h ON h.id = gh.household_id
		JOIN memberships m ON m.household_id = gh.household_id AND m.user_id = ?
		WHERE gh.grant_id = ? ORDER BY h.name, h.id`, userID, grantID)
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

// OAuthGrant is a connected app as the user sees it.
type OAuthGrant struct {
	ID         string
	ClientName string
	Scopes     []string
	Households []TokenHousehold
	CreatedAt  time.Time
	LastUsedAt time.Time // zero = never
}

// ListOAuthGrants lists the user's active (redeemed, not revoked, not
// expired) grants, newest first.
func (s *Store) ListOAuthGrants(ctx context.Context, userID string) ([]OAuthGrant, error) {
	var out []OAuthGrant
	err := s.inTx(ctx, false, func(t *tx) error {
		rows, err := t.query(ctx, `SELECT g.id, c.name, g.scopes, g.created_at, g.last_used_at
			FROM oauth_grants g JOIN oauth_clients c ON c.id = g.client_id
			WHERE g.user_id = ? AND g.activated_at IS NOT NULL AND g.revoked_at IS NULL AND g.expires_at > ?
			ORDER BY g.created_at DESC, g.id`, userID, s.nowMS())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g OAuthGrant
			var scopes string
			var created int64
			var used sql.NullInt64
			if err := rows.Scan(&g.ID, &g.ClientName, &scopes, &created, &used); err != nil {
				return err
			}
			g.Scopes, g.CreatedAt, g.LastUsedAt = strings.Fields(scopes), time.UnixMilli(created).UTC(), fromMS(used)
			out = append(out, g)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		_ = rows.Close()
		for i := range out {
			if out[i].Households, err = t.grantHouseholds(ctx, out[i].ID, userID); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// RevokeOAuthGrant revokes one of the user's grants at once (all its tokens
// stop working). ErrNotFound if it is not theirs or already revoked.
func (s *Store) RevokeOAuthGrant(ctx context.Context, userID, grantID string) error {
	return s.inTx(ctx, true, func(t *tx) error {
		return t.execOne(ctx, "UPDATE oauth_grants SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL",
			s.nowMS(), grantID, userID)
	})
}

// RevokeOAuthByToken is RFC 7009 revocation: the grant that the access or
// refresh token belongs to is revoked, provided the token was issued to
// clientID. ok is false when there was nothing to revoke.
func (s *Store) RevokeOAuthByToken(ctx context.Context, tokenHash, clientID string) (grantID, userID string, ok bool, err error) {
	err = s.inTx(ctx, true, func(t *tx) error {
		if err := t.queryRow(ctx, `SELECT g.id, g.user_id FROM oauth_tokens t JOIN oauth_grants g ON g.id = t.grant_id
			WHERE t.token_hash = ? AND g.client_id = ? AND g.revoked_at IS NULL`, tokenHash, clientID).Scan(&grantID, &userID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		ok = true
		return t.execOne(ctx, "UPDATE oauth_grants SET revoked_at = ? WHERE id = ?", s.nowMS(), grantID)
	})
	return grantID, userID, ok, err
}

// revokeUserOAuth revokes every active OAuth grant of the user (same reasons
// as revokeUserTokens) and audits oauth_revoke when any were.
func (t *tx) revokeUserOAuth(ctx context.Context, userID, via string, now int64) error {
	rows, err := t.query(ctx, "SELECT id FROM oauth_grants WHERE user_id = ? AND revoked_at IS NULL ORDER BY id", userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var idList []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		idList = append(idList, id)
	}
	if err := rows.Err(); err != nil || len(idList) == 0 {
		return err
	}
	_ = rows.Close()
	if _, err := t.exec(ctx, "UPDATE oauth_grants SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL", now, userID); err != nil {
		return err
	}
	return t.insertAudit(ctx, AuditEvent{ActorUserID: userID, Event: AuditOAuthRevoke,
		Meta: map[string]string{"via": via, "count": strconv.Itoa(len(idList)), "grants": strings.Join(idList, ",")}}, now)
}

// dropGrantBindings is dropTokenBindings for OAuth grants.
func (t *tx) dropGrantBindings(ctx context.Context, householdID, userID string) error {
	_, err := t.exec(ctx, "DELETE FROM oauth_grant_households WHERE household_id = ? AND grant_id IN (SELECT id FROM oauth_grants WHERE user_id = ?)",
		householdID, userID)
	return err
}
