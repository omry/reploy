package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	PrefixValidationSchemaV1 = "prefix-validation-v1"
	ValidationLabelPrefix    = "io.reploy.validation."
	ValidationSchemaLabel    = ValidationLabelPrefix + "schema"
	ValidationSubjectLabel   = ValidationLabelPrefix + "subject"
	ValidationRecordLabel    = ValidationLabelPrefix + "record"
)

type PrefixValidationV1 struct {
	Schema         string                         `json:"schema"`
	SubjectRootFS  canonical.Digest               `json:"subject_rootfs"`
	Profiles       []providers.ValidationEvidence `json:"profiles"`
	RuntimePolicy  canonical.Digest               `json:"runtime_policy"`
	ExposedOutputs []providers.ExecutableEvidence `json:"exposed_outputs"`
}

func ValidatePrefixValidation(record PrefixValidationV1) error {
	if record.Schema != PrefixValidationSchemaV1 {
		return fmt.Errorf("prefix validation schema must be %q", PrefixValidationSchemaV1)
	}
	if err := record.SubjectRootFS.Validate(); err != nil {
		return fmt.Errorf("prefix validation rootfs subject: %w", err)
	}
	if record.Profiles == nil || record.ExposedOutputs == nil {
		return fmt.Errorf("prefix validation profiles and exposed outputs must use arrays")
	}
	for index, evidence := range record.Profiles {
		if err := evidence.Validate(); err != nil {
			return fmt.Errorf("prefix validation profile %d: %w", index, err)
		}
		if evidence.SubjectRootFS != record.SubjectRootFS {
			return fmt.Errorf("prefix validation profile %s binds a different rootfs subject", evidence.ProfileDigest)
		}
		if index > 0 && record.Profiles[index-1].ProfileDigest >= evidence.ProfileDigest {
			return fmt.Errorf("prefix validation profiles must be unique and sorted by profile digest")
		}
	}
	if err := record.RuntimePolicy.Validate(); err != nil {
		return fmt.Errorf("prefix validation runtime policy: %w", err)
	}
	for index, evidence := range record.ExposedOutputs {
		if err := providers.ValidateFinalExecutableEvidence(evidence); err != nil {
			return fmt.Errorf("prefix validation exposed output %d: %w", index, err)
		}
		if index > 0 && compareValidationOutputs(record.ExposedOutputs[index-1], evidence) >= 0 {
			return fmt.Errorf("prefix validation exposed outputs must be unique and sorted by qualified output")
		}
	}
	return nil
}

func PrefixValidationDigest(record PrefixValidationV1) (canonical.Digest, error) {
	if err := ValidatePrefixValidation(record); err != nil {
		return "", err
	}
	return canonical.Sum("prefix-validation", PrefixValidationSchemaV1, record)
}

func PublishPrefixValidation(ctx context.Context, store providerstore.Store, record PrefixValidationV1) (providerstore.StoreObjectRef, error) {
	content, reference, err := EncodePrefixValidation(record)
	if err != nil {
		return providerstore.StoreObjectRef{}, err
	}
	if err := store.PublishValidationRecord(ctx, reference, content); err != nil {
		return providerstore.StoreObjectRef{}, err
	}
	return reference, nil
}

func LoadPrefixValidation(store providerstore.Store, reference providerstore.StoreObjectRef) (PrefixValidationV1, error) {
	content, err := store.LoadValidationRecord(reference)
	if err != nil {
		return PrefixValidationV1{}, err
	}
	return DecodePrefixValidation(content, reference)
}

func EncodePrefixValidation(record PrefixValidationV1) ([]byte, providerstore.StoreObjectRef, error) {
	digest, err := PrefixValidationDigest(record)
	if err != nil {
		return nil, providerstore.StoreObjectRef{}, fmt.Errorf("encode prefix validation: %w", err)
	}
	content, err := canonical.Marshal(record)
	if err != nil {
		return nil, providerstore.StoreObjectRef{}, fmt.Errorf("encode prefix validation: %w", err)
	}
	return content, providerstore.StoreObjectRef{Kind: providerstore.ValidationRecordKind, Digest: digest}, nil
}

func DecodePrefixValidation(content []byte, reference providerstore.StoreObjectRef) (PrefixValidationV1, error) {
	if err := reference.Validate(); err != nil {
		return PrefixValidationV1{}, fmt.Errorf("prefix validation reference: %w", err)
	}
	if reference.Kind != providerstore.ValidationRecordKind {
		return PrefixValidationV1{}, fmt.Errorf("prefix validation reference kind must be %q", providerstore.ValidationRecordKind)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var record PrefixValidationV1
	if err := decoder.Decode(&record); err != nil {
		return PrefixValidationV1{}, fmt.Errorf("decode prefix validation: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return PrefixValidationV1{}, fmt.Errorf("prefix validation contains trailing JSON")
		}
		return PrefixValidationV1{}, fmt.Errorf("decode prefix validation trailer: %w", err)
	}
	digest, err := PrefixValidationDigest(record)
	if err != nil {
		return PrefixValidationV1{}, fmt.Errorf("validate prefix validation: %w", err)
	}
	if digest != reference.Digest {
		return PrefixValidationV1{}, fmt.Errorf("prefix validation digest %s does not match store reference %s", digest, reference.Digest)
	}
	canonicalContent, err := canonical.Marshal(record)
	if err != nil {
		return PrefixValidationV1{}, err
	}
	if !bytes.Equal(content, canonicalContent) {
		return PrefixValidationV1{}, fmt.Errorf("prefix validation is not canonical JSON")
	}
	return record, nil
}

func compareValidationOutputs(left providers.ExecutableEvidence, right providers.ExecutableEvidence) int {
	if left.Output.Component != right.Output.Component {
		return strings.Compare(left.Output.Component, right.Output.Component)
	}
	return strings.Compare(left.Output.Name, right.Output.Name)
}

func PrefixValidationLabels(subject canonical.Digest, reference providerstore.StoreObjectRef) ([]providers.ImageLabel, error) {
	if err := subject.Validate(); err != nil {
		return nil, fmt.Errorf("prefix validation label subject: %w", err)
	}
	if err := reference.Validate(); err != nil {
		return nil, fmt.Errorf("prefix validation label record: %w", err)
	}
	if reference.Kind != providerstore.ValidationRecordKind {
		return nil, fmt.Errorf("prefix validation label record kind must be %q", providerstore.ValidationRecordKind)
	}
	return []providers.ImageLabel{
		{Name: ValidationRecordLabel, Value: string(reference.Digest)},
		{Name: ValidationSchemaLabel, Value: PrefixValidationSchemaV1},
		{Name: ValidationSubjectLabel, Value: string(subject)},
	}, nil
}

func ValidatePrefixValidationLabels(labels map[string]string, image providers.RealizedImageV1, reference providerstore.StoreObjectRef) error {
	if err := image.Validate(); err != nil {
		return fmt.Errorf("prefix validation labeled image: %w", err)
	}
	expected, err := PrefixValidationLabels(image.RootFSSubject, reference)
	if err != nil {
		return err
	}
	expectedByName := make(map[string]string, len(expected))
	for _, label := range expected {
		expectedByName[label.Name] = label.Value
	}
	unknownReserved := []string{}
	for name := range labels {
		if strings.HasPrefix(name, ValidationLabelPrefix) {
			if _, found := expectedByName[name]; !found {
				unknownReserved = append(unknownReserved, name)
			}
		}
	}
	if len(unknownReserved) != 0 {
		sort.Strings(unknownReserved)
		return fmt.Errorf("final image contains unknown reserved validation label %q", unknownReserved[0])
	}
	for _, expectedLabel := range expected {
		value, found := labels[expectedLabel.Name]
		if !found {
			return fmt.Errorf("final image is missing reserved validation label %q", expectedLabel.Name)
		}
		if value != expectedLabel.Value {
			return fmt.Errorf("final image validation label %q is %q, want %q", expectedLabel.Name, value, expectedLabel.Value)
		}
	}
	return nil
}
