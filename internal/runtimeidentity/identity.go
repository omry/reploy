// Package runtimeidentity defines the portable container-local identity
// contract shared by blueprint resolution, runtime planning, and controlled
// sessions.
package runtimeidentity

import (
	"fmt"
	"strconv"
)

// IdentityV1 is the canonical, target-independent form of one container-local
// runtime identity. Numeric IDs use decimal strings so the value can be used
// directly in canonical JSON records.
type IdentityV1 struct {
	Username          string   `json:"username"`
	UID               string   `json:"uid"`
	GID               string   `json:"gid"`
	SupplementaryGIDs []string `json:"supplementary_gids"`
}

// ValidateUserName applies Reploy's portable container-local user-name
// grammar. It does not decide whether the selected identity may be root.
func ValidateUserName(value string) error {
	if value == "" || len(value) > 32 {
		return fmt.Errorf("must be a nonempty portable Unix user name no longer than 32 bytes")
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character == '_' && index == 0 ||
			index > 0 && (character >= '0' && character <= '9' || character == '_' || character == '-') {
			continue
		}
		return fmt.Errorf("must be a portable lowercase Unix user name")
	}
	return nil
}

// ValidateIdentityV1 applies the common numeric and root-group invariants for
// application runtime identities.
func ValidateIdentityV1(identity IdentityV1) error {
	if err := ValidateUserName(identity.Username); err != nil {
		return fmt.Errorf("runtime username %q %w", identity.Username, err)
	}
	uid, ok := canonicalUnsignedV1(identity.UID)
	if !ok {
		return fmt.Errorf("runtime UID must use a canonical unsigned 32-bit decimal string other than 4294967295")
	}
	primaryGID, ok := canonicalUnsignedV1(identity.GID)
	if !ok {
		return fmt.Errorf("runtime GID must use a canonical unsigned 32-bit decimal string other than 4294967295")
	}
	if uid == 0 && identity.Username != "root" {
		return fmt.Errorf("root runtime identity must use the username root")
	}
	if uid != 0 && identity.Username == "root" {
		return fmt.Errorf("non-root runtime identity must not use the username root")
	}
	if uid != 0 && primaryGID == 0 {
		return fmt.Errorf("non-root runtime identity must not use the root group")
	}
	if identity.SupplementaryGIDs == nil {
		return fmt.Errorf("runtime supplementary GIDs must use an array")
	}
	var previous uint64
	for index, value := range identity.SupplementaryGIDs {
		gid, ok := canonicalUnsignedV1(value)
		if !ok {
			return fmt.Errorf("supplementary GID %q must use a canonical unsigned 32-bit decimal string other than 4294967295", value)
		}
		if gid == primaryGID {
			return fmt.Errorf("runtime supplementary GIDs must exclude the primary GID")
		}
		if uid != 0 && gid == 0 {
			return fmt.Errorf("non-root runtime identity must not include the root group")
		}
		if index > 0 && previous >= gid {
			return fmt.Errorf("runtime supplementary GIDs must be unique, sorted numerically, and exclude the primary GID")
		}
		previous = gid
	}
	return nil
}

func canonicalUnsignedV1(value string) (uint64, bool) {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return 0, false
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	// POSIX credential-changing calls reserve the all-ones uid_t/gid_t value
	// as "leave unchanged". It must never become a planned runtime identity.
	const credentialUnchangedSentinelV1 = uint64(1<<32 - 1)
	return parsed, err == nil && parsed != credentialUnchangedSentinelV1
}
