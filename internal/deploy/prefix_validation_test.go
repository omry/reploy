package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func prefixValidationTestDigest(char string) canonical.Digest {
	return canonical.Digest("sha256:" + strings.Repeat(char, 64))
}

func validPrefixValidation() PrefixValidationV1 {
	subject := prefixValidationTestDigest("1")
	return PrefixValidationV1{
		Schema: PrefixValidationSchemaV1, SubjectRootFS: subject,
		Profiles: []providers.ValidationEvidence{
			{Schema: providers.ValidationEvidenceSchemaV1, SubjectRootFS: subject, ProfileDigest: prefixValidationTestDigest("2")},
			{Schema: providers.ValidationEvidenceSchemaV1, SubjectRootFS: subject, ProfileDigest: prefixValidationTestDigest("3")},
		},
		RuntimePolicy: prefixValidationTestDigest("4"), ExposedOutputs: []providers.ExecutableEvidence{},
	}
}

func prefixValidationOutput(component string, name string, path string, digestChar string) providers.ExecutableEvidence {
	return providers.ExecutableEvidence{
		Schema: providers.ExecutableEvidenceSchemaV1,
		Output: providers.QualifiedOutput{Component: component, Name: name}, InvocationPath: path,
		LinkChain: []providers.LinkEvidence{},
		Terminal: providers.FileEvidence{
			Schema: providers.FileEvidenceSchemaV1, Path: path, Kind: "regular", Mode: "0755", Size: "1", SHA256: prefixValidationTestDigest(digestChar),
		},
		Access: providers.PortableAccessEvidence{
			Schema: providers.PortableAccessSchemaV1, Profile: providers.PortableOutputAccessV1,
			Paths: []providers.AccessPathEvidence{{Path: path, Kind: "regular", Mode: "0755", Required: "other-read-execute"}},
		},
		Facts: providers.CanonicalProviderData{Schema: "prefix-test-output-v1", Value: canonical.Object{}},
	}
}

func TestPrefixValidationDigestAndStoreRoundTrip(t *testing.T) {
	record := validPrefixValidation()
	digest, err := PrefixValidationDigest(record)
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reference, err := PublishPrefixValidation(context.Background(), store, record)
	if err != nil {
		t.Fatal(err)
	}
	if reference != (providerstore.StoreObjectRef{Kind: providerstore.ValidationRecordKind, Digest: digest}) {
		t.Fatalf("reference = %#v", reference)
	}
	loaded, err := LoadPrefixValidation(store, reference)
	if err != nil {
		t.Fatal(err)
	}
	loadedDigest, err := PrefixValidationDigest(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if loadedDigest != digest {
		t.Fatalf("loaded digest = %s, want %s", loadedDigest, digest)
	}
}

func TestPrefixValidationRejectsIncompleteOrMismatchedEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PrefixValidationV1)
		want   string
	}{
		{name: "nil profiles", mutate: func(value *PrefixValidationV1) { value.Profiles = nil }, want: "arrays"},
		{name: "profile subject", mutate: func(value *PrefixValidationV1) { value.Profiles[0].SubjectRootFS = prefixValidationTestDigest("9") }, want: "different rootfs"},
		{name: "profile order", mutate: func(value *PrefixValidationV1) {
			value.Profiles[0], value.Profiles[1] = value.Profiles[1], value.Profiles[0]
		}, want: "sorted"},
		{name: "runtime policy", mutate: func(value *PrefixValidationV1) { value.RuntimePolicy = "bad" }, want: "runtime policy"},
		{name: "nil outputs", mutate: func(value *PrefixValidationV1) { value.ExposedOutputs = nil }, want: "arrays"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validPrefixValidation()
			test.mutate(&value)
			if err := ValidatePrefixValidation(value); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPrefixValidationRequiresUniqueSortedExposedOutputs(t *testing.T) {
	record := validPrefixValidation()
	record.ExposedOutputs = []providers.ExecutableEvidence{
		prefixValidationOutput("api", "server", "/opt/api/server", "5"),
		prefixValidationOutput("web", "server", "/opt/web/server", "6"),
	}
	if err := ValidatePrefixValidation(record); err != nil {
		t.Fatal(err)
	}
	record.ExposedOutputs[0], record.ExposedOutputs[1] = record.ExposedOutputs[1], record.ExposedOutputs[0]
	if err := ValidatePrefixValidation(record); err == nil || !strings.Contains(err.Error(), "sorted") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodePrefixValidationRejectsWrongIdentityAndNoncanonicalContent(t *testing.T) {
	record := validPrefixValidation()
	content, reference, err := EncodePrefixValidation(record)
	if err != nil {
		t.Fatal(err)
	}
	wrong := reference
	wrong.Digest = prefixValidationTestDigest("f")
	if _, err := DecodePrefixValidation(content, wrong); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("wrong identity error = %v", err)
	}
	noncanonical := append([]byte(" \n"), content...)
	if _, err := DecodePrefixValidation(noncanonical, reference); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("noncanonical error = %v", err)
	}
}

func TestPrefixValidationLabelsBindImageSubjectAndRecord(t *testing.T) {
	image := providers.RealizedImageV1{
		Digest: prefixValidationTestDigest("7"), ConfigDigest: prefixValidationTestDigest("8"), RootFSSubject: prefixValidationTestDigest("9"),
	}
	reference := providerstore.StoreObjectRef{Kind: providerstore.ValidationRecordKind, Digest: prefixValidationTestDigest("a")}
	generated, err := PrefixValidationLabels(image.RootFSSubject, reference)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{"org.example.vendor": "inherited"}
	for _, label := range generated {
		labels[label.Name] = label.Value
	}
	if err := ValidatePrefixValidationLabels(labels, image, reference); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{name: "missing", mutate: func(value map[string]string) { delete(value, ValidationRecordLabel) }, want: "missing"},
		{name: "wrong subject", mutate: func(value map[string]string) { value[ValidationSubjectLabel] = string(prefixValidationTestDigest("b")) }, want: "want"},
		{name: "unknown reserved", mutate: func(value map[string]string) { value[ValidationLabelPrefix+"extra"] = "value" }, want: "unknown reserved"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := map[string]string{}
			for name, labelValue := range labels {
				value[name] = labelValue
			}
			test.mutate(value)
			if err := ValidatePrefixValidationLabels(value, image, reference); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
