package providers

import (
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

func TestBuildErrorV1RendersSafeFieldsAndRetainsCause(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("raw backend output with credentials")
	failure := NewBuildErrorV1(BuildErrorV1{
		Code: "apt.update_failed", Phase: "resolve", Platform: &platform, NodeID: "apt",
		BaseDigest: canonical.Digest("sha256:0123456789abcdef"),
		Correction: &CorrectionV1{Kind: "select-updatable-base"}, CauseKind: "apt.resolve.update",
	}, cause)
	message := failure.Error()
	for _, want := range []string{"apt.update_failed", "apt.resolve.update", "linux/arm64", "sha256:0123456789abcdef", "select or rebuild a base image"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not contain %q", message, want)
		}
	}
	if strings.Contains(message, "credentials") || !errors.Is(failure, cause) || failure.Schema != BuildErrorSchemaV1 {
		t.Fatalf("failure = %#v, message = %q", failure, message)
	}
}
