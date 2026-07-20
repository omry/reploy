package dockerdeploy

import (
	"context"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
)

func TestReproduceAPTOutputEvidenceUsesHeldSessionOwnerAndExactStateQueries(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	observation := directExecutableObservation("output", "/usr/bin/hello")
	freshEvidence, err := ExecutableEvidenceFromProbe(observation, ProbeExecutableBinding{
		Output: providers.QualifiedOutput{Component: "system", Name: "hello"},
		Facts:  providers.CanonicalProviderData{Schema: aptprovider.ExplicitExportSchemaV1, Value: canonical.Object{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tuple := aptprovider.PackageTuple{
		Name: "hello", Version: "2.10-3", Architecture: "amd64", Status: aptprovider.InstalledPackageStatusV1,
	}
	lockedEvidence := freshEvidence
	lockedEvidence.Terminal.Owner = &providers.OwnerEvidence{
		Provider: "apt",
		Data: providers.CanonicalProviderData{Schema: aptprovider.DPKGOwnerDataSchemaV1, Value: canonical.Object{
			"name": tuple.Name, "version": tuple.Version, "architecture": tuple.Architecture, "status": tuple.Status,
		}},
	}
	previous := runImageValidationFollowupCommand
	t.Cleanup(func() { runImageValidationFollowupCommand = previous })
	commands := []CommandSpec{}
	outputs := [][]byte{
		[]byte("hello: /usr/bin/hello\n"),
		[]byte("hello:amd64\t2.10-3\tamd64\tinstall ok installed\n"),
	}
	runImageValidationFollowupCommand = func(spec CommandSpec, options RunOptions) error {
		commands = append(commands, spec)
		_, _ = options.Stdout.Write(outputs[len(commands)-1])
		return nil
	}
	session := &ImageValidationSession{descriptor: descriptor, containerName: "held-validation"}
	reproduced, err := reproduceAPTOutputEvidence(
		context.Background(), session, "amd64",
		[]providers.ExecutableEvidence{freshEvidence}, []providers.ExecutableEvidence{lockedEvidence},
	)
	if err != nil {
		t.Fatal(err)
	}
	if reproduced[0].Terminal.Owner == nil || reproduced[0].Terminal.Owner.Data.Value["version"] != tuple.Version {
		t.Fatalf("reproduced output = %#v", reproduced)
	}
	want := [][]string{
		{"exec", "--user", "0:0", "--workdir", "/", "held-validation", "/usr/bin/dpkg-query", "--search", "/usr/bin/hello"},
		{"exec", "--user", "0:0", "--workdir", "/", "held-validation", "/usr/bin/dpkg-query", "--show", "--showformat=${binary:Package}\t${Version}\t${Architecture}\t${Status}\n", "hello"},
	}
	if len(commands) != len(want) {
		t.Fatalf("APT output commands = %#v", commands)
	}
	for index := range want {
		if !reflect.DeepEqual(commands[index].Args, want[index]) {
			t.Fatalf("APT output command %d = %#v", index, commands[index])
		}
	}
}
