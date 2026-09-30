package dockerdeploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

func TestInspectPortableRuntimeLayerReferenceRechecksAliasAfterImageAudit(t *testing.T) {
	first := rendererDigest("a")
	second := rendererDigest("b")
	reference := "reploy/env/demo:p-" + strings.Repeat("1", environmentReferenceRandomBytes*2) + "-" + strings.TrimPrefix(string(first), "sha256:")
	platform := blueprint.Platform{OS: "linux", Architecture: "amd64", Canonical: "linux/amd64"}
	for _, tc := range []struct {
		name   string
		second canonical.Digest
		lost   bool
		want   string
	}{
		{name: "stable", second: first},
		{name: "retargeted", second: second, want: "changed during inspection"},
		{name: "removed", lost: true, want: "inspect portable runtime layer reference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, audits := 0, 0
			_, err := inspectPortableRuntimeLayerReferenceWith(t.Context(), reference, platform,
				func(_ context.Context, args ...string) (string, error) {
					reads++
					if reads == 2 {
						if tc.lost {
							return "", errors.New("reference missing")
						}
						return string(tc.second), nil
					}
					return string(first), nil
				},
				func(_ context.Context, candidate BuiltImageCandidate, _ blueprint.Platform) (InspectedImageCandidate, error) {
					audits++
					if candidate.ImageID != first {
						t.Fatalf("audited image %s", candidate.ImageID)
					}
					return InspectedImageCandidate{}, nil
				},
			)
			if audits != 1 || reads != 2 || (tc.want == "" && err != nil) || (tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want))) {
				t.Fatalf("audits=%d reads=%d error=%v", audits, reads, err)
			}
		})
	}
}
