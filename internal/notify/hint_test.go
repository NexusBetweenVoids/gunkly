package notify

import (
	"strings"
	"testing"
)

func TestHintRevealsNoSecretCharacters(t *testing.T) {
	cases := []struct{ kind, url, want string }{
		{KindDiscord, "discord://tokensecret1234567890@1234567890", "discord"},
		{KindTelegram, "telegram://123456:SECRETTOKENabcd@telegram?chats=@me", "telegram"},
		{KindNtfy, "ntfy://:tk_secretvalue@ntfy.sh/topicname", "ntfy · ntfy.sh"},
		{KindGotify, "gotify://gotify.example.com/AbCdEfGhIjKl", "gotify.example.com"},
		{KindGeneric, "generic://user:pass@hooks.example.org:8443/path/secret?x=1", "hooks.example.org"},
	}
	for _, c := range cases {
		got := Hint(c.kind, c.url)
		if got != c.want {
			t.Errorf("Hint(%s) = %q, want %q", c.kind, got, c.want)
		}
		for _, secret := range []string{"1234567890", "SECRET", "secret", "pass", "AbCd", "topicname"} {
			if strings.Contains(c.url, secret) && strings.Contains(got, secret) {
				t.Errorf("Hint(%s) leaks %q: %q", c.kind, secret, got)
			}
		}
	}
}
