package python

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	providerapi "github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func pythonMaterializeTestInput(t *testing.T) providerapi.MaterializeInput {
	t.Helper()
	dir := t.TempDir()
	writeTestWheel(t, dir, "demo_server-1.2.3-py3-none-any.whl", "Demo-Server", "1.2.3", map[string]string{"demo-server": "demo:main"})
	plan, platform, upstream, catalog, selectedEvidence := preparedNodeTestPlan(t, "demo-server==1.2.3")
	resolver := WheelNodeResolver{
		PrepareWheels: func(context.Context, providerapi.ResolveInput, providerapi.ExecutableEvidence) (string, error) {
			return dir, nil
		},
		ResolveInterpreter: func(context.Context, providerapi.ExecutableRequirement, []providerapi.RealizedOutput, providerapi.RealizedImageV1, blueprint.Platform) (providerapi.ExecutableEvidence, error) {
			return selectedEvidence, nil
		},
	}
	request := providerapi.ResolveNodeRequest{
		Plan: plan, NodeID: "python/application", EarlierCatalog: catalog, Platform: platform,
		SourceCandidates: []providerapi.ResolvedSourceInput{}, Upstream: upstream, ReusableArtifacts: []providerstore.StoreObjectRef{},
	}
	result, err := providerapi.ResolveProviderNode(context.Background(), request, resolver, preparedTestSink{}, providerapi.ProviderOwnerValidators{
		Profile: ValidateRequirementProfileV1, Bundle: ValidateResolvedBundlePayloadV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	validated := preparedTestConsumerValidation()
	return providerapi.MaterializeInput{
		Bundle: result.Bundle, Profile: result.Profile,
		AssemblyParent: result.Bundle.Payload.Upstream,
		Carrier:        validated.Carrier, EnvironmentLauncher: validated.EnvironmentLauncher,
		FinalImageConfig: validated.FinalImageConfig,
	}
}

func TestComponentProviderMaterializeBuildsClosedOfflineTransaction(t *testing.T) {
	input := pythonMaterializeTestInput(t)
	transaction, err := (ComponentProvider{}).Materialize(input)
	if err != nil {
		t.Fatal(err)
	}
	if transaction.Schema != providerapi.MaterializationTransactionSchemaV1 || transaction.NodeID != "python/application" || transaction.RecipeVersion != MaterializationRecipeVersion {
		t.Fatalf("transaction identity = %#v", transaction)
	}
	if transaction.Upstream != input.AssemblyParent || transaction.Upstream != input.Bundle.Payload.Upstream {
		t.Fatalf("transaction upstream = %#v; assembly parent = %#v; resolver upstream = %#v", transaction.Upstream, input.AssemblyParent, input.Bundle.Payload.Upstream)
	}
	if len(transaction.Prerequisites) != 1 || transaction.Prerequisites[0].ID != "interpreter" || transaction.Prerequisites[0].Role != providerapi.ExecutableRoleSelectedOutput {
		t.Fatalf("prerequisites = %#v", transaction.Prerequisites)
	}
	wantArgv := []providerapi.TypedArgument{
		{Kind: providerapi.TypedArgumentValidatedExecutable, ExecutableID: "carrier"},
		{Kind: providerapi.TypedArgumentLiteral, Literal: "-eu"},
		{Kind: providerapi.TypedArgumentMountedArtifact, MountID: "script", RelativePath: "scripts/python-materialize-v1.sh"},
		{Kind: providerapi.TypedArgumentValidatedExecutable, ExecutableID: "interpreter"},
		{Kind: providerapi.TypedArgumentGeneratedExecutable, GeneratedID: "venv_python"},
		{Kind: providerapi.TypedArgumentLiteral, Literal: "/opt/reploy/providers/python/application"},
		{Kind: providerapi.TypedArgumentMountedArtifact, MountID: "wheels", RelativePath: "wheels/demo_server-1.2.3-py3-none-any.whl"},
	}
	if !reflect.DeepEqual(transaction.Argv, wantArgv) {
		t.Fatalf("argv = %#v, want %#v", transaction.Argv, wantArgv)
	}
	if len(transaction.Mounts) != 2 || transaction.Mounts[0].SourceDigest != transaction.Script.SHA256 || transaction.Mounts[1].SourceDigest != input.Bundle.Identity {
		t.Fatalf("mounts = %#v", transaction.Mounts)
	}
	wantGenerated := []providerapi.GeneratedExecutableDeclaration{
		{ID: "output_demo-server", Path: "/opt/reploy/providers/python/application/bin/demo-server", ExclusiveRoot: "/opt/reploy/providers/python/application", ValidationPolicy: providerapi.ValidationPolicyCompatible},
		{ID: "venv_python", Path: "/opt/reploy/providers/python/application/bin/python", ExclusiveRoot: "/opt/reploy/providers/python/application", ValidationPolicy: providerapi.ValidationPolicyCompatible},
	}
	if !reflect.DeepEqual(transaction.GeneratedExecutables, wantGenerated) || !reflect.DeepEqual(transaction.FinalImageConfig, input.FinalImageConfig) {
		t.Fatalf("generated/config = %#v; %#v", transaction.GeneratedExecutables, transaction.FinalImageConfig)
	}
	if transaction.Network != providerapi.NetworkPolicyNone || !transaction.ChildEnvironment.InheritNone || len(transaction.ChildEnvironment.Variables) != 0 {
		t.Fatalf("network/environment = %#v; %#v", transaction.Network, transaction.ChildEnvironment)
	}
	if err := providerapi.ValidateMaterializationTransaction(transaction); err != nil {
		t.Fatal(err)
	}
}

func TestComponentProviderMaterializeRejectsBundleInterpreterDrift(t *testing.T) {
	input := pythonMaterializeTestInput(t)
	request, err := decodeCanonicalProviderRequestV1(input.Bundle.Payload.Request)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := DecodeCanonicalBundleDataV1(request.Component, input.Bundle.Payload.ProviderPayload)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Interpreter.Facts = CanonicalInterpreterFactsV1("3.12.9")
	data, err := CanonicalBundleDataV1(request.Component, bundle)
	if err != nil {
		t.Fatal(err)
	}
	payload := input.Bundle.Payload
	payload.ProviderPayload = data
	rebuilt, err := providerapi.NewResolvedBundle(payload, ValidateResolvedBundlePayloadV1)
	if err != nil {
		t.Fatal(err)
	}
	input.Bundle = rebuilt
	if _, err := (ComponentProvider{}).Materialize(input); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v", err)
	}
}
