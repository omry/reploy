package dockerdeploy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

const identityHashLength = 8

func installedInstanceID(service string, target string) (string, error) {
	hash, err := pathIdentityHash(target)
	if err != nil {
		return "", err
	}
	return dockerNameSlug(service, "service") + "-" + hash, nil
}

func pathIdentityHash(path string) (string, error) {
	canonical, err := canonicalIdentityPath(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])[:identityHashLength], nil
}

func canonicalIdentityPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("identity path must not be empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(absolute)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(resolved), nil
	}
	missing := []string{}
	probe := clean
	for {
		if resolved, err := filepath.EvalSymlinks(probe); err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return clean, nil
		}
		missing = append(missing, filepath.Base(probe))
		probe = parent
	}
}

func dockerNameSlug(value string, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if valid {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return fallback
	}
	return result
}
