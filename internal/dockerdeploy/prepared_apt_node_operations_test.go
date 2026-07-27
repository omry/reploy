package dockerdeploy

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestAPTConsumerValidationSelectsOnlyLockedCarrierAndLauncher(t *testing.T) {
	config := pythonConsumerTestImageConfig()
	config.Environment = []providers.EnvironmentVariable{{Name: "LANG", Value: "C"}}
	validation := APTBaseValidation{Executables: []providers.ValidatedExecutableInput{
		rendererExecutable("apt_get", providers.ExecutableRoleProviderPrerequisite, "/usr/bin/apt-get"),
		rendererExecutable("env", providers.ExecutableRoleEnvironmentLauncher, "/usr/bin/env"),
		rendererExecutable("sh", providers.ExecutableRoleCarrier, "/bin/sh"),
	}}
	consumer, err := aptConsumerValidation(validation, config)
	if err != nil {
		t.Fatal(err)
	}
	if consumer.Carrier.ID != "sh" || consumer.EnvironmentLauncher.ID != "env" || consumer.FinalImageConfig.WorkingDir != config.WorkingDir {
		t.Fatalf("consumer = %#v", consumer)
	}
	config.Environment[0].Value = "changed"
	if consumer.FinalImageConfig.Environment[0].Value == "changed" {
		t.Fatal("APT consumer retained mutable final config input")
	}
}

func TestValidateAPTReusableArtifactsRequiresExactDebReferences(t *testing.T) {
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	deb := providerstore.ArtifactDescriptor{LogicalPath: "debs/demo.deb", Kind: "deb", Size: "1", SHA256: digest}
	reference, err := deb.StoreObjectRef()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAPTReusableArtifacts([]providerstore.StoreObjectRef{reference}, []providerstore.ArtifactDescriptor{deb}); err != nil {
		t.Fatal(err)
	}
	wheel := deb
	wheel.Kind = "wheel"
	if err := validateAPTReusableArtifacts([]providerstore.StoreObjectRef{reference}, []providerstore.ArtifactDescriptor{wheel}); err == nil || !strings.Contains(err.Error(), "not a deb") {
		t.Fatalf("non-deb error = %v", err)
	}
	if err := validateAPTReusableArtifacts([]providerstore.StoreObjectRef{}, []providerstore.ArtifactDescriptor{deb}); err == nil || !strings.Contains(err.Error(), "does not uniquely match") {
		t.Fatalf("missing reference error = %v", err)
	}
}
