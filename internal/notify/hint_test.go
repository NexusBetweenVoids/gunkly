package notify

import (
	"os"
	"strings"
	"testing"
)

// Every kind is covered: a credential in the URL (marked SECRET) never shows
// up in the hint, and only a server host the user chose does.
func TestHintRevealsNoSecretCharacters(t *testing.T) {
	cases := []struct{ kind, url, want string }{
		{KindDiscord, "discord://SECRETtoken@SECRETid", "discord"},
		{KindSlack, "slack://hook:SECRETa-SECRETb-SECRETc@webhook", "slack"},
		{KindTelegram, "telegram://123456:SECRETTOKENabcd@telegram?chats=@me", "telegram"},
		{KindPushover, "pushover://shoutrrr:SECRETtoken@SECRETuser", "pushover"},
		{KindPushbullet, "pushbullet://SECRETtoken/device", "pushbullet"},
		{KindGoogleChat, "googlechat://chat.googleapis.com/v1/spaces/SECRET/messages?key=SECRETk&token=SECRETt", "googlechat · chat.googleapis.com"},
		{KindNtfy, "ntfy://:tk_SECRET@ntfy.sh/SECRETtopic", "ntfy · ntfy.sh"},
		{KindNtfySelfHosted, "ntfy://:tk_SECRET@ntfy.example.com/SECRETtopic", "ntfy.example.com"},
		{KindGeneric, "generic://user:SECRETpass@hooks.example.org:8443/path/SECRET?x=1", "hooks.example.org"},
		{KindSMTP, "smtp://user:SECRETpass@mail.example.com:587/?from=a@example.com&to=b@example.com", "mail.example.com"},
		{KindMQTT, "mqtts://user:SECRETpass@broker.example.com:8883/SECRETtopic", "broker.example.com"},
		{KindXMPP, "xmpp://user:SECRETpass@xmpp.example.com", "xmpp.example.com"},
		{KindGotify, "gotify://gotify.example.com/SECRETtoken", "gotify.example.com"},
		{KindMatrix, "matrix://user:SECRETpass@matrix.example.com/?rooms=!SECRETroom", "matrix.example.com"},
		{KindMattermost, "mattermost://bot@chat.example.com/SECRETtoken", "chat.example.com"},
		{KindRocketChat, "rocketchat://bot@rocket.example.com/SECRETa/SECRETb", "rocket.example.com"},
		{KindZulip, "zulip://bot%40example.com:SECRETkey@zulip.example.com/?stream=s", "zulip.example.com"},
		{KindHomeAssistant, "homeassistant://ha.example.com:8123/SECRETwebhook", "ha.example.com"},
		{KindSignal, "signal://user:SECRETpass@signal.example.com:8080/+15550001/+15550002", "signal.example.com"},
		{KindBark, "bark://:SECRETdevice@bark.example.com", "bark.example.com"},
		{KindTeams, "teams://SECRETgroup@SECRETtenant/SECRETalt/SECRETowner?host=SECRET.webhook.office.com", "teams"},
		{KindTwilio, "twilio://SECRETsid:SECRETtoken@SECRET15551234/+15559876", "twilio"},
		{KindPagerDuty, "pagerduty://events.pagerduty.com/SECRETkey", "events.pagerduty.com"},
		{KindOpsgenie, "opsgenie://api.opsgenie.com/SECRETkey", "api.opsgenie.com"},
		{KindIFTTT, "ifttt://SECRETwebhook/?events=chore", "ifttt"},
		{KindJoin, "join://shoutrrr:SECRETkey@join/?devices=SECRETdev", "join"},
		{KindLark, "lark://open.larksuite.com/SECRETtoken?secret=SECRETs", "open.larksuite.com"},
		{KindNotifiarr, "notifiarr://SECRETapikey", "notifiarr"},
		{KindSignalgrid, "signalgrid://SECRETkey@SECRETchannel", "signalgrid"},
		{KindWeCom, "wecom://SECRETkey", "wecom"},
	}
	all := map[string]bool{}
	for _, k := range AllKinds() {
		all[k] = true
	}
	for _, c := range cases {
		delete(all, c.kind)
		kind, err := ValidateURL(c.url, map[string]bool{c.kind: true})
		if err != nil || kind != c.kind {
			t.Errorf("%s: test URL not accepted: %q %v", c.kind, kind, err)
			continue
		}
		got := Hint(c.kind, c.url)
		if got != c.want {
			t.Errorf("Hint(%s) = %q, want %q", c.kind, got, c.want)
		}
		if strings.Contains(strings.ToUpper(got), "SECRET") || strings.Contains(got, "1555") {
			t.Errorf("Hint(%s) leaks a secret: %q", c.kind, got)
		}
	}
	for k := range all {
		t.Errorf("kind %s has no hint test case", k)
	}
}

// The migration that resets stored hints names exactly the optional kinds
// that show only their name.
func TestHintMigrationKinds(t *testing.T) {
	b, err := os.ReadFile("../db/migrations/0023_channel_hint_kind.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(b)
	for _, k := range AllKinds() {
		_, fixedKind := fixedServices[k]
		want := !fixedKind && !serverHostKinds[k]
		if got := strings.Contains(sql, "'"+k+"'"); got != want {
			t.Errorf("kind %s in migration = %v, want %v", k, got, want)
		}
	}
}
