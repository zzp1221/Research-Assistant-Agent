package tools

import (
	"research-assistant-backend/internal/modelprofile"
)

// sealSecret encrypts a tool/MCP secret at rest. The scope binds it to the
// owning record (tool id or server id) and owner.
func sealSecret(value, scopeID, ownerID string) (string, error) {
	return modelprofile.SealSecret(value, scopeID, ownerID, 1)
}

func openSecret(value, scopeID, ownerID string) (string, error) {
	return modelprofile.OpenSecret(value, scopeID, ownerID, 1)
}

// SealSecretValue is the exported form used by the API when it persists a
// tool/MCP secret. The scope binds the ciphertext to the owning record.
func SealSecretValue(value, scopeID, ownerID string) (string, error) {
	return sealSecret(value, scopeID, ownerID)
}
