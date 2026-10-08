package sealer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

func TestLocalRoundTripBoundToUserAndKey(t *testing.T) {
	ctx := context.Background()
	l := NewLocal()
	s, err := l.Seal(ctx, "42", []byte("secret-key"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(s.Ciphertext, []byte("secret-key")) || s.KeyID == "" {
		t.Fatalf("sealed: %+v", s)
	}
	pt, stale, err := l.Open(ctx, "42", s)
	if err != nil || string(pt) != "secret-key" || stale {
		t.Fatalf("open: %q %v %v", pt, stale, err)
	}
	if _, _, err := l.Open(ctx, "43", s); !errors.Is(err, ErrOpen) {
		t.Fatalf("opened under another user: %v", err)
	}
	if _, _, err := l.Open(ctx, "42", Sealed{Ciphertext: s.Ciphertext[:5], KeyID: s.KeyID}); !errors.Is(err, ErrOpen) {
		t.Fatalf("opened a truncated ciphertext: %v", err)
	}
	if _, _, err := NewLocal().Open(ctx, "42", s); !errors.Is(err, ErrOpen) {
		t.Fatal("another Local opened this one's ciphertext")
	}
}

// fakeKMS "encrypts" into JSON carrying the key ID and context, answers
// with the key it used, as KMS does, and refuses to decrypt under a
// different key or context. It counts decrypts.
type fakeKMS struct{ decrypts int }

type fakeBlob struct {
	KeyID   string
	Context map[string]string
	Data    []byte
}

func (*fakeKMS) Encrypt(_ context.Context, in *kms.EncryptInput, _ ...func(*kms.Options)) (*kms.EncryptOutput, error) {
	b, _ := json.Marshal(fakeBlob{KeyID: *in.KeyId, Context: in.EncryptionContext, Data: in.Plaintext})
	return &kms.EncryptOutput{CiphertextBlob: b, KeyId: in.KeyId}, nil
}

func (f *fakeKMS) Decrypt(_ context.Context, in *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	f.decrypts++
	var b fakeBlob
	if err := json.Unmarshal(in.CiphertextBlob, &b); err != nil {
		return nil, err
	}
	if in.KeyId == nil || b.KeyID != *in.KeyId {
		return nil, errors.New("IncorrectKeyException")
	}
	if !maps.Equal(b.Context, in.EncryptionContext) {
		return nil, errors.New("InvalidCiphertextException")
	}
	return &kms.DecryptOutput{Plaintext: b.Data, KeyId: in.KeyId}, nil
}

const (
	oldKey = "arn:aws:kms:us-west-2:1:key/old"
	newKey = "arn:aws:kms:us-west-2:1:key/new"
)

func TestKMSRecordsTheSealingKey(t *testing.T) {
	ctx := context.Background()
	k := &KMS{Client: &fakeKMS{}, KeyID: newKey}
	s, err := k.Seal(ctx, "42", []byte("secret-key"))
	if err != nil {
		t.Fatal(err)
	}
	var b fakeBlob
	if err := json.Unmarshal(s.Ciphertext, &b); err != nil {
		t.Fatal(err)
	}
	if s.KeyID != newKey || b.KeyID != newKey || b.Context["hardcover_user_id"] != "42" {
		t.Fatalf("sealed %+v with %+v", s, b)
	}
	pt, stale, err := k.Open(ctx, "42", s)
	if err != nil || string(pt) != "secret-key" || stale {
		t.Fatalf("open: %q %v %v", pt, stale, err)
	}
	if _, _, err := k.Open(ctx, "43", s); err == nil {
		t.Fatal("opened under another user")
	}
}

// After a switch to a new key, what the old one sealed still opens, and
// says so, so it can be sealed again under the new key.
func TestKMSOpensWithPreviousKeys(t *testing.T) {
	ctx := context.Background()
	fake := &fakeKMS{}
	before := &KMS{Client: fake, KeyID: oldKey}
	s, err := before.Seal(ctx, "42", []byte("secret-key"))
	if err != nil {
		t.Fatal(err)
	}
	after := &KMS{Client: fake, KeyID: newKey, Previous: []string{oldKey}}
	pt, stale, err := after.Open(ctx, "42", s)
	if err != nil || string(pt) != "secret-key" || !stale {
		t.Fatalf("open with a previous key: %q %v %v", pt, stale, err)
	}

	// A key that is neither current nor previous is refused without asking KMS.
	fake.decrypts = 0
	retired := &KMS{Client: fake, KeyID: newKey}
	if _, _, err := retired.Open(ctx, "42", s); err == nil || fake.decrypts != 0 {
		t.Fatalf("retired key: %v, %d decrypts", err, fake.decrypts)
	}
}

// aliasKMS answers, as KMS does, with the ARN of the key behind whatever
// ID it was given.
type aliasKMS struct{ fakeKMS }

func (a *aliasKMS) Encrypt(ctx context.Context, in *kms.EncryptInput, opts ...func(*kms.Options)) (*kms.EncryptOutput, error) {
	out, err := a.fakeKMS.Encrypt(ctx, in, opts...)
	if err == nil {
		out.KeyId = aws.String(newKey)
	}
	return out, err
}

// Configured with anything but the key's own ARN, sealing would record a
// key that Open then refuses: refuse to seal instead.
func TestKMSRefusesToSealUnderAnotherName(t *testing.T) {
	k := &KMS{Client: &aliasKMS{}, KeyID: "arn:aws:kms:us-west-2:1:alias/dustjacket"}
	if _, err := k.Seal(context.Background(), "42", []byte("secret-key")); err == nil {
		t.Fatal("sealed under an alias")
	}
}
