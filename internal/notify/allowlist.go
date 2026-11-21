// Package notify validates notification channel URLs against the service
// allowlist (ADR 0007), seals them at rest, renders reminder messages and
// defines the Sender that delivers them. Every send goes through the egress
// guard (ADR 0006).
//
// Channel URLs are secrets (bot tokens, webhook keys): never log them, never
// put them in errors returned to clients.
package notify

import (
	"errors"
	"maps"
	"net/url"
	"slices"
	"strings"
)

// Service kinds. A kind is what the admin allowlist enables; it is usually the
// Shoutrrr scheme, except that aliases share a kind (hangouts = googlechat,
// mqtts = mqtt, xmpps = xmpp, generic+https = generic) and ntfy is split by
// host: KindNtfy is the public ntfy.sh only, KindNtfySelfHosted any other host.
const (
	KindDiscord    = "discord"
	KindSlack      = "slack"
	KindTelegram   = "telegram"
	KindPushover   = "pushover"
	KindPushbullet = "pushbullet"
	KindGoogleChat = "googlechat"
	KindNtfy       = "ntfy"

	KindNtfySelfHosted = "ntfy-selfhosted"
	KindGeneric        = "generic"
	KindSMTP           = "smtp"
	KindMQTT           = "mqtt"
	KindXMPP           = "xmpp"
	KindGotify         = "gotify"
	KindMatrix         = "matrix"
	KindMattermost     = "mattermost"
	KindRocketChat     = "rocketchat"
	KindZulip          = "zulip"
	KindHomeAssistant  = "homeassistant"
	KindSignal         = "signal"
	KindBark           = "bark"
	KindTeams          = "teams"
	KindTwilio         = "twilio"
	KindPagerDuty      = "pagerduty"
	KindOpsgenie       = "opsgenie"
	KindIFTTT          = "ifttt"
	KindJoin           = "join"
	KindLark           = "lark"
	KindNotifiarr      = "notifiarr"
	KindSignalgrid     = "signalgrid"
	KindWeCom          = "wecom"
)

// Errors from ValidateURL. They carry no part of the URL.
var (
	ErrInvalidURL      = errors.New("notify: invalid service URL")
	ErrUnknownService  = errors.New("notify: unknown service")
	ErrServiceDisabled = errors.New("notify: service not enabled on this instance")
	ErrHostOverride    = errors.New("notify: URL overrides the provider's host or transport")
)

// maxURLLen bounds a channel URL.
const maxURLLen = 2048

// fixed describes a fixed-public-provider service: the provider host cannot be
// chosen by the user. host is the required URL host ("" = the host part is a
// token or ID, not a server) and keys the allowed query parameters; anything
// else (host, TLS or API URL overrides, unknown keys) is rejected.
type fixed struct {
	host string
	keys []string
}

// fixedServices are on by default (ADR 0007). The query keys are the
// Shoutrrr config keys that only change presentation; e.g. telegram's apiurl
// and skiptlsverify or ntfy's scheme/disabletls are deliberately absent.
var fixedServices = map[string]fixed{
	KindDiscord:    {keys: []string{"title", "username", "avatar", "avatarurl", "thread_id", "thread_name", "splitlines"}},
	KindSlack:      {keys: []string{"botname", "username", "icon", "icon_emoji", "icon_url", "color", "title", "thread_ts"}},
	KindTelegram:   {host: "telegram", keys: []string{"chats", "channels", "title", "preview", "notification"}},
	KindPushover:   {keys: []string{"title", "devices", "priority"}},
	KindPushbullet: {keys: []string{"title"}},
	KindGoogleChat: {host: "chat.googleapis.com", keys: []string{"key", "token"}},
	KindNtfy:       {host: "ntfy.sh", keys: []string{"title", "token", "tags", "priority", "click", "icon", "cache", "firebase"}},
}

// optionalSchemes maps the Shoutrrr schemes of user-chosen-host, paid and
// incident services to their kind. They are off unless the admin enables the
// kind; the egress guard still applies to every send. Shoutrrr's "logger"
// service is deliberately absent: it would write reminder text and mark-done
// links into the server log.
var optionalSchemes = map[string]string{
	"generic":       KindGeneric,
	"smtp":          KindSMTP,
	"mqtt":          KindMQTT,
	"mqtts":         KindMQTT,
	"xmpp":          KindXMPP,
	"xmpps":         KindXMPP,
	"gotify":        KindGotify,
	"matrix":        KindMatrix,
	"mattermost":    KindMattermost,
	"rocketchat":    KindRocketChat,
	"zulip":         KindZulip,
	"homeassistant": KindHomeAssistant,
	"signal":        KindSignal,
	"bark":          KindBark,
	"teams":         KindTeams,
	"twilio":        KindTwilio,
	"pagerduty":     KindPagerDuty,
	"opsgenie":      KindOpsgenie,
	"ifttt":         KindIFTTT,
	"join":          KindJoin,
	"lark":          KindLark,
	"notifiarr":     KindNotifiarr,
	"signalgrid":    KindSignalgrid,
	"wecom":         KindWeCom,
}

// DefaultKinds returns the kinds enabled on a fresh instance.
func DefaultKinds() map[string]bool {
	m := make(map[string]bool, len(fixedServices))
	for k := range fixedServices {
		m[k] = true
	}
	return m
}

// AllKinds lists every kind an admin can enable, sorted.
func AllKinds() []string {
	set := DefaultKinds()
	set[KindNtfySelfHosted] = true
	for _, k := range optionalSchemes {
		set[k] = true
	}
	return slices.Sorted(maps.Keys(set))
}

// ValidateURL parses a user-supplied Shoutrrr service URL and returns its
// kind if the kind is in enabled (nil = DefaultKinds). Fixed-provider URLs
// that override the provider's host, port, transport or API endpoint are
// rejected even when the kind is enabled. Errors never contain the URL.
func ValidateURL(raw string, enabled map[string]bool) (string, error) {
	if enabled == nil {
		enabled = DefaultKinds()
	}
	kind, err := classify(raw)
	if err != nil {
		return "", err
	}
	if !enabled[kind] {
		return "", ErrServiceDisabled
	}
	return kind, nil
}

func classify(raw string) (string, error) {
	if raw == "" || len(raw) > maxURLLen || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\x00\r\n\t ") {
		return "", ErrInvalidURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", ErrInvalidURL
	}
	scheme := strings.ToLower(u.Scheme)
	base, custom, _ := strings.Cut(scheme, "+")
	if custom != "" {
		// Only the generic webhook has a meaningful custom form
		// (generic+https://host/path); other services' custom forms
		// replace the provider endpoint with an arbitrary URL.
		if base != "generic" || (custom != "https" && custom != "http") {
			return "", ErrHostOverride
		}
	}
	if base == "hangouts" {
		base = KindGoogleChat
	}
	if base == KindNtfy && !strings.EqualFold(u.Host, "ntfy.sh") {
		if u.Host == "" {
			return "", ErrInvalidURL
		}
		return KindNtfySelfHosted, nil
	}
	if f, ok := fixedServices[base]; ok {
		return base, f.check(u)
	}
	if kind, ok := optionalSchemes[base]; ok {
		if u.Host == "" {
			return "", ErrInvalidURL
		}
		return kind, nil
	}
	return "", ErrUnknownService
}

func (f fixed) check(u *url.URL) error {
	if u.Host == "" || u.Port() != "" || strings.HasSuffix(u.Host, ":") {
		return ErrHostOverride
	}
	if f.host != "" && !strings.EqualFold(u.Host, f.host) {
		return ErrHostOverride
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return ErrInvalidURL
	}
	for k := range q {
		if !slices.Contains(f.keys, strings.ToLower(k)) {
			return ErrHostOverride
		}
	}
	return nil
}
