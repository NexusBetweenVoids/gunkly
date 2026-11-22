package webpush

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func dec(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRFC8291Vector reproduces the example of RFC 8291 appendix A.
func TestRFC8291Vector(t *testing.T) {
	plain := []byte("When I grow up, I want to be a watermelon")
	as, err := ecdh.P256().NewPrivateKey(dec(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	if got := enc(as.PublicKey().Bytes()); got != "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8" {
		t.Fatalf("as_public = %s", got)
	}
	ua := dec(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	authSecret := dec(t, "BTBZMqHH6r4Tts7J_aSIgg")
	salt := dec(t, "DGv6ra1nlYgDCS1FRnbzlw")
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	got, err := encrypt(plain, ua, authSecret, as, salt)
	if err != nil {
		t.Fatal(err)
	}
	if enc(got) != want {
		t.Fatalf("body mismatch:\n got %s\nwant %s", enc(got), want)
	}
}

// decrypt is the receiving side, written from the RFC, to round-trip Encrypt.
func decrypt(t *testing.T, body []byte, ua *ecdh.PrivateKey, authSecret []byte) []byte {
	t.Helper()
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	asPub, ct := body[21:21+idlen], body[21+idlen:]
	if rs != 4096 || idlen != 65 {
		t.Fatalf("rs=%d idlen=%d", rs, idlen)
	}
	pk, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := ua.ECDH(pk)
	if err != nil {
		t.Fatal(err)
	}
	info := append(append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...), asPub...)
	prkKey, _ := hkdf.Extract(sha256.New, secret, authSecret)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, string(info), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	b, _ := aes.NewCipher(cek)
	g, _ := cipher.NewGCM(b)
	pt, err := g.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pt[len(pt)-1] != 2 {
		t.Fatal("missing delimiter")
	}
	return pt[:len(pt)-1]
}

func newSub(t *testing.T) (*ecdh.PrivateKey, []byte, Keys) {
	t.Helper()
	ua, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a := []byte("0123456789abcdef")
	return ua, a, Keys{P256dh: enc(ua.PublicKey().Bytes()), Auth: enc(a)}
}

func TestEncryptRoundTripAndLimits(t *testing.T) {
	ua, a, k := newSub(t)
	body, err := Encrypt([]byte(`{"title":"x"}`), k)
	if err != nil {
		t.Fatal(err)
	}
	if got := decrypt(t, body, ua, a); string(got) != `{"title":"x"}` {
		t.Fatalf("got %q", got)
	}
	big := make([]byte, MaxPayload)
	body, err = Encrypt(big, k)
	if err != nil || len(body) > 4096 {
		t.Fatalf("max payload: %v len=%d", err, len(body))
	}
	if _, err := Encrypt(make([]byte, MaxPayload+1), k); !errors.Is(err, ErrTooLong) {
		t.Fatalf("err = %v", err)
	}
	for _, bad := range []Keys{{P256dh: "AAAA", Auth: k.Auth}, {P256dh: k.P256dh, Auth: "AAAA"}, {P256dh: "!!", Auth: k.Auth}} {
		if _, err := Encrypt([]byte("x"), bad); !errors.Is(err, ErrBadKey) {
			t.Errorf("%+v: err = %v", bad, err)
		}
	}
	// A 65-byte string that is not a curve point.
	off := append([]byte{4}, make([]byte, 64)...)
	if _, err := Encrypt([]byte("x"), Keys{P256dh: enc(off), Auth: k.Auth}); !errors.Is(err, ErrBadKey) {
		t.Errorf("off-curve: %v", err)
	}
}

func TestVAPIDJWT(t *testing.T) {
	v, err := NewVAPID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	h, err := v.Authorization("https://fcm.googleapis.com/fcm/send/abc", "https://gunkly.example", now)
	if err != nil {
		t.Fatal(err)
	}
	rest, ok := strings.CutPrefix(h, "vapid t=")
	jwt, k, ok2 := strings.Cut(rest, ", k=")
	if !ok || !ok2 || k != v.PublicKey() {
		t.Fatalf("header %q", h)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatal(jwt)
	}
	if string(dec(t, parts[0])) != `{"typ":"JWT","alg":"ES256"}` {
		t.Fatalf("header %s", dec(t, parts[0]))
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(dec(t, parts[1]), &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Aud != "https://fcm.googleapis.com" || claims.Sub != "https://gunkly.example" ||
		claims.Exp != now.Add(12*time.Hour).Unix() || claims.Exp-now.Unix() > 24*3600 {
		t.Fatalf("claims %+v", claims)
	}
	// Verify with the public key from the header, as a push service does.
	pub := dec(t, k)
	pk, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pub)
	if err != nil {
		t.Fatal(err)
	}
	sig := dec(t, parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(pk, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("signature does not verify")
	}
	sum[0] ^= 1
	if ecdsa.Verify(pk, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("tampered input verifies")
	}
	if _, err := v.Authorization("not a url", "x", now); err == nil {
		t.Fatal("bad endpoint accepted")
	}
}

func TestVAPIDKeyRoundTrip(t *testing.T) {
	v, _ := NewVAPID()
	raw, err := v.PrivateBytes()
	if err != nil {
		t.Fatal(err)
	}
	w, err := ParseVAPID(raw)
	if err != nil {
		t.Fatal(err)
	}
	if w.PublicKey() != v.PublicKey() || len(dec(t, v.PublicKey())) != 65 {
		t.Fatal("public key changed")
	}
	if _, err := ParseVAPID([]byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestPost(t *testing.T) {
	ua, a, k := newSub(t)
	v, _ := NewVAPID()
	var status = http.StatusCreated
	var got http.Header
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		body = make([]byte, 5000)
		n, _ := r.Body.Read(body)
		body = body[:n]
		w.WriteHeader(status)
	}))
	defer srv.Close()
	m := Message{Payload: []byte(`{"title":"t","body":"b","url":"/chores/1"}`), TTL: time.Hour, Urgency: UrgencyHigh}
	if err := Post(context.Background(), srv.Client(), v, "https://x.example", srv.URL+"/push/abc", k, m, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got.Get("TTL") != "3600" || got.Get("Urgency") != "high" || got.Get("Content-Encoding") != "aes128gcm" ||
		!strings.HasPrefix(got.Get("Authorization"), "vapid t=") {
		t.Fatalf("headers %v", got)
	}
	if string(decrypt(t, body, ua, a)) != string(m.Payload) {
		t.Fatal("payload mismatch")
	}
	for code, want := range map[int]error{404: ErrGone, 410: ErrGone, 413: ErrRejected, 500: ErrRejected} {
		status = code
		err := Post(context.Background(), srv.Client(), v, "https://x.example", srv.URL+"/push/secret-path", k, m, time.Now())
		if !errors.Is(err, want) || strings.Contains(err.Error(), "secret-path") {
			t.Errorf("%d: %v", code, err)
		}
	}
}
