package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
)

func HashBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func HashFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return HashBytes(content), nil
}
