package deploy

import (
	"fmt"
	"strings"
	"sync"
)

const maxBootSessionIDLengthV1 = 128

var bootSessionCacheV1 struct {
	sync.Mutex
	value string
}

// CurrentBootSessionIDV1 returns an opaque host execution-session identity.
// It remains stable across Docker daemon restarts and suspend/resume, but
// changes when the host boots a new operating-system session.
func CurrentBootSessionIDV1() (string, error) {
	bootSessionCacheV1.Lock()
	defer bootSessionCacheV1.Unlock()
	if bootSessionCacheV1.value != "" {
		return bootSessionCacheV1.value, nil
	}
	value, err := currentBootSessionIDV1()
	if err != nil {
		return "", fmt.Errorf("obtain current boot-session identity: %w", err)
	}
	value = strings.TrimSpace(value)
	if err := validateBootSessionIDV1(value); err != nil {
		return "", fmt.Errorf("obtain current boot-session identity: %w", err)
	}
	bootSessionCacheV1.value = value
	return value, nil
}

func validateBootSessionIDV1(value string) error {
	if value == "" {
		return fmt.Errorf("boot-session identity is empty")
	}
	if len(value) > maxBootSessionIDLengthV1 || !safeRecoveryIdentity(value) {
		return fmt.Errorf("boot-session identity must be safe text no longer than %d bytes", maxBootSessionIDLengthV1)
	}
	return nil
}
