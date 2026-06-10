// Package identity handles ed25519 keypairs: loading private keys, parsing
// public keys, and signing/verifying request preimages. Keypairs are generated
// outside AgentStore with ssh-keygen; only the public key is ever sent to a server.
package identity

import (
	"crypto/ed25519"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

// LoadPrivateKey reads an OpenSSH-format ed25519 private key file.
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	raw, err := ssh.ParseRawPrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("parse private key %s: %w", path, err)
	}
	// ParseRawPrivateKey returns *ed25519.PrivateKey for ed25519 keys.
	switch k := raw.(type) {
	case *ed25519.PrivateKey:
		return *k, nil
	case ed25519.PrivateKey:
		return k, nil
	default:
		return nil, fmt.Errorf("key %s is not an ed25519 key (got %T)", path, raw)
	}
}

// ReadPublicKeyFile reads an OpenSSH .pub file and returns the canonical
// authorized-key line (without trailing newline).
func ReadPublicKeyFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read public key: %w", err)
	}
	// Validate it parses as an ed25519 key before returning the canonical line.
	if _, err := ParsePublicKey(string(data)); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// ParsePublicKey parses an OpenSSH authorized-key line into an ed25519 public key.
func ParsePublicKey(authorizedKey string) (ed25519.PublicKey, error) {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorizedKey))
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	cryptoPub, ok := pk.(ssh.CryptoPublicKey)
	if !ok {
		return nil, fmt.Errorf("public key does not expose a crypto key")
	}
	edPub, ok := cryptoPub.CryptoPublicKey().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not ed25519")
	}
	return edPub, nil
}

// Sign signs the preimage with the private key.
func Sign(priv ed25519.PrivateKey, preimage []byte) []byte {
	return ed25519.Sign(priv, preimage)
}

// Verify reports whether sig is a valid signature of preimage by pub.
func Verify(pub ed25519.PublicKey, preimage, sig []byte) bool {
	return ed25519.Verify(pub, preimage, sig)
}

// PrivateKeyPathFromPublic derives the conventional private key path from a
// public key path by stripping a trailing ".pub" (ssh-keygen's convention).
func PrivateKeyPathFromPublic(pubPath string) string {
	return strings.TrimSuffix(pubPath, ".pub")
}
