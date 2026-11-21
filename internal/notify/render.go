package notify

import (
	"github.com/NexusBetweenVoids/gunkly/internal/i18n"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
)

// Reminder is what a notification says.
type Reminder struct {
	Locale      string // recipient's locale; "" = default
	ChoreName   string
	Kind        schedule.ReminderKind
	Due         schedule.Date
	MarkDoneURL string // "" = no link (e.g. no base URL configured)
}

// Render builds the plain-text message for r. All text comes from
// internal/i18n. The chore name is user content and inserted verbatim:
// receivers treat the body as plain text.
func Render(r Reminder) Message {
	p := map[string]string{"chore": r.ChoreName, "date": r.Due.String()}
	key := "notify.on"
	switch r.Kind {
	case schedule.Before:
		key = "notify.before"
	case schedule.Overdue:
		key = "notify.overdue"
	}
	body := i18n.T(r.Locale, key, p)
	if r.MarkDoneURL != "" {
		body += "\n\n" + i18n.T(r.Locale, "notify.mark_done", map[string]string{"url": r.MarkDoneURL})
	}
	return Message{Title: i18n.T(r.Locale, "notify.title", p), Body: body}
}

// MarkDoneURL is the link contract shared with internal/api: GET
// <base>/done/<token> shows a confirmation page (store.PeekMarkDone) and only
// the POST completes the chore (store.ConsumeMarkDone).
func MarkDoneURL(baseURL, token string) string {
	if baseURL == "" || token == "" {
		return ""
	}
	return baseURL + "/done/" + token
}
