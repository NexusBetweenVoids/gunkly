package mail

import "encoding/json"

// Sealer encrypts and decrypts with a key ring (*auth.Keyring).
type Sealer interface {
	Encrypt(plaintext, ad []byte) string
	Decrypt(ciphertext string, ad []byte) ([]byte, error)
}

func sealAD(id string) []byte { return []byte("mail_outbox:" + id) }

// Seal encrypts m (recipient, subject and body, which can carry a code or a
// link token) for the outbox row id, so a database dump never shows them.
func Seal(k Sealer, id string, m Message) string {
	b, _ := json.Marshal(m) // three strings: cannot fail
	return k.Encrypt(b, sealAD(id))
}

// Open reverses Seal.
func Open(k Sealer, id, sealed string) (Message, error) {
	b, err := k.Decrypt(sealed, sealAD(id))
	if err != nil {
		return Message{}, err
	}
	var m Message
	return m, json.Unmarshal(b, &m)
}
