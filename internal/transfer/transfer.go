// Package transfer defines the portable export/import file formats (PRD:
// Interface & languages) and validates imported files. It is pure: no I/O,
// no database. internal/store builds export documents and applies validated
// imports; internal/api serves them.
//
// Format versioning: every file carries {"format": ..., "version": N}. Files
// with another format or a version this build does not know are rejected;
// unknown fields are rejected too (strict schema), so a version bump is the
// only way to extend a format.
package transfer

import (
	"time"
)

// Format names and the current version of each.
const (
	HouseholdFormat = "gunkly-household-export"
	UserFormat      = "gunkly-user-export"
	Version         = 1
)

// Household is the household export file (and the import file).
type Household struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exportedAt"`
	Household  Settings  `json:"household"`
	Areas      []Area    `json:"areas"`
	Chores     []Chore   `json:"chores"`
}

// Settings are the household settings that travel with a file. A merge
// import never applies them to the target household.
type Settings struct {
	Name                  string `json:"name"`
	Timezone              string `json:"timezone"`
	QuietHours            *Quiet `json:"quietHours"`
	DefaultReminderPreset string `json:"defaultReminderPreset"`
	// AreasEnabled: the areas feature is on. A new household made from a file
	// turns it on too when the file has areas.
	AreasEnabled bool `json:"areasEnabled"`
}

// Quiet is a quiet-hours window in minutes after local midnight.
type Quiet struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Area is a named group of chores.
type Area struct {
	Name string `json:"name"`
}

// Person identifies a household member in a file. Both fields are hints: on
// import only the username is matched, and only against members of the
// target household.
type Person struct {
	DisplayName string `json:"displayName"`
	Username    string `json:"username"`
}

// Interval is "every N days/weeks/months".
type Interval struct {
	Every int    `json:"every"`
	Unit  string `json:"unit"` // day | week | month
}

// Schedule is a chore's recurrence rule.
type Schedule struct {
	Type     string    `json:"type"` // interval | rrule
	Interval *Interval `json:"interval,omitempty"`
	RRule    string    `json:"rrule,omitempty"`
	Anchor   string    `json:"anchor,omitempty"` // from_completion | from_scheduled
	FirstDue string    `json:"firstDue"`         // local date YYYY-MM-DD
}

// Reminder is one custom reminder spec (see schedule.Reminder).
type Reminder struct {
	Kind   string `json:"kind"` // before | on | overdue
	Days   int    `json:"days"`
	Minute int    `json:"minute"`
}

// Completion is one history entry of a chore.
type Completion struct {
	CompletedAt time.Time `json:"completedAt"`
	By          *Person   `json:"by"`
	Note        string    `json:"note"`
	Source      string    `json:"source"` // app | mark_done_link | api_token | oauth
	Reverted    bool      `json:"reverted"`
}

// Chore is a chore with its reminders, assignees and history.
type Chore struct {
	Name               string     `json:"name"`
	Notes              string     `json:"notes"`
	Link               string     `json:"link"`
	Area               string     `json:"area"` // name of an entry of Household.Areas, "" = none
	Kind               string     `json:"kind"` // recurring | oneoff
	Schedule           Schedule   `json:"schedule"`
	DueDate            string     `json:"dueDate"`
	NextDue            *time.Time `json:"nextDue"`
	PausedUntil        *time.Time `json:"pausedUntil"`
	PausedIndefinitely bool       `json:"pausedIndefinitely"`
	ArchivedAt         *time.Time `json:"archivedAt"`
	ReminderPreset     string     `json:"reminderPreset"` // "" = household default | built-in preset | custom
	Reminders          []Reminder `json:"reminders"`
	Assignees          []Person   `json:"assignees"` // in rotation order
	// Rotation ("Take turns"): RotationAdvance is "any" or "assignee" and Turn
	// the person whose turn it is (one of Assignees; "" / null when not rotating).
	Rotation        bool         `json:"rotation,omitempty"`
	RotationAdvance string       `json:"rotationAdvance,omitempty"`
	Turn            *Person      `json:"turn,omitempty"`
	Completions     []Completion `json:"completions"`
}

// User is the personal data export file. It never contains credentials,
// tokens, notification URLs or calendar feed links.
type User struct {
	Format      string           `json:"format"`
	Version     int              `json:"version"`
	ExportedAt  time.Time        `json:"exportedAt"`
	Profile     UserProfile      `json:"profile"`
	Preferences UserPreferences  `json:"preferences"`
	Memberships []UserMembership `json:"memberships"`
	Completions []UserCompletion `json:"completions"`
	Channels    []UserChannel    `json:"channels"`
	Sessions    []UserSession    `json:"sessions"`
	Passkeys    []UserPasskey    `json:"passkeys"`
	Audit       []UserAudit      `json:"audit"`
}

// UserProfile is the account's own record.
type UserProfile struct {
	Username      string    `json:"username"`
	DisplayName   string    `json:"displayName"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"emailVerified"`
	TOTPEnabled   bool      `json:"totpEnabled"`
	SSOLinked     bool      `json:"ssoLinked"`
	CreatedAt     time.Time `json:"createdAt"`
}

// UserPreferences are the account's display settings and muted chores.
type UserPreferences struct {
	Locale      string      `json:"locale"`
	WeekStart   *int        `json:"weekStart"`
	QuietHours  *Quiet      `json:"quietHours"` // personal quiet hours
	TimeZone    string      `json:"timeZone"`   // IANA name; "" = follow the household
	MutedChores []MutedItem `json:"mutedChores"`
}

// MutedItem names a chore the user muted.
type MutedItem struct {
	Household string `json:"household"`
	Chore     string `json:"chore"`
}

// UserMembership is one household the user belongs to.
type UserMembership struct {
	Household string    `json:"household"`
	Role      string    `json:"role"`
	JoinedAt  time.Time `json:"joinedAt"`
}

// UserCompletion is a chore the user completed.
type UserCompletion struct {
	Chore       string    `json:"chore"`
	Household   string    `json:"household"`
	CompletedAt time.Time `json:"completedAt"`
	Note        string    `json:"note"`
	Source      string    `json:"source"`
	Reverted    bool      `json:"reverted"`
}

// UserChannel is a personal notification channel: kind and label only.
type UserChannel struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

// UserSession is a signed-in device (no tokens, no identifiers).
type UserSession struct {
	Device     string    `json:"device"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// UserPasskey is a registered passkey: its name and dates only, no key material.
type UserPasskey struct {
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}

// UserAudit is one of the user's own audit-log events.
type UserAudit struct {
	Event     string            `json:"event"`
	IP        string            `json:"ip"`
	Meta      map[string]string `json:"meta,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
}
