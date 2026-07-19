package providerstore

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestArtifactDescriptorValidate(t *testing.T) {
	valid := ArtifactDescriptor{
		LogicalPath: "wheels/demo.whl",
		Kind:        "wheel",
		Size:        "1024",
		SHA256:      canonical.Digest("sha256:" + strings.Repeat("a", 64)),
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*ArtifactDescriptor)
	}{
		{name: "absolute path", mutate: func(value *ArtifactDescriptor) { value.LogicalPath = "/demo.whl" }},
		{name: "escaping path", mutate: func(value *ArtifactDescriptor) { value.LogicalPath = "../demo.whl" }},
		{name: "backslash", mutate: func(value *ArtifactDescriptor) { value.LogicalPath = `wheels\demo.whl` }},
		{name: "kind", mutate: func(value *ArtifactDescriptor) { value.Kind = "Wheel" }},
		{name: "leading zero", mutate: func(value *ArtifactDescriptor) { value.Size = "01" }},
		{name: "negative", mutate: func(value *ArtifactDescriptor) { value.Size = "-1" }},
		{name: "digest", mutate: func(value *ArtifactDescriptor) { value.SHA256 = "abc" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatalf("invalid descriptor succeeded: %#v", candidate)
			}
		})
	}
}
