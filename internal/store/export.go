package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/transfer"
)

// Audit events of the data export and import features.
const (
	AuditAccountExport   = "account_export"
	AuditHouseholdExport = "household_export"
	AuditHouseholdImport = "household_import"
)

// ExportHousehold builds the household export file: settings, areas, chores
// with schedule, reminders, assignees (names as hints) and completion history.
// Member or owner; guests get ErrForbidden. Never includes channels, feeds,
// tokens, invites or member emails. One snapshot transaction.
func (s *Store) ExportHousehold(ctx context.Context, a Actor, householdID string) (*transfer.Household, error) {
	out := &transfer.Household{Format: transfer.HouseholdFormat, Version: transfer.Version,
		ExportedAt: s.now().UTC().Truncate(time.Second), Areas: []transfer.Area{}, Chores: []transfer.Chore{}}
	err := s.inTx(ctx, false, func(t *tx) error {
		if _, err := t.authorize(ctx, a, householdID, RoleMember, false); err != nil {
			return err
		}
		var qs, qe sql.NullInt64
		h := &out.Household
		if err := t.queryRow(ctx, "SELECT name, timezone, quiet_start_min, quiet_end_min, default_reminder_preset, areas_enabled FROM households WHERE id = ?",
			householdID).Scan(&h.Name, &h.Timezone, &qs, &qe, &h.DefaultReminderPreset, &h.AreasEnabled); err != nil {
			return notFound(err)
		}
		if qs.Valid && qe.Valid {
			h.QuietHours = &transfer.Quiet{Start: int(qs.Int64), End: int(qe.Int64)}
		}
		areaName, err := t.exportAreas(ctx, householdID, out)
		if err != nil {
			return err
		}
		idx, err := t.exportChores(ctx, householdID, areaName, out)
		if err != nil {
			return err
		}
		if err := t.exportReminders(ctx, householdID, idx, out); err != nil {
			return err
		}
		if err := t.exportAssignees(ctx, householdID, idx, out); err != nil {
			return err
		}
		return t.exportCompletions(ctx, householdID, idx, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (t *tx) exportAreas(ctx context.Context, hh string, out *transfer.Household) (map[string]string, error) {
	names := map[string]string{}
	rows, err := t.query(ctx, "SELECT id, name FROM areas WHERE household_id = ? ORDER BY name_lower, id", hh)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		names[id] = name
		out.Areas = append(out.Areas, transfer.Area{Name: name})
	}
	return names, rows.Err()
}

// exportChores appends the chores and returns chore ID -> index in out.Chores.
func (t *tx) exportChores(ctx context.Context, hh string, areaName map[string]string, out *transfer.Household) (map[string]int, error) {
	idx := map[string]int{}
	rows, err := t.query(ctx, "SELECT "+choreCols+" FROM chores WHERE household_id = ? ORDER BY created_at, id", hh)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanChore(rows)
		if err != nil {
			return nil, err
		}
		ec := transfer.Chore{
			Name: c.Name, Notes: c.Notes, Link: c.Link, Area: areaName[c.AreaID], Kind: c.Kind,
			Schedule:    transfer.Schedule{Type: c.ScheduleType, Anchor: c.Anchor, FirstDue: c.FirstDue},
			DueDate:     c.DueDate,
			NextDue:     timeOrNil(c.NextDue),
			PausedUntil: timeOrNil(c.PausedUntil), PausedIndefinitely: c.PausedIndefinitely,
			ArchivedAt: timeOrNil(c.ArchivedAt), ReminderPreset: c.ReminderPreset,
			Rotation:  c.Rotation,
			Reminders: []transfer.Reminder{}, Assignees: []transfer.Person{}, Completions: []transfer.Completion{},
		}
		if c.Kind == KindRecurring {
			if c.ScheduleType == ScheduleRRule {
				ec.Schedule.RRule = c.RRule
			} else {
				ec.Schedule.Interval = &transfer.Interval{Every: c.IntervalN, Unit: c.IntervalUnit}
			}
		}
		if c.Rotation {
			ec.RotationAdvance = c.RotationAdvance
		}
		idx[c.ID] = len(out.Chores)
		out.Chores = append(out.Chores, ec)
	}
	return idx, rows.Err()
}

func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (t *tx) exportReminders(ctx context.Context, hh string, idx map[string]int, out *transfer.Household) error {
	rows, err := t.query(ctx, `SELECT r.chore_id, r.kind, r.days_before, r.time_of_day FROM chore_reminders r
		JOIN chores c ON c.id = r.chore_id WHERE c.household_id = ? ORDER BY r.chore_id, r.kind, r.days_before DESC, r.time_of_day`, hh)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var r transfer.Reminder
		if err := rows.Scan(&id, &r.Kind, &r.Days, &r.Minute); err != nil {
			return err
		}
		if r.Kind == "on" {
			r.Days = 0
		}
		c := &out.Chores[idx[id]]
		c.Reminders = append(c.Reminders, r)
	}
	return rows.Err()
}

func (t *tx) exportAssignees(ctx context.Context, hh string, idx map[string]int, out *transfer.Household) error {
	rows, err := t.query(ctx, `SELECT ca.chore_id, u.display_name, u.username,
			CASE WHEN c.rotation = TRUE AND ca.user_id = c.turn_user_id THEN 1 ELSE 0 END
		FROM chore_assignees ca
		JOIN chores c ON c.id = ca.chore_id JOIN users u ON u.id = ca.user_id
		WHERE c.household_id = ? ORDER BY ca.chore_id, ca.position, u.username_lower`, hh)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var p transfer.Person
		var turn int
		if err := rows.Scan(&id, &p.DisplayName, &p.Username, &turn); err != nil {
			return err
		}
		c := &out.Chores[idx[id]]
		c.Assignees = append(c.Assignees, p)
		if turn == 1 {
			c.Turn = &p
		}
	}
	return rows.Err()
}

func (t *tx) exportCompletions(ctx context.Context, hh string, idx map[string]int, out *transfer.Household) error {
	rows, err := t.query(ctx, `SELECT co.chore_id, co.completed_at, co.source, co.note, co.reverted_at, u.display_name, u.username
		FROM completions co JOIN chores c ON c.id = co.chore_id LEFT JOIN users u ON u.id = co.user_id
		WHERE c.household_id = ? ORDER BY co.chore_id, co.completed_at, co.id`, hh)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var at int64
		var rev sql.NullInt64
		var name, user sql.NullString
		c := transfer.Completion{}
		if err := rows.Scan(&id, &at, &c.Source, &c.Note, &rev, &name, &user); err != nil {
			return err
		}
		c.CompletedAt, c.Reverted = time.UnixMilli(at).UTC(), rev.Valid
		if user.Valid {
			c.By = &transfer.Person{DisplayName: name.String, Username: user.String}
		}
		ch := &out.Chores[idx[id]]
		ch.Completions = append(ch.Completions, c)
	}
	return rows.Err()
}

// ExportUser builds the caller's personal data export: profile, preferences,
// memberships, their own completions, personal notification channels (kind
// and label only), sessions and own audit events. It never reads password
// hashes, TOTP, passkey or recovery material, channel URLs or ciphertext, session
// tokens or calendar feed tokens.
func (s *Store) ExportUser(ctx context.Context, userID string) (*transfer.User, error) {
	out := &transfer.User{Format: transfer.UserFormat, Version: transfer.Version, ExportedAt: s.now().UTC().Truncate(time.Second),
		Preferences: transfer.UserPreferences{MutedChores: []transfer.MutedItem{}},
		Memberships: []transfer.UserMembership{}, Completions: []transfer.UserCompletion{},
		Channels: []transfer.UserChannel{}, Sessions: []transfer.UserSession{}, Passkeys: []transfer.UserPasskey{}, Audit: []transfer.UserAudit{}}
	err := s.inTx(ctx, false, func(t *tx) error {
		var ws, qs, qe sql.NullInt64
		var created int64
		p, pr := &out.Profile, &out.Preferences
		if err := t.queryRow(ctx, `SELECT username, display_name, COALESCE(email, ''), email_verified, totp_enabled, created_at, locale, week_start, quiet_start_min, quiet_end_min
			FROM users WHERE id = ?`, userID).Scan(&p.Username, &p.DisplayName, &p.Email, &p.EmailVerified, &p.TOTPEnabled, &created, &pr.Locale, &ws, &qs, &qe); err != nil {
			return notFound(err)
		}
		p.CreatedAt = time.UnixMilli(created).UTC()
		if ws.Valid {
			w := int(ws.Int64)
			pr.WeekStart = &w
		}
		if qs.Valid && qe.Valid {
			pr.QuietHours = &transfer.Quiet{Start: int(qs.Int64), End: int(qe.Int64)}
		}
		return t.exportUserRows(ctx, userID, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (t *tx) exportUserRows(ctx context.Context, userID string, out *transfer.User) error {
	scans := []struct {
		query string
		each  func(*sql.Rows) error
	}{
		{`SELECT h.name, m.role, m.joined_at FROM memberships m JOIN households h ON h.id = m.household_id
			WHERE m.user_id = ? ORDER BY m.joined_at, h.id`, func(r *sql.Rows) error {
			var m transfer.UserMembership
			var at int64
			if err := r.Scan(&m.Household, &m.Role, &at); err != nil {
				return err
			}
			m.JoinedAt = time.UnixMilli(at).UTC()
			out.Memberships = append(out.Memberships, m)
			return nil
		}},
		{`SELECT c.name, h.name, co.completed_at, co.note, co.source, co.reverted_at FROM completions co
			JOIN chores c ON c.id = co.chore_id JOIN households h ON h.id = c.household_id
			WHERE co.user_id = ? ORDER BY co.completed_at, co.id`, func(r *sql.Rows) error {
			var c transfer.UserCompletion
			var at int64
			var rev sql.NullInt64
			if err := r.Scan(&c.Chore, &c.Household, &at, &c.Note, &c.Source, &rev); err != nil {
				return err
			}
			c.CompletedAt, c.Reverted = time.UnixMilli(at).UTC(), rev.Valid
			out.Completions = append(out.Completions, c)
			return nil
		}},
		{`SELECT service, label FROM notification_channels WHERE scope = 'user' AND user_id = ? ORDER BY created_at, id`, func(r *sql.Rows) error {
			var c transfer.UserChannel
			if err := r.Scan(&c.Kind, &c.Label); err != nil {
				return err
			}
			out.Channels = append(out.Channels, c)
			return nil
		}},
		{`SELECT user_agent, created_at, last_seen_at, expires_at FROM sessions WHERE user_id = ? ORDER BY created_at, id`, func(r *sql.Rows) error {
			var s transfer.UserSession
			var c, l, e int64
			if err := r.Scan(&s.Device, &c, &l, &e); err != nil {
				return err
			}
			s.CreatedAt, s.LastSeenAt, s.ExpiresAt = time.UnixMilli(c).UTC(), time.UnixMilli(l).UTC(), time.UnixMilli(e).UTC()
			out.Sessions = append(out.Sessions, s)
			return nil
		}},
		{`SELECT name, created_at, last_used_at FROM passkeys WHERE user_id = ? ORDER BY created_at, id`, func(r *sql.Rows) error {
			var k transfer.UserPasskey
			var c int64
			var l sql.NullInt64
			if err := r.Scan(&k.Name, &c, &l); err != nil {
				return err
			}
			k.CreatedAt = time.UnixMilli(c).UTC()
			if l.Valid {
				u := time.UnixMilli(l.Int64).UTC()
				k.LastUsedAt = &u
			}
			out.Passkeys = append(out.Passkeys, k)
			return nil
		}},
		{`SELECT event, ip, meta, created_at FROM audit_log WHERE actor_user_id = ? ORDER BY created_at, id`, func(r *sql.Rows) error {
			var e transfer.UserAudit
			var meta string
			var at int64
			if err := r.Scan(&e.Event, &e.IP, &meta, &at); err != nil {
				return err
			}
			if json.Unmarshal([]byte(meta), &e.Meta) != nil {
				e.Meta = nil
			}
			e.CreatedAt = time.UnixMilli(at).UTC()
			out.Audit = append(out.Audit, e)
			return nil
		}},
		{`SELECT h.name, c.name FROM chore_mutes m JOIN chores c ON c.id = m.chore_id JOIN households h ON h.id = c.household_id
			WHERE m.user_id = ? ORDER BY h.name, c.name`, func(r *sql.Rows) error {
			var m transfer.MutedItem
			if err := r.Scan(&m.Household, &m.Chore); err != nil {
				return err
			}
			out.Preferences.MutedChores = append(out.Preferences.MutedChores, m)
			return nil
		}},
	}
	for _, sc := range scans {
		if err := func() error {
			rows, err := t.query(ctx, sc.query, userID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				if err := sc.each(rows); err != nil {
					return err
				}
			}
			return rows.Err()
		}(); err != nil {
			return err
		}
	}
	return nil
}

// userMatch returns the lower-cased, trimmed username used for matching.
func userMatch(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
