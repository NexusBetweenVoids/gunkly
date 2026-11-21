package auth

import (
	"encoding/base32"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"golang.org/x/crypto/argon2"
)

func TestPasswordHashVerify(t *testing.T) {
	h := HashPassword("correct horse battery")
	if !regexp.MustCompile(`^\$argon2id\$v=19\$m=19456,t=2,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`).MatchString(h) {
		t.Fatalf("unexpected PHC string %q", h)
	}
	if h == HashPassword("correct horse battery") {
		t.Fatal("salt not random")
	}
	if ok, err := VerifyPassword(h, "correct horse battery"); !ok || err != nil {
		t.Fatalf("verify correct = %v, %v", ok, err)
	}
	if ok, err := VerifyPassword(h, "correct horse batterY"); ok || err != nil {
		t.Fatalf("verify wrong = %v, %v", ok, err)
	}
	if NeedsRehash(h) {
		t.Fatal("fresh hash needs rehash")
	}
}

func TestPasswordOtherParams(t *testing.T) {
	// Older/weaker parameters still verify but need rehash.
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte("pw"), salt, 1, 8192, 1, 32)
	h := encodePHC(argonParams{memory: 8192, time: 1, threads: 1}, salt, key)
	if ok, err := VerifyPassword(h, "pw"); !ok || err != nil {
		t.Fatalf("verify = %v, %v", ok, err)
	}
	if !NeedsRehash(h) {
		t.Fatal("weaker params should need rehash")
	}
}

func TestPasswordMalformed(t *testing.T) {
	good := HashPassword("x")
	parts := strings.Split(good, "$")
	bad := []string{
		"",
		"plain",
		strings.Replace(good, "argon2id", "argon2i", 1),
		strings.Replace(good, "v=19", "v=16", 1),
		strings.Replace(good, "m=19456", "m=4294967295", 1),
		strings.Replace(good, "m=19456", "m=019456", 1),
		strings.Replace(good, "t=2", "t=0", 1),
		strings.Replace(good, "t=2", "t=1000", 1),
		strings.Replace(good, "p=1", "p=0", 1),
		strings.Replace(good, "p=1", "p=1,x", 1),
		"$argon2id$v=19$m=19456,t=2,p=1$$" + parts[5],
		"$argon2id$v=19$m=19456,t=2,p=1$" + parts[4] + "$",
		"$argon2id$v=19$m=19456,t=2,p=1$" + parts[4] + "$" + parts[5] + "=",
		good + "$",
	}
	for _, h := range bad {
		if ok, err := VerifyPassword(h, "x"); ok || !errors.Is(err, ErrMalformedHash) {
			t.Errorf("%q: verify = %v, %v", h, ok, err)
		}
		if !NeedsRehash(h) {
			t.Errorf("%q: malformed hash should need rehash", h)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		pw  string
		min int
		err error
	}{
		{"123456789", 0, ErrPasswordTooShort},
		{"1234567890", 0, nil},
		{"ééééééééé", 0, ErrPasswordTooShort}, // 9 characters, 18 bytes
		{"éééééééééé", 0, nil},
		{"short", 5, nil},
		{"shor", 5, ErrPasswordTooShort},
		{strings.Repeat("a", MaxPasswordBytes), 0, nil},
		{strings.Repeat("a", MaxPasswordBytes+1), 0, ErrPasswordTooLong},
		{"0123456789\xff", 0, ErrPasswordInvalid},
	}
	for _, c := range cases {
		if err := ValidatePassword(c.pw, c.min); !errors.Is(err, c.err) {
			t.Errorf("ValidatePassword(%q, %d) = %v, want %v", c.pw, c.min, err, c.err)
		}
	}
}

// RFC 6238 Appendix B, SHA-1 (8-digit codes; a 6-digit code is the last 6).
func TestTOTPVectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	vectors := []struct {
		unix int64
		code string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, v := range vectors {
		want := v.code[2:]
		step, ok := ValidateTOTP(secret, want, time.Unix(v.unix, 0))
		if !ok || step != v.unix/30 {
			t.Errorf("t=%d: ValidateTOTP(%s) = %d, %v", v.unix, want, step, ok)
		}
		if got := hotp([]byte("12345678901234567890"), v.unix/30); got != want {
			t.Errorf("t=%d: hotp = %s, want %s", v.unix, got, want)
		}
	}
	// RFC 4226 Appendix D HOTP values.
	for i, want := range []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"} {
		if got := hotp([]byte("12345678901234567890"), int64(i)); got != want {
			t.Errorf("hotp(%d) = %s, want %s", i, got, want)
		}
	}
}

func TestTOTPSkewAndReplayStep(t *testing.T) {
	secret := GenerateTOTPSecret()
	if len(secret) != 32 {
		t.Fatalf("secret length %d, want 32 base32 chars", len(secret))
	}
	key, _ := totpEncoding.DecodeString(secret)
	now := time.Unix(1_700_000_000, 0)
	cur := now.Unix() / 30
	for d := int64(-3); d <= 3; d++ {
		step, ok := ValidateTOTP(secret, hotp(key, cur+d), now)
		wantOK := d >= -1 && d <= 1
		if ok != wantOK || (ok && step != cur+d) {
			t.Errorf("offset %d: step=%d ok=%v", d, step, ok)
		}
	}
	// Lower-case secret accepted.
	if _, ok := ValidateTOTP(strings.ToLower(secret), hotp(key, cur), now); !ok {
		t.Error("lower-case secret rejected")
	}
	for _, code := range []string{"", "12345", "1234567", "12345a", " 12345", "+12345", "１２３４５６"} {
		if _, ok := ValidateTOTP(secret, code, now); ok {
			t.Errorf("code %q accepted", code)
		}
	}
	if _, ok := ValidateTOTP("not base32!", "123456", now); ok {
		t.Error("bad secret accepted")
	}
}

func TestTOTPURI(t *testing.T) {
	u, err := url.Parse(TOTPURI("JBSWY3DPEHPK3PXP", "ann+x@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme != "otpauth" || u.Host != "totp" || u.Path != "/Gunkly:ann+x@example.com" ||
		q.Get("secret") != "JBSWY3DPEHPK3PXP" || q.Get("issuer") != "Gunkly" ||
		q.Get("digits") != "6" || q.Get("period") != "30" || q.Get("algorithm") != "SHA1" {
		t.Fatalf("bad URI %s", u)
	}
	// A colon or slash in the account name must not change the label structure.
	if raw := TOTPURI("S", "a:b/c?d"); !strings.HasPrefix(raw, "otpauth://totp/Gunkly:a%3Ab%2Fc%3Fd?") {
		t.Fatalf("account not escaped: %s", raw)
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, hashes := GenerateRecoveryCodes(RecoveryCodeCount)
	if len(codes) != RecoveryCodeCount || len(hashes) != RecoveryCodeCount {
		t.Fatal("wrong count")
	}
	format := regexp.MustCompile(`^[0-9a-hjkmnp-tv-z]{4}-[0-9a-hjkmnp-tv-z]{4}-[0-9a-hjkmnp-tv-z]{4}$`)
	seen := map[string]bool{}
	for i, c := range codes {
		if !format.MatchString(c) {
			t.Errorf("bad code %q", c)
		}
		if seen[c] {
			t.Errorf("duplicate code %q", c)
		}
		seen[c] = true
		for _, in := range []string{c, strings.ToUpper(c), strings.ReplaceAll(c, "-", ""), " " + strings.ReplaceAll(c, "-", " ") + " "} {
			h, ok := HashRecoveryCode(in)
			if !ok || !ids.Equal(h, hashes[i]) {
				t.Errorf("input %q does not match code %q", in, c)
			}
		}
	}
	// Look-alikes normalise.
	h1, _ := HashRecoveryCode("0011-aaaa-bbbb")
	h2, ok := HashRecoveryCode("OoIL-AAAA-BBBB")
	if !ok || h1 != h2 {
		t.Error("look-alikes not normalised")
	}
	for _, in := range []string{"", "abcd-efgh-jkm", "abcd-efgh-jkmnp", "abcd-efgh-jkmu", "abcd_efgh_jkmn", "abcd-efgh-jkmé"} {
		if _, ok := HashRecoveryCode(in); ok {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestEmailOTP(t *testing.T) {
	counts := map[byte]int{}
	for range 2000 {
		c := GenerateEmailOTP()
		if !regexp.MustCompile(`^[0-9]{6}$`).MatchString(c) {
			t.Fatalf("bad code %q", c)
		}
		counts[c[0]]++
	}
	if len(counts) != 10 {
		t.Errorf("leading digits not spread: %v", counts)
	}
	h := HashEmailOTP("challenge-1", "123456")
	if h != HashEmailOTP("challenge-1", " 123456 ") {
		t.Error("whitespace not trimmed")
	}
	if h == HashEmailOTP("challenge-2", "123456") || h == HashEmailOTP("challenge-1", "123457") {
		t.Error("hash not bound to challenge and code")
	}
}

var (
	secretA = strings.Repeat("a", MinSecretLen)
	secretB = strings.Repeat("b", MinSecretLen)
)

func TestKeyringRoundTrip(t *testing.T) {
	k, err := NewKeyring(1, map[uint32]string{1: secretA})
	if err != nil {
		t.Fatal(err)
	}
	ad := []byte("notification_channels:0193-abc:url")
	for _, pt := range []string{"", "discord://token@id", strings.Repeat("x", 10000)} {
		ct := k.Encrypt([]byte(pt), ad)
		if !strings.HasPrefix(ct, "v1:") {
			t.Fatalf("ciphertext %q lacks version prefix", ct)
		}
		if strings.Contains(ct, pt) && pt != "" {
			t.Fatal("plaintext visible")
		}
		got, err := k.Decrypt(ct, ad)
		if err != nil || string(got) != pt {
			t.Fatalf("roundtrip: %q, %v", got, err)
		}
		if k.NeedsRotation(ct) {
			t.Fatal("current ciphertext needs rotation")
		}
	}
	ct1, ct2 := k.Encrypt([]byte("x"), ad), k.Encrypt([]byte("x"), ad)
	if ct1 == ct2 {
		t.Fatal("nonce reuse")
	}
}

func TestKeyringFailures(t *testing.T) {
	k, _ := NewKeyring(1, map[uint32]string{1: secretA})
	ad := []byte("row-1:url")
	ct := k.Encrypt([]byte("secret"), ad)

	if _, err := k.Decrypt(ct, []byte("row-2:url")); !errors.Is(err, ErrDecrypt) {
		t.Errorf("wrong AD: %v", err)
	}
	if _, err := k.Decrypt(ct, nil); !errors.Is(err, ErrDecrypt) {
		t.Errorf("missing AD: %v", err)
	}
	if _, err := k.Decrypt("v2"+ct[2:], ad); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("unknown version: %v", err)
	}
	// Flip one bit in every byte position of the payload.
	body := []byte(ct)
	for i := 3; i < len(body); i++ {
		orig := body[i]
		body[i] ^= 0x01
		if _, err := k.Decrypt(string(body), ad); err == nil {
			t.Errorf("tamper at %d accepted", i)
		}
		body[i] = orig
	}
	for _, bad := range []string{"", "v1", "v1:", "1:" + ct[3:], "v:" + ct[3:], "v01:" + ct[3:], "v+1:" + ct[3:], "v0:" + ct[3:], "v1:!!!", "v1:" + ct[3:13], "v99999999999:" + ct[3:]} {
		if _, err := k.Decrypt(bad, ad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, bad := range []string{"", "v1", "1:" + ct[3:], "v01:" + ct[3:], "v0:" + ct[3:]} {
		if !k.NeedsRotation(bad) {
			t.Errorf("%q: bad version prefix should need rotation", bad)
		}
	}
}

func TestKeyringRotation(t *testing.T) {
	old, _ := NewKeyring(1, map[uint32]string{1: secretA})
	ad := []byte("ad")
	ct1 := old.Encrypt([]byte("secret"), ad)

	k, err := NewKeyring(2, map[uint32]string{1: secretA, 2: secretB})
	if err != nil {
		t.Fatal(err)
	}
	if !k.NeedsRotation(ct1) {
		t.Fatal("old ciphertext should need rotation")
	}
	pt, err := k.Decrypt(ct1, ad)
	if err != nil {
		t.Fatal(err)
	}
	ct2 := k.Encrypt(pt, ad)
	if !strings.HasPrefix(ct2, "v2:") || k.NeedsRotation(ct2) {
		t.Fatalf("re-encrypted %q", ct2)
	}
	// After rotation the old key can be dropped.
	newOnly, _ := NewKeyring(2, map[uint32]string{2: secretB})
	if got, err := newOnly.Decrypt(ct2, ad); err != nil || string(got) != "secret" {
		t.Fatalf("new-only decrypt: %q, %v", got, err)
	}
	if _, err := newOnly.Decrypt(ct1, ad); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("dropped key still decrypts: %v", err)
	}
	// Different secrets give different keys: v1 bytes under v2's key fail.
	if _, err := k.Decrypt("v2"+ct1[2:], ad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("cross-version: %v", err)
	}
}

func TestNewKeyringValidation(t *testing.T) {
	cases := []struct {
		cur     uint32
		secrets map[uint32]string
	}{
		{1, nil},
		{1, map[uint32]string{1: ""}},
		{1, map[uint32]string{1: strings.Repeat("a", MinSecretLen-1)}},
		{2, map[uint32]string{1: secretA}},
		{0, map[uint32]string{0: secretA}},
	}
	for i, c := range cases {
		if _, err := NewKeyring(c.cur, c.secrets); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}
