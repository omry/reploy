package deploy

import (
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
)

const IdentityPolicyLockSchemaV1 = "identity-policy-lock-v1"

// IdentityPolicyLockV1 binds portable intent to verified final-image accounts.
// It grants no runtime authority and contains no host mapping assignments.
type IdentityPolicyLockV1 struct {
	Schema   string                     `json:"schema"`
	Policy   blueprint.IdentityPolicyV1 `json:"policy"`
	Evidence ImageIdentityEvidenceV1    `json:"evidence"`
}

func ValidateIdentityPolicyLockV1(lock *IdentityPolicyLockV1, image providers.RealizedImageV1, platform blueprint.Platform) error {
	if lock == nil {
		return nil
	}
	if lock.Schema != IdentityPolicyLockSchemaV1 {
		return fmt.Errorf("identity policy lock has unsupported schema")
	}
	return ValidateImageIdentityEvidenceV1(lock.Evidence, &lock.Policy, image, platform)
}
