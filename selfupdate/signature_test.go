package selfupdate

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestVerifySums(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	raw := []byte("aaaa  artex-1.0.0-linux-amd64.zip\n")
	sig := ed25519.Sign(priv, raw)

	old := UpdatePublicKey
	t.Cleanup(func() { UpdatePublicKey = old })

	UpdatePublicKey = ""
	if err := verifySums(raw, []byte(hex.EncodeToString(sig))); err == nil {
		t.Fatal("must refuse when no public key is embedded")
	}

	UpdatePublicKey = hex.EncodeToString(pub)
	if err := verifySums(raw, []byte(hex.EncodeToString(sig))); err != nil {
		t.Fatalf("hex sig: %v", err)
	}
	if err := verifySums(raw, []byte(base64.StdEncoding.EncodeToString(sig)+"\n")); err != nil {
		t.Fatalf("base64 sig: %v", err)
	}
	if err := verifySums(append(raw, 'x'), []byte(hex.EncodeToString(sig))); err == nil {
		t.Fatal("tampered checksum list must fail")
	}
	otherPub, _, _ := ed25519.GenerateKey(nil)
	UpdatePublicKey = hex.EncodeToString(otherPub)
	if err := verifySums(raw, []byte(hex.EncodeToString(sig))); err == nil {
		t.Fatal("wrong key must fail")
	}
	if err := verifySums(raw, []byte("not a signature")); err == nil {
		t.Fatal("garbage must fail")
	}
}

func TestRepoIsNotUpstream(t *testing.T) {
	if Repo == "Autumn-27/artex" {
		t.Fatal("self-update must not default to the upstream repository")
	}
}
