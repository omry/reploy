package deploy

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

func imageTestDigest(char string) canonical.Digest {
	return canonical.Digest("sha256:" + strings.Repeat(char, 64))
}

func validImageDescriptor(t *testing.T) ImageDescriptor {
	t.Helper()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	return ImageDescriptor{
		Schema: ImageDescriptorSchemaV1, Platform: platform, AuthorReference: "debian:13",
		ImmutableReference: "debian@" + string(imageTestDigest("1")), ManifestDigest: imageTestDigest("1"), ConfigDigest: imageTestDigest("2"),
		RootFSDiffIDs: []canonical.Digest{imageTestDigest("3"), imageTestDigest("4")},
	}
}

func TestImageDescriptorAndRootFSSubject(t *testing.T) {
	descriptor := validImageDescriptor(t)
	if err := descriptor.Validate(); err != nil {
		t.Fatal(err)
	}
	first, err := RootFSSubject(descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RootFSSubject(descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("rootfs subjects differ: %q != %q", first, second)
	}
	reversed := []canonical.Digest{descriptor.RootFSDiffIDs[1], descriptor.RootFSDiffIDs[0]}
	changed, err := RootFSSubject(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("rootfs layer order did not affect the subject")
	}
}

func TestLocalImageDescriptorUsesConfigIdentityWithoutManifest(t *testing.T) {
	descriptor := validImageDescriptor(t)
	descriptor.AuthorReference = descriptor.ImmutableReference
	descriptor.ImmutableReference = string(descriptor.ConfigDigest)
	descriptor.ManifestDigest = ""
	if err := descriptor.Validate(); err != nil {
		t.Fatal(err)
	}
	descriptor.ManifestDigest = imageTestDigest("8")
	if err := descriptor.Validate(); err == nil || !strings.Contains(err.Error(), "must not fabricate") {
		t.Fatalf("error = %v", err)
	}
}

func TestImageDescriptorRejectsMutableOrUnsafeIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ImageDescriptor)
	}{
		{name: "mutable immutable reference", mutate: func(value *ImageDescriptor) { value.ImmutableReference = "debian:13" }},
		{name: "credential URL", mutate: func(value *ImageDescriptor) { value.AuthorReference = "https://user:pass@example/repo" }},
		{name: "manifest mismatch", mutate: func(value *ImageDescriptor) { value.ManifestDigest = imageTestDigest("8") }},
		{name: "empty rootfs", mutate: func(value *ImageDescriptor) { value.RootFSDiffIDs = []canonical.Digest{} }},
		{name: "bad config digest", mutate: func(value *ImageDescriptor) { value.ConfigDigest = "bad" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validImageDescriptor(t)
			test.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("invalid image descriptor was accepted")
			}
		})
	}
}

func TestBaseConfigRequiresCanonicalEffectiveCollections(t *testing.T) {
	valid := BaseConfig{
		Schema:      BaseConfigSchemaV1,
		Environment: []ConfigEnvironmentVariable{{Name: "HOME", Value: "/root"}, {Name: "PATH", Value: "/usr/bin"}},
		Entrypoint:  []string{}, Command: []string{}, OnBuild: []string{}, Volumes: []string{"/data", "/mnt/cache"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []func(*BaseConfig){
		func(value *BaseConfig) { value.Environment = nil },
		func(value *BaseConfig) {
			value.Environment[0], value.Environment[1] = value.Environment[1], value.Environment[0]
		},
		func(value *BaseConfig) { value.Volumes[0] = "data" },
		func(value *BaseConfig) { value.Volumes[0], value.Volumes[1] = value.Volumes[1], value.Volumes[0] },
	}
	for index, mutate := range tests {
		value := valid
		value.Environment = append([]ConfigEnvironmentVariable{}, valid.Environment...)
		value.Volumes = append([]string{}, valid.Volumes...)
		mutate(&value)
		if err := value.Validate(); err == nil {
			t.Fatalf("invalid base config %d was accepted", index)
		}
	}
}
