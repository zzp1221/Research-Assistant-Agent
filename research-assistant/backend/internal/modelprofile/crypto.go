package modelprofile

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
)

// EncryptionKeyEnv is the environment variable holding the profile encryption
// key. Any non-empty value is accepted; a base64-decoded 32-byte value is used
// verbatim, otherwise the value is hashed to 32 bytes with SHA-256.
const EncryptionKeyEnv = "MODEL_PROFILE_ENCRYPTION_KEY"

// cipherKey derives the AES-256 key from the configured secret. It returns
// ErrEncryptionUnavailable when no secret is configured so callers fail closed.
func cipherKey() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(EncryptionKeyEnv))
	if raw == "" {
		return nil, ErrEncryptionUnavailable
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	sum := sha256.Sum256([]byte(raw))
	return sum[:], nil
}

// EncryptionAvailable reports whether profiles can be persisted with a secret.
func EncryptionAvailable() bool {
	_, err := cipherKey()
	return err == nil
}

// aad binds a ciphertext to its profile identity and revision so it cannot be
// moved to another profile or replayed after a revision bump.
func aad(profileID, ownerID string, revision int) []byte {
	return []byte(fmt.Sprintf("%s|%s|%d", profileID, ownerID, revision))
}

// encryptSecret seals apiKey with AES-256-GCM.
func encryptSecret(apiKey, profileID, ownerID string, revision int) (string, error) {
	key, err := cipherKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(apiKey), aad(profileID, ownerID, revision))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptSecret opens a ciphertext produced by encryptSecret.
func decryptSecret(ciphertext, profileID, ownerID string, revision int) (string, error) {
	if strings.TrimSpace(ciphertext) == "" {
		return "", nil
	}
	key, err := cipherKey()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("decode profile secret: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("profile secret is truncated")
	}
	nonce, body := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, body, aad(profileID, ownerID, revision))
	if err != nil {
		return "", fmt.Errorf("decrypt profile secret: %w", err)
	}
	return string(plain), nil
}
