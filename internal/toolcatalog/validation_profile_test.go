package toolcatalog

import (
	"testing"

	"github.com/omry/reploy/internal/canonical"
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
