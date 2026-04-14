package crypto

import (
	"bytes"
	"testing"
)

func TestGenerateKeyPair_Unique(t *testing.T) {
	a1, b1, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	a2, b2, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if a1 == a2 || b1 == b2 {
		t.Fatal("two generated keypairs should differ")
	}
}

func TestEncryptor_DerivesPublicKey(t *testing.T) {
	priv, pub, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(priv)
	if err != nil {
		t.Fatal(err)
	}
	if e.PublicKey() != pub {
		t.Fatalf("derived pub %x != generated pub %x", e.PublicKey(), pub)
	}
}

func TestSealOpen_Roundtrip(t *testing.T) {
	aPriv, aPub, _ := GenerateKeyPair()
	bPriv, bPub, _ := GenerateKeyPair()

	alice, err := New(aPriv)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := New(bPriv)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("hello formidable")
	sealed, err := alice.Seal(bPub, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	got, err := bob.Open(aPub, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}

func TestSealOpen_WrongSenderFails(t *testing.T) {
	aPriv, _, _ := GenerateKeyPair()
	bPriv, bPub, _ := GenerateKeyPair()
	_, mallory, _ := GenerateKeyPair()

	alice, _ := New(aPriv)
	bob, _ := New(bPriv)

	sealed, err := alice.Seal(bPub, []byte("for bob"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := bob.Open(mallory, sealed); err == nil {
		t.Fatal("expected Open with wrong sender pub to fail")
	}
}

func TestSealOpen_TamperedFails(t *testing.T) {
	aPriv, aPub, _ := GenerateKeyPair()
	bPriv, bPub, _ := GenerateKeyPair()

	alice, _ := New(aPriv)
	bob, _ := New(bPriv)

	sealed, err := alice.Seal(bPub, []byte("pristine"))
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 0x01 // flip a bit

	if _, err := bob.Open(aPub, sealed); err == nil {
		t.Fatal("expected Open on tampered ciphertext to fail")
	}
}

func TestSelfSealRoundtrip(t *testing.T) {
	priv, _, _ := GenerateKeyPair()
	e, _ := New(priv)

	plaintext := []byte("at-rest token store")
	sealed, err := e.SealSelf(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.OpenSelf(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}

func TestSelfSeal_DifferentServerCannotOpen(t *testing.T) {
	priv1, _, _ := GenerateKeyPair()
	priv2, _, _ := GenerateKeyPair()
	server1, _ := New(priv1)
	server2, _ := New(priv2)

	sealed, _ := server1.SealSelf([]byte("secret"))
	if _, err := server2.OpenSelf(sealed); err == nil {
		t.Fatal("another server's SealSelf must not open with a different key")
	}
}

func TestStringAPI_Roundtrip(t *testing.T) {
	aPriv, aPub, _ := GenerateKeyPair()
	bPriv, bPub, _ := GenerateKeyPair()
	alice, _ := New(aPriv)
	bob, _ := New(bPriv)

	s, err := alice.SealString(bPub, []byte("over the wire"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := bob.OpenString(aPub, s)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "over the wire" {
		t.Fatalf("got %q", got)
	}
}

func TestDecodeKey_Roundtrip(t *testing.T) {
	_, pub, _ := GenerateKeyPair()
	encoded := pub.Encode()
	decoded, err := DecodeKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != pub {
		t.Fatal("encode/decode round-trip changed the key")
	}
}

func TestDecodeKey_BadInput(t *testing.T) {
	if _, err := DecodeKey("not-base64!!!"); err == nil {
		t.Fatal("expected error on bad base64")
	}
	if _, err := DecodeKey("c2hvcnQ="); err == nil {
		t.Fatal("expected error on wrong length")
	}
}

func TestOpen_ShortPayload(t *testing.T) {
	priv, _, _ := GenerateKeyPair()
	e, _ := New(priv)
	if _, err := e.OpenSelf([]byte("too short")); err == nil {
		t.Fatal("expected error on short payload")
	}
}
