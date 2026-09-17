package python

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
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
		{Kind: providerapi.TypedArgumentMountedArtifact, MountID: "script", RelativePath: "scripts/python-materialize-v3.sh"},
		{Kind: providerapi.TypedArgumentValidatedExecutable, ExecutableID: "interpreter"},
		{Kind: providerapi.TypedArgumentGeneratedExecutable, GeneratedID: "venv_python"},
		{Kind: providerapi.TypedArgumentLiteral, Literal: "/opt/reploy/providers/python/application"},
		{Kind: providerapi.TypedArgumentLiteral, Literal: pythonOutputValidationMarker},
		{Kind: providerapi.TypedArgumentLiteral, Literal: "/opt/reploy/providers/python/application/bin/demo-server"},
		{Kind: providerapi.TypedArgumentLiteral, Literal: pythonWheelArgumentMarker},
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
	bundle.Interpreter.Facts = CanonicalInterpreterFactsV2(testInterpreterFactsV2("3.12.9", nil, nil))
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

func TestPythonMaterializationScriptPreservesWheelsAndRejectsBadWrappers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell materialization fixture requires a POSIX host")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("POSIX shell materialization fixture requires sh: %v", err)
	}
	dir := t.TempDir()
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	interpreter := filepath.Join(dir, "interpreter")
	fakePython := filepath.Join(dir, "fake-python")
	writeExecutable := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable(interpreter, `#!/bin/sh
set -eu
[ "$1" = "-m" ] && [ "$2" = "venv" ]
mkdir -p "$3/bin"
: > "$VENV_CALL_LOG"
cp "$FAKE_PYTHON" "$3/bin/python"
chmod 755 "$3/bin/python"
`)
	writeExecutable(fakePython, `#!/bin/sh
set -eu
[ "$1" = "-m" ] && [ "$2" = "pip" ]
printf '%s\n' "$@" > "$PIP_ARGS_LOG"
if [ "${SKIP_OUTPUT:-}" = "1" ]; then
    exit 0
fi
shebang="$0"
if [ "${WRONG_SHEBANG:-}" = "1" ]; then
    shebang=/usr/bin/python3
fi
printf '#!%s\n' "$shebang" > "$PY_OUTPUT"
chmod 755 "$PY_OUTPUT"
`)
	script := filepath.Join(dir, "materialize.sh")
	if err := os.WriteFile(script, []byte(materializationScriptV1), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(root, output string, extra ...string) ([]byte, error) {
		t.Helper()
		args := []string{script, interpreter, filepath.Join(root, "bin", "python"), root,
			pythonOutputValidationMarker, output, pythonWheelArgumentMarker,
			"/.reploy-build/wheels/space name.whl", "/.reploy-build/wheels/other.whl"}
		cmd := exec.Command(shell, args...)
		cmd.Env = append(os.Environ(), append([]string{
			"FAKE_PYTHON=" + fakePython,
			"PY_OUTPUT=" + output,
			"PIP_ARGS_LOG=" + filepath.Join(root, "pip-args"),
			"VENV_CALL_LOG=" + filepath.Join(root, "venv-call"),
		}, extra...)...)
		return cmd.CombinedOutput()
	}
	root := filepath.Join(realDir, "venv")
	output := filepath.Join(root, "bin", "demo")
	if outputBytes, err := run(root, output); err != nil {
		t.Fatalf("valid materialization failed: %v: %s", err, outputBytes)
	}
	args, err := os.ReadFile(filepath.Join(root, "pip-args"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "/.reploy-build/wheels/space name.whl\n") || strings.Contains(string(args), pythonOutputValidationMarker) {
		t.Fatalf("pip did not receive untouched wheel argv: %q", args)
	}
	missingRoot := filepath.Join(realDir, "missing")
	if outputBytes, err := run(missingRoot, filepath.Join(missingRoot, "bin", "demo"), "SKIP_OUTPUT=1"); err == nil || !strings.Contains(string(outputBytes), "not generated") {
		t.Fatalf("missing wrapper result = %v: %s", err, outputBytes)
	}
	wrongRoot := filepath.Join(realDir, "wrong")
	if outputBytes, err := run(wrongRoot, filepath.Join(wrongRoot, "bin", "demo"), "WRONG_SHEBANG=1"); err == nil || !strings.Contains(string(outputBytes), "does not name") {
		t.Fatalf("wrong shebang result = %v: %s", err, outputBytes)
	}
	longRoot := filepath.Join(realDir, strings.Repeat("long-runtime-owner-", 10))
	if outputBytes, err := run(longRoot, filepath.Join(longRoot, "bin", "demo")); err != nil {
		t.Fatalf("long runtime root result = %v: %s", err, outputBytes)
	}

	existingRoot := filepath.Join(realDir, "existing")
	if err := os.Mkdir(existingRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existingRoot, "sentinel"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if outputBytes, err := run(existingRoot, filepath.Join(existingRoot, "bin", "demo")); err == nil || !strings.Contains(string(outputBytes), "already exists") {
		t.Fatalf("existing runtime root result = %v: %s", err, outputBytes)
	}
	if _, err := os.Stat(filepath.Join(existingRoot, "venv-call")); !os.IsNotExist(err) {
		t.Fatalf("existing runtime root invoked venv: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(existingRoot, "sentinel")); err != nil || string(content) != "stale" {
		t.Fatalf("existing runtime root was modified: %q, %v", content, err)
	}

	linkTarget := filepath.Join(realDir, "link-target")
	linkAncestor := filepath.Join(realDir, "link-ancestor")
	if err := os.Mkdir(linkTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linkTarget, linkAncestor); err != nil {
		t.Skipf("symlinked ancestor fixture unavailable: %v", err)
	}
	linkedRoot := filepath.Join(linkAncestor, "venv")
	if outputBytes, err := run(linkedRoot, filepath.Join(linkedRoot, "bin", "demo")); err == nil || !strings.Contains(string(outputBytes), "symlinked ancestor") {
		t.Fatalf("symlinked runtime ancestor result = %v: %s", err, outputBytes)
	}
	if _, err := os.Stat(filepath.Join(linkTarget, "venv", "venv-call")); !os.IsNotExist(err) {
		t.Fatalf("symlinked runtime ancestor invoked venv: %v", err)
	}

	directLinkTarget := filepath.Join(realDir, "direct-link-target")
	directLink := filepath.Join(realDir, "direct-link")
	if err := os.Mkdir(directLinkTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(directLinkTarget, directLink); err != nil {
		t.Skipf("direct root symlink fixture unavailable: %v", err)
	}
	if outputBytes, err := run(directLink, filepath.Join(directLink, "bin", "demo")); err == nil || !strings.Contains(string(outputBytes), "already exists") {
		t.Fatalf("symlinked runtime root result = %v: %s", err, outputBytes)
	}
	if _, err := os.Stat(filepath.Join(directLinkTarget, "venv-call")); !os.IsNotExist(err) {
		t.Fatalf("symlinked runtime root invoked venv: %v", err)
	}
}
