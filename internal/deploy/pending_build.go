package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	PendingBuildSchemaV1 = "pending-build-v1"

	PendingBuildPhaseValidated         = "validated"
	PendingBuildPhaseGenerationCreated = "generation-created"
	PendingBuildPhaseLockPublished     = "lock-published"
	PendingBuildPhaseStateCommitted    = "state-committed"
	PendingBuildPhaseCleanup           = "cleanup"

	CleanupKindContainer               = "container"
	CleanupKindMount                   = "mount"
	CleanupKindTemporaryFile           = "temporary-file"
	CleanupKindTemporaryImageReference = "temporary-image-reference"
	CleanupKindGenerationReference     = "generation-reference"
)

type EnvironmentGenerationState struct {
	Reference           string             `json:"reference"`
	ImageDigest         canonical.Digest   `json:"image_digest"`
	RootFSSubject       canonical.Digest   `json:"rootfs_subject"`
	BuildLockDigest     canonical.Digest   `json:"build_lock_digest"`
	Platform            blueprint.Platform `json:"platform"`
	RuntimePolicyDigest canonical.Digest   `json:"runtime_policy_digest"`
}

type PendingBuildV1 struct {
	Schema    string                      `json:"schema"`
	Phase     string                      `json:"phase"`
	Old       *EnvironmentGenerationState `json:"old"`
	Candidate PendingCandidateV1          `json:"candidate"`
	Cleanup   []CleanupItemV1             `json:"cleanup"`
	// OldReferences survives pruning the retired owner's build lock.
	OldReferences []OwnedImageReferenceV1 `json:"old_references,omitempty"`
}

// OwnedImageReferenceV1 retains exact image identity and one owner reference.
type OwnedImageReferenceV1 struct {
	Reference string                    `json:"reference"`
	Image     providers.RealizedImageV1 `json:"image"`
}

type PendingCandidateV1 struct {
	TemporaryReference  string                         `json:"temporary_reference"`
	GenerationReference string                         `json:"generation_reference"`
	Image               providers.RealizedImageV1      `json:"image"`
	BuildLockDigest     canonical.Digest               `json:"build_lock_digest"`
	StoreObjects        []providerstore.StoreObjectRef `json:"store_objects"`
	// Owner and Companion are present together only for a portable candidate.
	Owner     *EnvironmentGenerationState `json:"owner,omitempty"`
	Companion *OwnedImageReferenceV1      `json:"companion,omitempty"`
}

type CleanupItemV1 struct {
	Kind     string `json:"kind"`
	Identity string `json:"identity"`
}

func ValidateEnvironmentGenerationState(generation EnvironmentGenerationState) error {
	if !safeRecoveryIdentity(generation.Reference) {
		return fmt.Errorf("environment generation reference must be nonempty safe text")
	}
	if err := generation.ImageDigest.Validate(); err != nil {
		return fmt.Errorf("environment generation image digest: %w", err)
	}
	if err := generation.RootFSSubject.Validate(); err != nil {
		return fmt.Errorf("environment generation rootfs subject: %w", err)
	}
	if err := generation.BuildLockDigest.Validate(); err != nil {
		return fmt.Errorf("environment generation build lock digest: %w", err)
	}
	if err := generation.Platform.Validate(); err != nil {
		return fmt.Errorf("environment generation platform: %w", err)
	}
	if err := generation.RuntimePolicyDigest.Validate(); err != nil {
		return fmt.Errorf("environment generation runtime policy digest: %w", err)
	}
	return nil
}

func ValidatePendingBuild(record PendingBuildV1) error {
	if record.Schema != PendingBuildSchemaV1 {
		return fmt.Errorf("pending build schema must be %q", PendingBuildSchemaV1)
	}
	switch record.Phase {
	case PendingBuildPhaseValidated, PendingBuildPhaseGenerationCreated, PendingBuildPhaseLockPublished, PendingBuildPhaseStateCommitted, PendingBuildPhaseCleanup:
	default:
		return fmt.Errorf("pending build phase %q is unsupported", record.Phase)
	}
	if record.Old != nil {
		if err := ValidateEnvironmentGenerationState(*record.Old); err != nil {
			return fmt.Errorf("pending build old generation: %w", err)
		}
	}
	if err := validatePendingCandidate(record.Candidate); err != nil {
		return err
	}
	if len(record.OldReferences) != 0 {
		if record.Old == nil || len(record.OldReferences) > 2 {
			return fmt.Errorf("pending old references require one old owner and its primary/optional companion")
		}
		primary := record.OldReferences[0]
		if primary.Reference != record.Old.Reference || primary.Image.Digest != record.Old.ImageDigest || primary.Image.RootFSSubject != record.Old.RootFSSubject {
			return fmt.Errorf("pending old primary reference does not match its owner")
		}
		for index, pair := range record.OldReferences {
			if !safeRecoveryIdentity(pair.Reference) {
				return fmt.Errorf("pending old reference must be safe text")
			}
			if err := pair.Image.Validate(); err != nil {
				return fmt.Errorf("pending old image: %w", err)
			}
			if pair.Reference == record.Candidate.GenerationReference || pair.Reference == record.Candidate.TemporaryReference || (record.Candidate.Companion != nil && pair.Reference == record.Candidate.Companion.Reference) || (index > 0 && pair.Reference == primary.Reference) {
				return fmt.Errorf("pending reference roles must be distinct")
			}
		}
	}
	if record.Old != nil && (record.Old.Reference == record.Candidate.TemporaryReference || record.Old.Reference == record.Candidate.GenerationReference) {
		return fmt.Errorf("pending build candidate references must differ from the old generation")
	}
	if record.Cleanup == nil {
		return fmt.Errorf("pending build cleanup must use an array")
	}
	for index, item := range record.Cleanup {
		if err := validateCleanupItem(item); err != nil {
			return fmt.Errorf("pending build cleanup item %d: %w", index, err)
		}
		if index > 0 && compareCleanupItems(record.Cleanup[index-1], item) >= 0 {
			return fmt.Errorf("pending build cleanup items must be unique and sorted by kind and identity")
		}
	}
	return nil
}

func EncodePendingBuild(record PendingBuildV1) ([]byte, error) {
	if err := ValidatePendingBuild(record); err != nil {
		return nil, fmt.Errorf("encode pending build: %w", err)
	}
	content, err := canonical.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("encode pending build: %w", err)
	}
	return content, nil
}

func DecodePendingBuild(content []byte) (PendingBuildV1, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var record PendingBuildV1
	if err := decoder.Decode(&record); err != nil {
		return PendingBuildV1{}, fmt.Errorf("decode pending build: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return PendingBuildV1{}, fmt.Errorf("pending build contains trailing JSON")
		}
		return PendingBuildV1{}, fmt.Errorf("decode pending build trailer: %w", err)
	}
	if err := ValidatePendingBuild(record); err != nil {
		return PendingBuildV1{}, fmt.Errorf("validate pending build: %w", err)
	}
	canonicalContent, err := canonical.Marshal(record)
	if err != nil {
		return PendingBuildV1{}, err
	}
	if !bytes.Equal(content, canonicalContent) {
		return PendingBuildV1{}, fmt.Errorf("pending build is not canonical JSON")
	}
	return record, nil
}

func validatePendingCandidate(candidate PendingCandidateV1) error {
	if !safeRecoveryIdentity(candidate.TemporaryReference) || !safeRecoveryIdentity(candidate.GenerationReference) {
		return fmt.Errorf("pending build candidate references must be nonempty safe text")
	}
	if candidate.TemporaryReference == candidate.GenerationReference {
		return fmt.Errorf("pending build candidate temporary and generation references must differ")
	}
	if err := candidate.Image.Validate(); err != nil {
		return fmt.Errorf("pending build candidate image: %w", err)
	}
	if err := candidate.BuildLockDigest.Validate(); err != nil {
		return fmt.Errorf("pending build candidate lock digest: %w", err)
	}
	if (candidate.Owner == nil) != (candidate.Companion == nil) {
		return fmt.Errorf("pending portable candidate requires both owner and companion")
	}
	if candidate.Owner != nil {
		owner := candidate.Owner
		if err := ValidateEnvironmentGenerationState(*owner); err != nil {
			return fmt.Errorf("pending candidate owner: %w", err)
		}
		if owner.Reference != candidate.GenerationReference || owner.ImageDigest != candidate.Image.Digest || owner.RootFSSubject != candidate.Image.RootFSSubject || owner.BuildLockDigest != candidate.BuildLockDigest {
			return fmt.Errorf("pending candidate owner does not match candidate identity")
		}
		if err := candidate.Companion.Image.Validate(); err != nil {
			return fmt.Errorf("pending candidate companion image: %w", err)
		}
		if !safeRecoveryIdentity(candidate.Companion.Reference) || candidate.Companion.Reference == candidate.GenerationReference || candidate.Companion.Reference == candidate.TemporaryReference {
			return fmt.Errorf("pending candidate companion must have a distinct safe reference")
		}
	}
	if candidate.StoreObjects == nil {
		return fmt.Errorf("pending build candidate store objects must use an array")
	}
	for index, reference := range candidate.StoreObjects {
		if err := reference.Validate(); err != nil {
			return fmt.Errorf("pending build candidate store object %d: %w", index, err)
		}
		if index > 0 && compareStoreObjectReferences(candidate.StoreObjects[index-1], reference) >= 0 {
			return fmt.Errorf("pending build candidate store objects must be unique and sorted by kind and digest")
		}
	}
	return nil
}

func validateCleanupItem(item CleanupItemV1) error {
	switch item.Kind {
	case CleanupKindContainer, CleanupKindMount, CleanupKindTemporaryFile, CleanupKindTemporaryImageReference, CleanupKindGenerationReference:
	default:
		return fmt.Errorf("cleanup kind %q is unsupported", item.Kind)
	}
	if !safeRecoveryIdentity(item.Identity) {
		return fmt.Errorf("cleanup identity must be nonempty safe text")
	}
	return nil
}

func compareStoreObjectReferences(left providerstore.StoreObjectRef, right providerstore.StoreObjectRef) int {
	if left.Kind != right.Kind {
		return strings.Compare(left.Kind, right.Kind)
	}
	return strings.Compare(string(left.Digest), string(right.Digest))
}

func compareCleanupItems(left CleanupItemV1, right CleanupItemV1) int {
	if left.Kind != right.Kind {
		return strings.Compare(left.Kind, right.Kind)
	}
	return strings.Compare(left.Identity, right.Identity)
}

func safeRecoveryIdentity(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.HasPrefix(value, "-") || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}
