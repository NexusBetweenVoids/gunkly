package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"
)

// softKey is a software WebAuthn authenticator for tests: a resident ES256
// credential that signs like a platform authenticator with user verification.
// "none" attestation, so the registration needs no certificate chain.
type softKey struct {
	t       *testing.T
	priv    *ecdsa.PrivateKey
	id      []byte
	origin  string
	rpID    string
	handle  []byte
	counter uint32
	flags   byte // flags of an assertion; default user present + user verified
}

const (
	flagUP = 0x01
	flagUV = 0x04
	flagAT = 0x40
)

func newSoftKey(t *testing.T, origin, rpID string) *softKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	return &softKey{t: t, priv: priv, id: id, origin: origin, rpID: rpID, flags: flagUP | flagUV}
}

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (k *softKey) clientData(typ, challenge string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": k.origin, "crossOrigin": false})
	return b
}

// cborBytes encodes a CBOR byte string (major type 2).
func cborBytes(b []byte) []byte {
	var h []byte
	switch n := len(b); {
	case n < 24:
		h = []byte{0x40 | byte(n)}
	case n < 256:
		h = []byte{0x58, byte(n)}
	default:
		h = []byte{0x59, byte(n >> 8), byte(n)}
	}
	return append(h, b...)
}

// createCredential answers a creation options response (the body of POST
// /account/passkeys/options) with a registration credential.
func (k *softKey) createCredential(options any) map[string]any {
	k.t.Helper()
	pk := field(field(options, "options"), "publicKey")
	challenge, _ := field(pk, "challenge").(string)
	k.handle, _ = base64.RawURLEncoding.DecodeString(field(field(pk, "user"), "id").(string))
	rpHash := sha256.Sum256([]byte(k.rpID))
	pub, err := k.priv.PublicKey.Bytes() // 0x04 || X || Y
	if err != nil {
		k.t.Fatal(err)
	}
	x, y := pub[1:33], pub[33:65]
	cose := []byte{0xA5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21}
	cose = append(cose, cborBytes(x)...)
	cose = append(cose, 0x22)
	cose = append(cose, cborBytes(y)...)
	auth := append([]byte{}, rpHash[:]...)
	auth = append(auth, flagUP|flagUV|flagAT, 0, 0, 0, byte(k.counter))
	auth = append(auth, make([]byte, 16)...) // AAGUID: all zero
	auth = binary.BigEndian.AppendUint16(auth, uint16(len(k.id)))
	auth = append(auth, k.id...)
	auth = append(auth, cose...)
	att := []byte{0xA3, 0x63, 'f', 'm', 't', 0x64, 'n', 'o', 'n', 'e', 0x67, 'a', 't', 't', 'S', 't', 'm', 't', 0xA0,
		0x68, 'a', 'u', 't', 'h', 'D', 'a', 't', 'a'}
	att = append(att, cborBytes(auth)...)
	return map[string]any{"id": b64u(k.id), "rawId": b64u(k.id), "type": "public-key", "response": map[string]any{
		"clientDataJSON": b64u(k.clientData("webauthn.create", challenge)), "attestationObject": b64u(att),
		"transports": []string{"internal"}}}
}

// assertion answers a request options response (the body of one of the
// "options" calls) with the next signed assertion, counting up.
func (k *softKey) assertion(options any) map[string]any {
	k.t.Helper()
	challenge, _ := field(field(field(options, "options"), "publicKey"), "challenge").(string)
	k.counter++
	return k.assertionFor(challenge, k.counter)
}

// assertionFor signs an assertion with an explicit signature counter.
func (k *softKey) assertionFor(challenge string, counter uint32) map[string]any {
	k.t.Helper()
	rpHash := sha256.Sum256([]byte(k.rpID))
	auth := append([]byte{}, rpHash[:]...)
	auth = append(auth, k.flags)
	auth = binary.BigEndian.AppendUint32(auth, counter)
	cd := k.clientData("webauthn.get", challenge)
	cdHash := sha256.Sum256(cd)
	sum := sha256.Sum256(append(append([]byte{}, auth...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, sum[:])
	if err != nil {
		k.t.Fatal(err)
	}
	return map[string]any{"id": b64u(k.id), "rawId": b64u(k.id), "type": "public-key", "response": map[string]any{
		"clientDataJSON": b64u(cd), "authenticatorData": b64u(auth), "signature": b64u(sig), "userHandle": b64u(k.handle)}}
}
