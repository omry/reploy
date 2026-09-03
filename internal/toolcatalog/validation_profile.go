package toolcatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

// ValidationProfileDigestV1 validates a complete portable-tool validation
// profile and returns its canonical record identity.
func ValidationProfileDigestV1(profile ValidationProfileRecordV1) (canonical.Digest, error) {
	record := loadedRecordV1{
		ID: profile.ID, Schema: profile.Schema, Value: &profile,
	}
	if err := validateLoadedRecordV1(record); err != nil {
		return "", fmt.Errorf("validation profile: %w", err)
	}
	digest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, &profile)
	if err != nil {
		return "", fmt.Errorf("validation profile digest: %w", err)
	}
	return digest, nil
}

// DecodePortableToolValidationProfileV1 converts one canonical provider
// envelope back into the exact validation-profile record it was compiled
// from. It is the sole plan-to-executor conversion boundary: a scheduler
// decodes the profile carried by the plan rather than re-reading catalog
// state, so locked replay cannot pick up a moving definition.
//
// The reference is authoritative. Decoding is strict, the decoded record is
// revalidated, and its recomputed identity must equal the reference digest,
// so a substituted or mutated envelope fails before any probe runs.
func DecodePortableToolValidationProfileV1(
	reference providers.PortableToolRecordReferenceV1,
	data providers.CanonicalProviderData,
) (ValidationProfileRecordV1, error) {
	if data.Schema != ValidationProfileSchemaV1 {
		return ValidationProfileRecordV1{}, fmt.Errorf(
			"validation profile data must use schema %q", ValidationProfileSchemaV1,
		)
	}
	encoded, err := canonical.Marshal(data.Value)
	if err != nil {
		return ValidationProfileRecordV1{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var profile ValidationProfileRecordV1
	if err := decoder.Decode(&profile); err != nil {
		return ValidationProfileRecordV1{}, fmt.Errorf("decode validation profile data: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ValidationProfileRecordV1{}, fmt.Errorf("decode validation profile data trailer")
	}
	digest, err := ValidationProfileDigestV1(profile)
	if err != nil {
		return ValidationProfileRecordV1{}, err
	}
	if profile.ID != reference.ID {
		return ValidationProfileRecordV1{}, fmt.Errorf(
			"validation profile record %q does not match reference %q", profile.ID, reference.ID,
		)
	}
	if digest != reference.Digest {
		return ValidationProfileRecordV1{}, fmt.Errorf(
			"validation profile %q identity %s does not match reference %s", profile.ID, digest, reference.Digest,
		)
	}
	return profile, nil
}
