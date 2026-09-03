package toolcatalog

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

const portableToolTestDecodeDigestV1 = canonical.Digest(
	"sha256:0000000000000000000000000000000000000000000000000000000000000000",
)

func TestValidationProfileDigestV1UsesPortableToolRecordIdentity(t *testing.T) {
	profile := *(validRecordValuesV1()[10].(*ValidationProfileRecordV1))
	digest, err := ValidationProfileDigestV1(profile)
	if err != nil {
		t.Fatal(err)
	}
	want, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, &profile)
	if err != nil {
		t.Fatal(err)
	}
	if digest != want {
		t.Fatalf("profile digest = %s, want %s", digest, want)
	}
}

func TestValidationProfileDigestV1RejectsInvalidProfile(t *testing.T) {
	profile := *(validRecordValuesV1()[10].(*ValidationProfileRecordV1))
	profile.Probes = nil
	if _, err := ValidationProfileDigestV1(profile); err == nil {
		t.Fatal("invalid validation profile produced a digest")
	}
}

func portableToolValidationProfileEnvelopeV1(t *testing.T) (
	providers.PortableToolRecordReferenceV1,
	providers.CanonicalProviderData,
	ValidationProfileRecordV1,
) {
	t.Helper()
	profile := *(validRecordValuesV1()[10].(*ValidationProfileRecordV1))
	compiled, err := compilePortableToolValidationProfilesV1([]ValidationProfileRecordV1{profile})
	if err != nil {
		t.Fatal(err)
	}
	return compiled[0].Reference, compiled[0].Record, profile
}

func TestDecodePortableToolValidationProfileV1RoundTripsTheCompiledEnvelope(t *testing.T) {
	reference, record, want := portableToolValidationProfileEnvelopeV1(t)
	got, err := DecodePortableToolValidationProfileV1(reference, record)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded profile = %#v, want %#v", got, want)
	}
}

func TestDecodePortableToolValidationProfileV1RejectsSubstitutedRecords(t *testing.T) {
	reference, _, _ := portableToolValidationProfileEnvelopeV1(t)
	for _, test := range []struct {
		name      string
		reference providers.PortableToolRecordReferenceV1
		mutate    func(*providers.CanonicalProviderData)
		want      string
	}{
		{
			name:      "wrong data schema",
			reference: reference,
			mutate:    func(data *providers.CanonicalProviderData) { data.Schema = "portable-tool-payload-v1" },
			want:      "must use schema",
		},
		{
			name:      "unknown field",
			reference: reference,
			mutate:    func(data *providers.CanonicalProviderData) { data.Value["unexpected"] = "value" },
			want:      "decode validation profile data",
		},
		{
			name:      "mismatched reference id",
			reference: providers.PortableToolRecordReferenceV1{ID: "tool:other/releases/1/validation/profiles/x", Digest: reference.Digest},
			mutate:    func(*providers.CanonicalProviderData) {},
			want:      "does not match reference",
		},
		{
			name:      "mismatched reference digest",
			reference: providers.PortableToolRecordReferenceV1{ID: reference.ID, Digest: portableToolTestDecodeDigestV1},
			mutate:    func(*providers.CanonicalProviderData) {},
			want:      "does not match reference",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, mutable, _ := portableToolValidationProfileEnvelopeV1(t)
			test.mutate(&mutable)
			_, err := DecodePortableToolValidationProfileV1(test.reference, mutable)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
