package deploy

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func pendingBuildTestDigest(char string) canonical.Digest {
	return canonical.Digest("sha256:" + strings.Repeat(char, 64))
}

func validPendingBuild(t *testing.T) PendingBuildV1 {
	t.Helper()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	old := EnvironmentGenerationState{
		Reference: "reploy/env/demo-abcd:g-old", ImageDigest: pendingBuildTestDigest("1"), RootFSSubject: pendingBuildTestDigest("2"),
		BuildLockDigest: pendingBuildTestDigest("3"), Platform: platform, RuntimePolicyDigest: pendingBuildTestDigest("4"),
	}
	return PendingBuildV1{
		Schema: PendingBuildSchemaV1, Phase: PendingBuildPhaseValidated, Old: &old,
		Candidate: PendingCandidateV1{
			TemporaryReference: "reploy/env/demo-abcd:tmp-new", GenerationReference: "reploy/env/demo-abcd:g-new",
			Image:           providers.RealizedImageV1{Digest: pendingBuildTestDigest("5"), ConfigDigest: pendingBuildTestDigest("5"), RootFSSubject: pendingBuildTestDigest("6")},
			BuildLockDigest: pendingBuildTestDigest("7"),
			StoreObjects: []providerstore.StoreObjectRef{
				{Kind: providerstore.BlobKind, Digest: pendingBuildTestDigest("8")},
				{Kind: providerstore.ValidationRecordKind, Digest: pendingBuildTestDigest("9")},
			},
		},
		Cleanup: []CleanupItemV1{
			{Kind: CleanupKindGenerationReference, Identity: old.Reference},
			{Kind: CleanupKindTemporaryImageReference, Identity: "reploy/env/demo-abcd:tmp-new"},
		},
	}
}

func TestPendingBuildCanonicalRoundTrip(t *testing.T) {
	record := validPendingBuild(t)
	content, err := EncodePendingBuild(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePendingBuild(content)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := EncodePendingBuild(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, reencoded) {
		t.Fatalf("pending build encoding changed:\n%s\n%s", content, reencoded)
	}
}

func TestPendingBuildAllowsFirstGeneration(t *testing.T) {
	record := validPendingBuild(t)
	record.Old = nil
	if _, err := EncodePendingBuild(record); err != nil {
		t.Fatal(err)
	}
}

func TestPendingBuildRejectsMalformedRecords(t *testing.T) {
	valid := validPendingBuild(t)
	tests := []struct {
		name   string
		mutate func(*PendingBuildV1)
		want   string
	}{
		{name: "schema", mutate: func(value *PendingBuildV1) { value.Schema = "pending-build-v2" }, want: "schema"},
		{name: "phase", mutate: func(value *PendingBuildV1) { value.Phase = "published" }, want: "phase"},
		{name: "old", mutate: func(value *PendingBuildV1) { value.Old.ImageDigest = "bad" }, want: "old generation"},
		{name: "candidate reference", mutate: func(value *PendingBuildV1) { value.Candidate.TemporaryReference = "-bad" }, want: "candidate references"},
		{name: "same references", mutate: func(value *PendingBuildV1) { value.Candidate.GenerationReference = value.Candidate.TemporaryReference }, want: "must differ"},
		{name: "old reference reuse", mutate: func(value *PendingBuildV1) { value.Candidate.GenerationReference = value.Old.Reference }, want: "old generation"},
		{name: "candidate image", mutate: func(value *PendingBuildV1) { value.Candidate.Image.RootFSSubject = "bad" }, want: "candidate image"},
		{name: "store array", mutate: func(value *PendingBuildV1) { value.Candidate.StoreObjects = nil }, want: "store objects must use an array"},
		{name: "store order", mutate: func(value *PendingBuildV1) {
			value.Candidate.StoreObjects[0], value.Candidate.StoreObjects[1] = value.Candidate.StoreObjects[1], value.Candidate.StoreObjects[0]
		}, want: "store objects must be unique and sorted"},
		{name: "cleanup array", mutate: func(value *PendingBuildV1) { value.Cleanup = nil }, want: "cleanup must use an array"},
		{name: "cleanup kind", mutate: func(value *PendingBuildV1) { value.Cleanup[0].Kind = "image" }, want: "cleanup kind"},
		{name: "cleanup order", mutate: func(value *PendingBuildV1) {
			value.Cleanup[0], value.Cleanup[1] = value.Cleanup[1], value.Cleanup[0]
		}, want: "cleanup items must be unique and sorted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			old := *valid.Old
			candidate.Old = &old
			candidate.Candidate.StoreObjects = append([]providerstore.StoreObjectRef{}, valid.Candidate.StoreObjects...)
			candidate.Cleanup = append([]CleanupItemV1{}, valid.Cleanup...)
			test.mutate(&candidate)
			if _, err := EncodePendingBuild(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDecodePendingBuildRejectsUnknownTrailingAndNoncanonicalJSON(t *testing.T) {
	record := validPendingBuild(t)
	content, err := EncodePendingBuild(record)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		content []byte
		want    string
	}{
		{name: "unknown", content: append(append([]byte{}, content[:len(content)-1]...), []byte(`,"unknown":true}`)...), want: "unknown field"},
		{name: "trailing", content: append(append([]byte{}, content...), []byte(` {}`)...), want: "trailing JSON"},
		{name: "noncanonical", content: append([]byte(" "), content...), want: "not canonical"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodePendingBuild(test.content); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
