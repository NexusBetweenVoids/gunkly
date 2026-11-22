package auth

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// solve is what a client does: try numbers until the hash matches.
func solve(t *testing.T, c PoWChallenge) PoWSolution {
	t.Helper()
	for n := 0; n <= c.MaxNumber; n++ {
		if powHash(c.Salt, n) == c.Challenge {
			return PoWSolution{Algorithm: c.Algorithm, Challenge: c.Challenge, Salt: c.Salt, Signature: c.Signature, Number: n}
		}
	}
	t.Fatal("no solution within maxnumber")
	return PoWSolution{}
}

func TestPoWRoundTrip(t *testing.T) {
	p := NewPoW([]byte(strings.Repeat("k", 32)), 2000)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	c := p.New(now)
	if c.Algorithm != "SHA-256" || c.MaxNumber != 2000 || len(c.Challenge) != 64 || len(c.Signature) != 64 {
		t.Fatalf("challenge: %+v", c)
	}
	s := solve(t, c)
	ch, exp, err := p.Verify(s, now.Add(time.Minute))
	if err != nil || ch != c.Challenge || !exp.Equal(now.Add(PoWTTL)) {
		t.Fatalf("verify: %q %v %v", ch, exp, err)
	}
	if _, _, err := p.Verify(s, now.Add(PoWTTL)); !errors.Is(err, ErrPoWExpired) {
		t.Fatalf("at expiry: %v", err)
	}
}

func TestPoWRejectsTampering(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	p := NewPoW(key, 2000)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	c := p.New(now)
	good := solve(t, c)

	wrong := good
	wrong.Number = (good.Number + 1) % 2001
	other := NewPoW([]byte(strings.Repeat("x", 32)), 2000)
	forged := good
	forged.Signature = strings.Repeat("0", 64)
	// An attacker who picks their own salt (far-future expiry) and solves it
	// still cannot sign it.
	own := PoWChallenge{Algorithm: "SHA-256", Salt: "aa?expires=99999999999&"}
	own.Challenge = powHash(own.Salt, 5)
	ownSol := PoWSolution{Algorithm: "SHA-256", Challenge: own.Challenge, Salt: own.Salt, Signature: strings.Repeat("a", 64), Number: 5}
	badAlg := good
	badAlg.Algorithm = "SHA-1"
	neg := good
	neg.Number = -1
	huge := good
	huge.Number = 2001
	short := good
	short.Challenge = "abc"

	for name, s := range map[string]PoWSolution{"wrong number": wrong, "forged signature": forged, "own salt": ownSol,
		"algorithm": badAlg, "negative": neg, "above max": huge, "short": short, "empty": {}} {
		if _, _, err := p.Verify(s, now); !errors.Is(err, ErrPoWInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, _, err := other.Verify(good, now); !errors.Is(err, ErrPoWInvalid) {
		t.Errorf("another key accepted the solution: %v", err)
	}
	// A salt whose expiry was edited breaks the hash.
	edit := good
	edit.Salt = strings.Replace(good.Salt, "expires=", "expires=9", 1)
	if _, _, err := p.Verify(edit, now); !errors.Is(err, ErrPoWInvalid) {
		t.Errorf("edited salt: %v", err)
	}
}

func TestPoWMaxNumberClamped(t *testing.T) {
	if got := NewPoW(nil, 1).maxNumber; got != MinPoWMaxNumber {
		t.Errorf("low: %d", got)
	}
	if got := NewPoW(nil, 1<<40).maxNumber; got != MaxPoWMaxNumber {
		t.Errorf("high: %d", got)
	}
}

func TestDeriveKeyIsPerLabelAndSecret(t *testing.T) {
	a, _ := NewKeyring(1, map[uint32]string{1: strings.Repeat("a", MinSecretLen)})
	b, _ := NewKeyring(1, map[uint32]string{1: strings.Repeat("b", MinSecretLen)})
	if string(a.DeriveKey("x")) == string(a.DeriveKey("y")) || string(a.DeriveKey("x")) == string(b.DeriveKey("x")) ||
		!bytes.Equal(a.DeriveKey("x"), a.DeriveKey("x")) || len(a.DeriveKey("x")) != 32 {
		t.Fatal("derived keys do not behave")
	}
}
