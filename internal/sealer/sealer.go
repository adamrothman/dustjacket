// Package sealer encrypts each person's Hardcover key before it is
// stored, bound to their Hardcover user ID: a ciphertext moved onto
// another person's record does not open. On Lambda the work is done by
// KMS; tests and local runs use an in-process AES-GCM key. Every sealed
// value records the key that sealed it, so the key can be replaced without
// breaking what is already stored.
package sealer

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// ErrOpen is a ciphertext that does not open for the user given.
var ErrOpen = errors.New("sealer: cannot open")

// Sealed is a ciphertext and the ID of the key that sealed it.
type Sealed struct {
	Ciphertext []byte
	KeyID      string
}

type Sealer interface {
	Seal(ctx context.Context, userID string, plaintext []byte) (Sealed, error)
	// Open opens s, and reports it stale when a previous key sealed it:
	// the caller should seal the plaintext again under the current key.
	Open(ctx context.Context, userID string, s Sealed) (plaintext []byte, stale bool, err error)
}

// Local seals with AES-256-GCM under a random key made when it is
// created, with the user ID as additional authenticated data. Anything
// it sealed is unreadable once the process exits.
type Local struct {
	id   string
	aead cipher.AEAD
}

func NewLocal() *Local {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return &Local{id: "local:" + hex.EncodeToString(key[:4]), aead: aead}
}

func (l *Local) Seal(_ context.Context, userID string, plaintext []byte) (Sealed, error) {
	nonce := make([]byte, l.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Sealed{}, err
	}
	return Sealed{Ciphertext: l.aead.Seal(nonce, nonce, plaintext, []byte(userID)), KeyID: l.id}, nil
}

func (l *Local) Open(_ context.Context, userID string, s Sealed) ([]byte, bool, error) {
	n := l.aead.NonceSize()
	if s.KeyID != l.id || len(s.Ciphertext) < n {
		return nil, false, ErrOpen
	}
	plaintext, err := l.aead.Open(nil, s.Ciphertext[:n], s.Ciphertext[n:], []byte(userID))
	if err != nil {
		return nil, false, ErrOpen
	}
	return plaintext, false, nil
}

// KMSAPI is the part of the KMS client KMS uses.
type KMSAPI interface {
	Encrypt(ctx context.Context, in *kms.EncryptInput, opts ...func(*kms.Options)) (*kms.EncryptOutput, error)
	Decrypt(ctx context.Context, in *kms.DecryptInput, opts ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

// KMS seals with a customer-managed KMS key, using the user ID as
// encryption context, and opens with the key that sealed: the current one,
// or one of the previous keys it still accepts. Key IDs are key ARNs, the
// form KMS reports the sealing key in.
type KMS struct {
	Client   KMSAPI
	KeyID    string
	Previous []string
}

func encryptionContext(userID string) map[string]string {
	return map[string]string{"hardcover_user_id": userID}
}

func (k *KMS) Seal(ctx context.Context, userID string, plaintext []byte) (Sealed, error) {
	out, err := k.Client.Encrypt(ctx, &kms.EncryptInput{KeyId: &k.KeyID, Plaintext: plaintext, EncryptionContext: encryptionContext(userID)})
	if err != nil {
		return Sealed{}, err
	}
	// Open accepts only the configured ARNs; a value recorded under any
	// other name for the key would never open again.
	if got := aws.ToString(out.KeyId); got != k.KeyID {
		return Sealed{}, fmt.Errorf("sealer: KMS sealed with %q but the configured key is %q; configure the key ARN", got, k.KeyID)
	}
	return Sealed{Ciphertext: out.CiphertextBlob, KeyID: k.KeyID}, nil
}

func (k *KMS) Open(ctx context.Context, userID string, s Sealed) ([]byte, bool, error) {
	stale := s.KeyID != k.KeyID
	if stale && !slices.Contains(k.Previous, s.KeyID) {
		return nil, false, fmt.Errorf("sealer: sealed by %q, which is neither the current key nor a previous one", s.KeyID)
	}
	out, err := k.Client.Decrypt(ctx, &kms.DecryptInput{KeyId: &s.KeyID, CiphertextBlob: s.Ciphertext, EncryptionContext: encryptionContext(userID)})
	if err != nil {
		return nil, false, err
	}
	return out.Plaintext, stale, nil
}
