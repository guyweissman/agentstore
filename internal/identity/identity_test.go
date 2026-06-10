package identity_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/guyweissman/agentstore/internal/identity"
)

// writeTestKeypair generates an ed25519 keypair and writes it to disk in
// OpenSSH format (private + .pub), returning the private key path.
func writeTestKeypair(t *testing.T, dir string) (privPath, pubLine string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	// Marshal private key to OpenSSH PEM.
	pemBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	privPath = filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(privPath, pem.EncodeToMemory(pemBlock), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}

	// Marshal public key to authorized-keys line.
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("new public key: %v", err)
	}
	pubLine = string(ssh.MarshalAuthorizedKey(sshPub))
	if err := os.WriteFile(privPath+".pub", []byte(pubLine), 0o644); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	return privPath, pubLine
}

func TestSignVerifyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	privPath, pubLine := writeTestKeypair(t, dir)

	priv, err := identity.LoadPrivateKey(privPath)
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	pub, err := identity.ParsePublicKey(pubLine)
	if err != nil {
		t.Fatalf("ParsePublicKey: %v", err)
	}

	preimage := []byte("agentstore-request-v1\nsome canonical bytes")
	sig := identity.Sign(priv, preimage)

	if !identity.Verify(pub, preimage, sig) {
		t.Error("valid signature failed to verify")
	}
	// Tampered preimage must fail.
	if identity.Verify(pub, []byte("different bytes"), sig) {
		t.Error("signature verified against wrong preimage")
	}
}

func TestReadPublicKeyFile(t *testing.T) {
	dir := t.TempDir()
	privPath, pubLine := writeTestKeypair(t, dir)

	got, err := identity.ReadPublicKeyFile(privPath + ".pub")
	if err != nil {
		t.Fatalf("ReadPublicKeyFile: %v", err)
	}
	// The canonical line should parse to the same key.
	if _, err := identity.ParsePublicKey(got); err != nil {
		t.Errorf("read public key does not parse: %v", err)
	}
	_ = pubLine
}

func TestPrivateKeyPathFromPublic(t *testing.T) {
	if got := identity.PrivateKeyPathFromPublic("/x/id_ed25519.pub"); got != "/x/id_ed25519" {
		t.Errorf("got %q", got)
	}
	if got := identity.PrivateKeyPathFromPublic("/x/id_ed25519"); got != "/x/id_ed25519" {
		t.Errorf("got %q (no .pub suffix should be unchanged)", got)
	}
}
