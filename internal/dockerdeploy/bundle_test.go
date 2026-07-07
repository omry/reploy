package dockerdeploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/python"
)

func TestInitRecordsBundleRootsInState(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	state := readDeploymentState(t, deployDir)
	if len(state.Bundle.Roots) != 1 {
		t.Fatalf("bundle roots = %#v", state.Bundle.Roots)
	}
	root := state.Bundle.Roots[0]
	if root.Provider != "python" || root.Kind != "package" || root.Source != "demo-suite" {
		t.Fatalf("bundle root = %#v", root)
	}
}

func TestBundleAddRemoveUpdatesStateAndRequirements(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	results, err := BundleAdd(BundleRootOptions{Dir: deployDir, Source: "demo-imap==1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	assertResultStatus(t, results, filepath.Join(deployDir, StateFileName), deploy.UpdateStatusUpdated)
	assertResultStatus(t, results, filepath.Join(deployDir, RequirementsFileName), deploy.UpdateStatusUpdated)
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-suite\ndemo-imap==1.2.3\n" {
		t.Fatalf("requirements = %q", got)
	}

	roots, err := BundleList(BundleListOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || roots[1].Source != "demo-imap==1.2.3" {
		t.Fatalf("bundle roots = %#v", roots)
	}

	results, err = BundleRemove(BundleRootOptions{Dir: deployDir, Source: "demo-imap==1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	assertResultStatus(t, results, filepath.Join(deployDir, StateFileName), deploy.UpdateStatusUpdated)
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-suite\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestBundleAddOverwritesRuntimeCompose(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(deployDir, ComposeFileName)
	if err := os.WriteFile(composePath, []byte("local runtime edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := BundleAdd(BundleRootOptions{Dir: deployDir, Source: "demo-imap==1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	assertResultStatus(t, results, composePath, deploy.UpdateStatusUpdated)
	if got := readFile(t, composePath); got == "local runtime edit\n" {
		t.Fatal("runtime compose was not overwritten")
	}
}

func TestBundleAddRecreatesMissingRuntimeComposeDir(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(deployDir, RuntimeDirName)); err != nil {
		t.Fatal(err)
	}

	results, err := BundleAdd(BundleRootOptions{Dir: deployDir, Source: "demo-imap==1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(deployDir, ComposeFileName)
	assertResultStatus(t, results, composePath, deploy.UpdateStatusUpdated)
	if _, err := os.Stat(composePath); err != nil {
		t.Fatalf("missing regenerated runtime compose: %v", err)
	}
}

func TestBundleListRejectsMissingRequirementsProjection(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(deployDir, RequirementsFileName)); err != nil {
		t.Fatal(err)
	}

	_, err = BundleList(BundleListOptions{Dir: deployDir})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "requirements projection is missing") ||
		!strings.Contains(err.Error(), "reploy stage --update --dir") ||
		!strings.Contains(err.Error(), deployDir) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBundleListRejectsStaleRequirementsProjection(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, RequirementsFileName), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = BundleList(BundleListOptions{Dir: deployDir})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "requirements projection is out of date") ||
		!strings.Contains(err.Error(), "reploy stage --update --dir") ||
		!strings.Contains(err.Error(), deployDir) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBundleListAllShowsRootAndTransitiveWheels(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"demo-server==1.2.3"},
	}); err != nil {
		t.Fatal(err)
	}
	bundleDir := filepath.Join(deployDir, BundleDirName)
	for _, name := range []string{"demo_server-1.2.3-py3-none-any.whl", "h11-0.16.0-py3-none-any.whl"} {
		if err := os.WriteFile(filepath.Join(bundleDir, name), []byte("wheel\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	packages, err := BundleListAll(BundleListOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	want := []BundleResolvedPackage{
		{Kind: "root", Requirement: "demo-server==1.2.3"},
		{Kind: "transitive", Requirement: "h11==0.16.0"},
	}
	if !reflect.DeepEqual(packages, want) {
		t.Fatalf("packages = %#v, want %#v", packages, want)
	}
}

func TestBundleOptionsListsBlueprintOptions(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	options, err := BundleOptions(BundleListOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(options) != 3 {
		t.Fatalf("options = %#v", options)
	}
	if options[0].Name != "demo-suite" || options[0].Identifier != "demo-suite" || options[0].Group != "meta" || options[0].Description == "" {
		t.Fatalf("first option = %#v", options[0])
	}
}

func TestProviderIdentifierRootRejectsUnsupportedProvider(t *testing.T) {
	_, err := providerIdentifierRoot("demo", "demo-component")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `provider "demo" does not support bundle option identifiers`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBundleAddOptionUsesBlueprintIdentifier(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"demo-server==1.2.3"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := BundleAddMany(BundleRootsOptions{Dir: deployDir, Names: []string{"imap"}}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-server==1.2.3\ndemo-imap\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestBundleAddManyOptionsUseBlueprintIdentifiers(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"demo-server==1.2.3"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := BundleAddMany(BundleRootsOptions{Dir: deployDir, Names: []string{"imap", "smtp"}}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-server==1.2.3\ndemo-imap\ndemo-smtp\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestBundleAddManyRejectsInvalidRootWithoutWriting(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"demo-server==1.2.3"},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = BundleAddMany(BundleRootsOptions{Dir: deployDir, Sources: []string{"imap", "not valid"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-server==1.2.3\n" {
		t.Fatalf("requirements were partially updated: %q", got)
	}
}

func TestBundleAddManyAcceptsUnknownUnpinnedPackage(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"demo-server==1.2.3"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := BundleAddMany(BundleRootsOptions{Dir: deployDir, Names: []string{"imap"}, Sources: []string{"aa"}}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-server==1.2.3\ndemo-imap\naa\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestBundleAddManyRejectsLikelyOptionTypoWithoutWriting(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"demo-server==1.2.3"},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = BundleAddMany(BundleRootsOptions{Dir: deployDir, Names: []string{"imap", "smtpa"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `unknown bundle option "smtpa"`) || !strings.Contains(err.Error(), `did you mean "smtp"`) || !strings.Contains(err.Error(), "--extra") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-server==1.2.3\n" {
		t.Fatalf("requirements were partially updated: %q", got)
	}
}

func TestBundleAddManySourcesAreExplicitRoots(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"demo-server==1.2.3"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := BundleAddMany(BundleRootsOptions{Dir: deployDir, Sources: []string{"smtpa"}}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-server==1.2.3\nsmtpa\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestBundleAddManySourcesCanOverlapOptionNames(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"demo-server==1.2.3"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := BundleAddMany(BundleRootsOptions{Dir: deployDir, Sources: []string{"imap"}}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-server==1.2.3\nimap\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestBundleRemoveSourcesRemoveExplicitRoots(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"demo-server==1.2.3"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := BundleAddMany(BundleRootsOptions{Dir: deployDir, Sources: []string{"imap", "smtp"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := BundleRemoveMany(BundleRootsOptions{Dir: deployDir, Sources: []string{"imap", "smtp"}}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-server==1.2.3\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestBundleRemoveOptionUsesBlueprintIdentifier(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"demo-server==1.2.3"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := BundleAddMany(BundleRootsOptions{Dir: deployDir, Names: []string{"imap"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := BundleRemoveMany(BundleRootsOptions{Dir: deployDir, Names: []string{"imap"}}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-server==1.2.3\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestBundleAddMetaOptionUsesBlueprintIdentifier(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"demo-server==1.2.3"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := BundleAddMany(BundleRootsOptions{Dir: deployDir, Names: []string{"demo-suite"}}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-server==1.2.3\ndemo-suite\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestBundleAddWheelCopiesWheelIntoDeploymentBundle(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	sourceWheel := filepath.Join(t.TempDir(), "demo-1.0.0-py3-none-any.whl")
	if err := os.WriteFile(sourceWheel, []byte("wheel content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := BundleAddWheel(BundleRootOptions{Dir: deployDir, Source: sourceWheel})
	if err != nil {
		t.Fatal(err)
	}
	targetWheel := filepath.Join(deployDir, BundleDirName, filepath.Base(sourceWheel))
	assertResultStatus(t, results, targetWheel, deploy.UpdateStatusUpdated)
	assertResultStatus(t, results, filepath.Join(deployDir, StateFileName), deploy.UpdateStatusUpdated)
	if got := readFile(t, targetWheel); got != "wheel content\n" {
		t.Fatalf("copied wheel = %q", got)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-suite\n/bundle/demo-1.0.0-py3-none-any.whl\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestBundleCheckCommandValidatesPreparedWheelhouse(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	spec, bundleDir, err := BundleCheckCommand(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	if bundleDir != filepath.Join(deployDir, BundleDirName) {
		t.Fatalf("bundle dir = %q", bundleDir)
	}
	if spec.Name != "docker" {
		t.Fatalf("name = %q", spec.Name)
	}
	wantSuffix := []string{
		"python",
		"-m",
		"pip",
		"--disable-pip-version-check",
		"install",
		"--no-cache-dir",
		"--progress-bar",
		"off",
		"--root-user-action",
		"ignore",
		"--target",
		"/tmp/reploy-wheelhouse-check",
		"--no-index",
		"--find-links",
		"/bundle",
		"-r",
		"/requirements.txt",
	}
	if !reflect.DeepEqual(spec.Args[len(spec.Args)-len(wantSuffix):], wantSuffix) {
		t.Fatalf("suffix = %#v", spec.Args[len(spec.Args)-len(wantSuffix):])
	}
}

func TestBundleCheckDryRunPrintsCommand(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := BundleCheck(BundleCheckOptions{Dir: deployDir, DryRun: true, Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "would validate installation bundle:") || !strings.Contains(stdout.String(), "docker run --rm") {
		t.Fatalf("stdout missing dry-run command:\n%s", stdout.String())
	}
}

func TestCommandLineQuotesShellSensitiveArgs(t *testing.T) {
	got := commandLine(CommandSpec{
		Name: "reploy",
		Args: []string{"bundle", "build", "--dir", "/tmp/reploy staging/it's live;rm"},
	})
	want := `reploy bundle build --dir '/tmp/reploy staging/it'"'"'s live;rm'`
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestStageUpdateCommandQuotesShellSensitiveDir(t *testing.T) {
	got := stageUpdateCommand("/tmp/reploy staging/it's live;rm")
	want := `reploy stage --update --dir '/tmp/reploy staging/it'"'"'s live;rm'`
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestBundleCheckSuppressesCommandOutputByDefault(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(deployDir, BundleDirName), 0o755); err != nil {
		t.Fatal(err)
	}

	var runOptions []RunOptions
	restore := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		runOptions = append(runOptions, options)
		return nil
	})
	defer restore()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := BundleCheck(BundleCheckOptions{Dir: deployDir, Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	if len(runOptions) != 1 {
		t.Fatalf("ran %d commands, want check", len(runOptions))
	}
	if runOptions[0].Context == nil {
		t.Fatalf("run options should include an interruptible context: %#v", runOptions[0])
	}
	if runOptions[0].Stdout != nil || runOptions[0].Stderr != nil {
		t.Fatalf("run options should suppress output by default: %#v", runOptions[0])
	}
	if stdout.String() != "" || stderr.String() != "" {
		t.Fatalf("stdout=%q stderr=%q, want quiet", stdout.String(), stderr.String())
	}
}

func TestBundleCheckVerboseStreamsCommandOutput(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(deployDir, BundleDirName), 0o755); err != nil {
		t.Fatal(err)
	}

	var runOptions []RunOptions
	restore := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		runOptions = append(runOptions, options)
		return nil
	})
	defer restore()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := BundleCheck(BundleCheckOptions{Dir: deployDir, Verbose: true, Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	if len(runOptions) != 1 {
		t.Fatalf("ran %d commands, want check", len(runOptions))
	}
	if runOptions[0].Context == nil {
		t.Fatalf("run options should include an interruptible context: %#v", runOptions[0])
	}
	if runOptions[0].Stdout != &stdout || runOptions[0].Stderr != &stderr {
		t.Fatalf("run options should stream verbose output: %#v", runOptions[0])
	}
}

func TestBundlePrepareCommandBuildsWheelhouse(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	wheelhouseDir := filepath.Join(t.TempDir(), "wheelhouse")

	spec, err := BundlePrepareCommand(deployDir, wheelhouseDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "docker" {
		t.Fatalf("name = %q", spec.Name)
	}
	wantSuffix := []string{
		"python",
		"-m",
		"pip",
		"--disable-pip-version-check",
		"wheel",
		"--no-cache-dir",
		"--progress-bar",
		"off",
		"--find-links",
		"/bundle",
		"--wheel-dir",
		"/wheelhouse",
		"-r",
		"/requirements.txt",
	}
	if !reflect.DeepEqual(spec.Args[len(spec.Args)-len(wantSuffix):], wantSuffix) {
		t.Fatalf("suffix = %#v", spec.Args[len(spec.Args)-len(wantSuffix):])
	}
	if !containsAdjacent(spec.Args, "--user", defaultContainerUser()) {
		t.Fatalf("linux bundle command did not run as host user: %#v", spec.Args)
	}
}

func TestBundlePrepareCommandOmitsHostUserOnDarwin(t *testing.T) {
	restore := stubHostPlatform(t, hostPlatform{GOOS: "darwin"})
	defer restore()

	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	spec, err := BundlePrepareCommand(deployDir, filepath.Join(t.TempDir(), "wheelhouse"), false)
	if err != nil {
		t.Fatal(err)
	}
	if containsArg(spec.Args, "--user") {
		t.Fatalf("darwin bundle command should let Docker use its default user: %#v", spec.Args)
	}
}

func TestBundlePreparePyPIOnlySkipsExistingWheelhouse(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	spec, err := BundlePrepareCommand(deployDir, filepath.Join(t.TempDir(), "wheelhouse"), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range spec.Args {
		if arg == "--find-links" || strings.Contains(arg, ":/bundle:ro") {
			t.Fatalf("pypi-only command included existing wheelhouse arg: %#v", spec.Args)
		}
	}
}

func TestBundleAddSourceBuildsWheelIntoDeploymentBundle(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	sourceDir := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pyproject.toml"), []byte("[build-system]\nrequires = [\"setuptools>=68\", \"wheel\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var specs []CommandSpec
	restore := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		specs = append(specs, spec)
		wheelhouse := hostPathForContainerMount(t, spec.Args, "/wheelhouse")
		return os.WriteFile(filepath.Join(wheelhouse, "demo-1.0.0-py3-none-any.whl"), []byte("wheel content\n"), 0o644)
	})
	defer restore()

	results, err := BundleAddSource(BundleRootOptions{Dir: deployDir, Source: sourceDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("ran %d commands, want 1", len(specs))
	}
	if !containsInOrder(specs[0].Args, []string{"python", "-m", "pip", "--disable-pip-version-check", "wheel", "--progress-bar", "off", "--no-deps", "--no-build-isolation", "--wheel-dir", "/wheelhouse", "/source"}) {
		t.Fatalf("source wheel command missing expected pip args: %#v", specs[0].Args)
	}
	targetWheel := filepath.Join(deployDir, BundleDirName, "demo-1.0.0-py3-none-any.whl")
	assertResultStatus(t, results, targetWheel, deploy.UpdateStatusUpdated)
	if got := readFile(t, targetWheel); got != "wheel content\n" {
		t.Fatalf("copied wheel = %q", got)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-suite\n/bundle/demo-1.0.0-py3-none-any.whl\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestBundleUpgradeResolvesPackageRootsAndPreparesBundle(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"demo-server==1.2.3", "demo-imap==1.2.3"},
	}); err != nil {
		t.Fatal(err)
	}

	var specs []CommandSpec
	restore := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		specs = append(specs, spec)
		switch {
		case containsInOrder(spec.Args, []string{"install", "--progress-bar", "off", "--root-user-action", "ignore", "--dry-run", "--ignore-installed"}):
			workDir := hostPathForContainerMount(t, spec.Args, "/work")
			report := `{"install":[{"metadata":{"name":"demo-server","version":"1.2.4"}},{"metadata":{"name":"demo-imap","version":"1.2.5"}}]}`
			return os.WriteFile(filepath.Join(workDir, "report.json"), []byte(report), 0o644)
		case containsInOrder(spec.Args, []string{"wheel", "--no-cache-dir"}):
			wheelhouse := hostPathForContainerMount(t, spec.Args, "/wheelhouse")
			if err := os.WriteFile(filepath.Join(wheelhouse, "demo_server-1.2.4-py3-none-any.whl"), []byte("server\n"), 0o644); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(wheelhouse, "demo_imap-1.2.5-py3-none-any.whl"), []byte("imap\n"), 0o644)
		case containsInOrder(spec.Args, []string{"install", "--no-cache-dir", "--progress-bar", "off", "--root-user-action", "ignore", "--target"}):
			return nil
		case containsInOrder(spec.Args, []string{"run", "--rm", "--no-deps", "-e", "REPLOY_CONTAINER_COMMAND=__reploy_runtime_warmup", "app"}):
			return nil
		default:
			t.Fatalf("unexpected bundle command: %#v", spec.Args)
			return nil
		}
	})
	defer restore()

	results, err := BundleUpgrade(BundleUpgradeOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 4 {
		t.Fatalf("ran %d commands, want resolve, build, check, and warm runtime", len(specs))
	}
	assertResultStatus(t, results, filepath.Join(deployDir, StateFileName), deploy.UpdateStatusUpdated)
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-server==1.2.4\ndemo-imap==1.2.5\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestBundleUpgradeTargetExactPin(t *testing.T) {
	roots := []deploy.ArtifactRoot{
		{Provider: python.ProviderName, Kind: "package", Source: "demo-server==1.2.3"},
		{Provider: python.ProviderName, Kind: "package", Source: "demo-imap==1.2.3"},
	}
	input, _, err := python.BundleUpgradeInput(roots, "demo-imap==1.2.9")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"demo-server==1.2.3", "demo-imap==1.2.9"}
	if !reflect.DeepEqual(input, want) {
		t.Fatalf("input = %#v, want %#v", input, want)
	}
}

func TestBundleUpgradeRejectsWheelRoots(t *testing.T) {
	roots := []deploy.ArtifactRoot{{Provider: python.ProviderName, Kind: "wheel", Source: "/bundle/demo-1.0.0-py3-none-any.whl"}}
	_, _, err := python.BundleUpgradeInput(roots, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "only supports package roots") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBundlePreparePyPIOnlyResolvesUnpinnedRoots(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var specs []CommandSpec
	restore := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		specs = append(specs, spec)
		switch {
		case containsInOrder(spec.Args, []string{"install", "--progress-bar", "off", "--root-user-action", "ignore", "--dry-run", "--ignore-installed"}):
			workDir := hostPathForContainerMount(t, spec.Args, "/work")
			report := `{"install":[{"metadata":{"name":"demo-suite","version":"1.2.4"}}]}`
			return os.WriteFile(filepath.Join(workDir, "report.json"), []byte(report), 0o644)
		case containsInOrder(spec.Args, []string{"wheel", "--no-cache-dir"}):
			wheelhouse := hostPathForContainerMount(t, spec.Args, "/wheelhouse")
			return os.WriteFile(filepath.Join(wheelhouse, "demo_suite-1.2.4-py3-none-any.whl"), []byte("suite\n"), 0o644)
		case containsInOrder(spec.Args, []string{"install", "--no-cache-dir", "--progress-bar", "off", "--root-user-action", "ignore", "--target"}):
			return nil
		case containsInOrder(spec.Args, []string{"run", "--rm", "--no-deps", "-e", "REPLOY_CONTAINER_COMMAND=__reploy_runtime_warmup", "app"}):
			return nil
		default:
			t.Fatalf("unexpected bundle command: %#v", spec.Args)
			return nil
		}
	})
	defer restore()

	if err := BundlePrepare(BundlePrepareOptions{Dir: deployDir, PyPIOnly: true}); err != nil {
		t.Fatal(err)
	}
	if len(specs) != 4 {
		t.Fatalf("ran %d commands, want resolve, build, check, and warm runtime", len(specs))
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-suite==1.2.4\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestBundlePrepareUsesPackLocalSourcesForFilePacks(t *testing.T) {
	packDir := makeTestPackWithManifest(t, strings.Replace(testPackManifest(), "    identifier: demo-suite\n", "    identifier: demo-suite\n    local_sources:\n      demo-pkg: local/demo-pkg\n", 1))
	sourceDir := filepath.Join(packDir, "local", "demo-pkg")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pyproject.toml"), []byte("[build-system]\nrequires = [\"setuptools>=68\", \"wheel\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref, Requirements: []string{"demo-pkg==1.2.3", "other-pkg==1.2.3"}}); err != nil {
		t.Fatal(err)
	}
	expectedRuntimeRequirements := "setuptools>=68\nwheel\ndemo-pkg==1.2.3\n/source/app/demo-pkg\nother-pkg==1.2.3\n"
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != expectedRuntimeRequirements {
		t.Fatalf("persistent requirements after stage = %q", got)
	}
	compose := readFile(t, filepath.Join(deployDir, ComposeFileName))
	if !strings.Contains(compose, strconv.Quote(sourceDir+":/source/app/demo-pkg:rw")) {
		t.Fatalf("compose did not mount local source:\n%s", compose)
	}
	if !strings.Contains(compose, "--no-index --find-links /bundle --no-deps --no-build-isolation -e") {
		t.Fatalf("compose did not install bundle-backed editable sources hermetically:\n%s", compose)
	}
	if !strings.Contains(compose, "--no-cache-dir --no-deps --no-build-isolation -e") {
		t.Fatalf("compose did not install editable sources without resolving dependencies:\n%s", compose)
	}
	checkSpec, _, err := BundleCheckCommand(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	if checkSourceMount := hostPathForContainerMount(t, checkSpec.Args, "/source/app/demo-pkg"); checkSourceMount != sourceDir {
		t.Fatalf("check source mount = %q, want %q", checkSourceMount, sourceDir)
	}

	var buildRequirements string
	var checkRequirements string
	var buildRequirementsPath string
	var checkRequirementsPath string
	var wheelhouseMount string
	var sourceMount string
	restore := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		switch {
		case containsInOrder(spec.Args, []string{"sh", "-c"}):
			requirementsPath := hostPathForContainerMount(t, spec.Args, "/requirements.txt")
			buildRequirementsPath = requirementsPath
			content, err := os.ReadFile(requirementsPath)
			if err != nil {
				t.Fatal(err)
			}
			buildRequirements = string(content)
			sourceMount = hostPathForContainerMount(t, spec.Args, "/source/demo-pkg")
			if !strings.Contains(spec.Args[len(spec.Args)-1], "cp -a /source/demo-pkg /wheelhouse/.source/demo-pkg") {
				t.Fatalf("local source prepare script missing copy:\n%s", spec.Args[len(spec.Args)-1])
			}
			if !strings.Contains(spec.Args[len(spec.Args)-1], "reploy_uv build") || !strings.Contains(spec.Args[len(spec.Args)-1], "--out-dir /wheelhouse /wheelhouse/.source/demo-pkg") {
				t.Fatalf("local source prepare script missing uv build:\n%s", spec.Args[len(spec.Args)-1])
			}
			if !strings.Contains(spec.Args[len(spec.Args)-1], uvBuildBackendRequirement) {
				t.Fatalf("local source prepare script should install pinned uv build backend:\n%s", spec.Args[len(spec.Args)-1])
			}
			wheelhouse := hostPathForContainerMount(t, spec.Args, "/wheelhouse")
			wheelhouseMount = wheelhouse
			if err := os.WriteFile(filepath.Join(wheelhouse, "demo_pkg-1.2.3-py3-none-any.whl"), []byte("demo\n"), 0o644); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(wheelhouse, "other_pkg-1.2.3-py3-none-any.whl"), []byte("other\n"), 0o644)
		case containsInOrder(spec.Args, []string{"install", "--no-cache-dir", "--progress-bar", "off", "--root-user-action", "ignore", "--target"}):
			requirementsPath := hostPathForContainerMount(t, spec.Args, "/requirements.txt")
			checkRequirementsPath = requirementsPath
			content, err := os.ReadFile(requirementsPath)
			if err != nil {
				t.Fatal(err)
			}
			checkRequirements = string(content)
			return nil
		case containsInOrder(spec.Args, []string{"run", "--rm", "--no-deps", "-e", "REPLOY_CONTAINER_COMMAND=__reploy_runtime_warmup", "app"}):
			return nil
		default:
			t.Fatalf("unexpected bundle command: %#v", spec.Args)
			return nil
		}
	})
	defer restore()

	if err := BundlePrepare(BundlePrepareOptions{Dir: deployDir}); err != nil {
		t.Fatal(err)
	}
	if buildRequirements != "setuptools>=68\nwheel\ndemo-pkg==1.2.3\nother-pkg==1.2.3\n" {
		t.Fatalf("build requirements = %q", buildRequirements)
	}
	if checkRequirements != "demo-pkg==1.2.3\nother-pkg==1.2.3\n" {
		t.Fatalf("check requirements = %q", checkRequirements)
	}
	if sourceMount != sourceDir {
		t.Fatalf("source mount = %q, want %q", sourceMount, sourceDir)
	}
	if !strings.HasPrefix(buildRequirementsPath, filepath.Join(deployDir, ".reploy")+string(filepath.Separator)) {
		t.Fatalf("build requirements path = %q, want under deployment .reploy", buildRequirementsPath)
	}
	if !strings.HasPrefix(checkRequirementsPath, filepath.Join(deployDir, ".reploy")+string(filepath.Separator)) {
		t.Fatalf("check requirements path = %q, want under deployment .reploy", checkRequirementsPath)
	}
	if !strings.HasPrefix(wheelhouseMount, filepath.Join(deployDir, ".reploy")+string(filepath.Separator)) {
		t.Fatalf("wheelhouse mount = %q, want under deployment .reploy", wheelhouseMount)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != expectedRuntimeRequirements {
		t.Fatalf("persistent requirements = %q", got)
	}
}

func TestBundlePrepareDryRunPrintsCommand(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := BundlePrepare(BundlePrepareOptions{Dir: deployDir, DryRun: true, Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"would build installation bundle:", "would warm Python runtime:", "__reploy_runtime_warmup"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing dry-run output %q:\n%s", want, stdout.String())
		}
	}
}

func TestBundlePrepareSuppressesCommandOutputByDefault(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var runOptions []RunOptions
	restore := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		runOptions = append(runOptions, options)
		switch {
		case containsInOrder(spec.Args, []string{"python", "-m", "pip", "--disable-pip-version-check", "wheel", "--no-cache-dir"}):
			wheelhouse := hostPathForContainerMount(t, spec.Args, "/wheelhouse")
			return os.WriteFile(filepath.Join(wheelhouse, "demo_suite-1.2.3-py3-none-any.whl"), []byte("suite\n"), 0o644)
		case containsInOrder(spec.Args, []string{"install", "--no-cache-dir", "--progress-bar", "off", "--root-user-action", "ignore", "--target"}):
			return nil
		case containsInOrder(spec.Args, []string{"run", "--rm", "--no-deps", "-e", "REPLOY_CONTAINER_COMMAND=__reploy_runtime_warmup", "app"}):
			return nil
		default:
			t.Fatalf("unexpected bundle command: %#v", spec.Args)
			return nil
		}
	})
	defer restore()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := BundlePrepare(BundlePrepareOptions{Dir: deployDir, Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	if len(runOptions) != 3 {
		t.Fatalf("ran %d commands, want build, check, and warm runtime", len(runOptions))
	}
	for _, options := range runOptions {
		if options.Context == nil {
			t.Fatalf("run options should include an interruptible context: %#v", options)
		}
		if options.Stdout != nil || options.Stderr != nil {
			t.Fatalf("run options should suppress output by default: %#v", options)
		}
	}
	if stdout.String() != "" || stderr.String() != "" {
		t.Fatalf("stdout=%q stderr=%q, want quiet", stdout.String(), stderr.String())
	}
}

func TestBundlePrepareVerboseStreamsCommandOutput(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var runOptions []RunOptions
	restore := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		runOptions = append(runOptions, options)
		switch {
		case containsInOrder(spec.Args, []string{"python", "-m", "pip", "--disable-pip-version-check", "wheel", "--no-cache-dir"}):
			command := strings.Join(spec.Args, " ")
			if strings.Contains(command, "--progress-bar raw") || strings.Contains(command, "reploy-bundle-pip") {
				return fmt.Errorf("verbose build command should not enable raw progress:\n%s", command)
			}
			if options.Stdout != &stdout || options.Stderr != &stderr {
				t.Fatalf("build command should stream verbose output: %#v", options)
			}
			wheelhouse := hostPathForContainerMount(t, spec.Args, "/wheelhouse")
			return os.WriteFile(filepath.Join(wheelhouse, "demo_suite-1.2.3-py3-none-any.whl"), []byte("suite\n"), 0o644)
		case containsInOrder(spec.Args, []string{"install", "--no-cache-dir", "--progress-bar", "off", "--root-user-action", "ignore", "--target"}):
			return nil
		case containsInOrder(spec.Args, []string{"run", "--rm", "--no-deps", "-e", "REPLOY_CONTAINER_COMMAND=__reploy_runtime_warmup", "app"}):
			if options.Stdout != &stdout || options.Stderr != &stderr {
				t.Fatalf("warmup command should stream verbose output: %#v", options)
			}
			return nil
		default:
			return fmt.Errorf("unexpected bundle command: %#v", spec.Args)
		}
	})
	defer restore()

	if err := BundlePrepare(BundlePrepareOptions{Dir: deployDir, Verbose: true, Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	if len(runOptions) != 3 {
		t.Fatalf("ran %d commands, want build, check, and warm runtime", len(runOptions))
	}
	for _, options := range runOptions {
		if options.Context == nil {
			t.Fatalf("run options should include an interruptible context: %#v", options)
		}
		if options.Stdout != &stdout || options.Stderr != &stderr {
			t.Fatalf("run options should stream verbose output: %#v", options)
		}
	}
	if !strings.Contains(stdout.String(), "built installation bundle:") {
		t.Fatalf("stdout missing verbose build message:\n%s", stdout.String())
	}
	for _, expected := range []string{
		"bundle build: prepare workspace...",
		"bundle build: build wheelhouse...",
		"bundle build timing:",
		"prepare workspace:",
		"copy existing bundle:",
		"prepare local sources:",
		"build wheelhouse:",
		"replace bundle:",
		"validate bundle:",
		"warm Python runtime:",
		"total:",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout missing timing line %q:\n%s", expected, stdout.String())
		}
	}
}

func TestBundlePrepareWarmRuntimeCreatesManagedFilePlaceholders(t *testing.T) {
	packDir := makeTestPackWithManifest(t, testPackManifestWithManagedFile())
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	commands := []string{}
	restore := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		switch {
		case containsInOrder(spec.Args, []string{"wheel", "--no-cache-dir"}):
			commands = append(commands, "build")
			wheelhouse := hostPathForContainerMount(t, spec.Args, "/wheelhouse")
			return os.WriteFile(filepath.Join(wheelhouse, "demo_suite-1.2.3-py3-none-any.whl"), []byte("suite\n"), 0o644)
		case containsInOrder(spec.Args, []string{"install", "--no-cache-dir", "--progress-bar", "off", "--root-user-action", "ignore", "--target"}):
			commands = append(commands, "check")
			return nil
		case containsInOrder(spec.Args, []string{"run", "--rm", "--no-deps", "-e", "REPLOY_CONTAINER_COMMAND=__reploy_runtime_warmup", "app"}):
			commands = append(commands, "warm runtime")
			return nil
		default:
			t.Fatalf("unexpected bundle command: %#v", spec.Args)
			return nil
		}
	})
	defer restore()

	if err := BundlePrepare(BundlePrepareOptions{Dir: deployDir}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(deployDir, ".arbiter.env")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("bundle prepare should create managed file placeholder for warmup: info=%v err=%v", info, err)
	}
	want := []string{"build", "check", "warm runtime"}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestPlanLocalWheelhouseBuildSkipsUnchangedSources(t *testing.T) {
	previousBundle := t.TempDir()
	workingBundle := t.TempDir()
	sources := []bundleBuildSource{
		makeWheelhousePlanSource(t, "demo-suite", "1.2.3"),
		makeWheelhousePlanSource(t, "demo-imap", "1.2.3"),
	}
	roots := []deploy.ArtifactRoot{
		python.PackageRoot("demo-suite==1.2.3"),
		python.PackageRoot("demo-imap==1.2.3"),
	}
	manifest := wheelhouseManifest{
		SchemaVersion:           1,
		RequirementsFingerprint: wheelhouseRequirementsFingerprint(roots, sources),
		LocalSources:            map[string]wheelhouseManifestSource{},
	}
	for _, source := range sources {
		fingerprint, err := localSourceFingerprint(source.HostDir)
		if err != nil {
			t.Fatal(err)
		}
		wheel := strings.ReplaceAll(source.Name, "-", "_") + "-1.2.3-py3-none-any.whl"
		manifest.LocalSources[source.Name] = wheelhouseManifestSource{Fingerprint: fingerprint, Wheel: wheel}
		if err := os.WriteFile(filepath.Join(previousBundle, wheel), []byte(source.Name), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workingBundle, wheel), []byte(source.Name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeRawWheelhouseManifest(previousBundle, manifest); err != nil {
		t.Fatal(err)
	}

	plan, err := planLocalWheelhouseBuild(roots, sources, previousBundle, workingBundle)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.SkipBuild || !plan.NoIndex || len(plan.StaleSources) != 0 {
		t.Fatalf("plan = %#v", plan)
	}
	requirements, err := localBuildRequirements(roots, sources, plan.StaleSourceNames)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(requirements), "/wheelhouse/.source/") {
		t.Fatalf("unchanged requirements should use existing wheel pins:\n%s", requirements)
	}
}

func TestBundlePreparePipWheelhouseUsesPipForLocalSourceBuilds(t *testing.T) {
	packDir := makeTestPackWithManifest(t, strings.Replace(testPackManifest(), "    identifier: demo-suite\n", "    identifier: demo-suite\n    local_sources:\n      demo-pkg: local/demo-pkg\n", 1))
	sourceDir := filepath.Join(packDir, "local", "demo-pkg")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pyproject.toml"), []byte("[build-system]\nrequires = [\"setuptools>=68\", \"wheel\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref, Requirements: []string{"demo-pkg==1.2.3"}}); err != nil {
		t.Fatal(err)
	}

	var buildRequirements string
	var buildScript string
	restore := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		switch {
		case containsInOrder(spec.Args, []string{"sh", "-c"}):
			requirementsPath := hostPathForContainerMount(t, spec.Args, "/requirements.txt")
			content, err := os.ReadFile(requirementsPath)
			if err != nil {
				t.Fatal(err)
			}
			buildRequirements = string(content)
			buildScript = spec.Args[len(spec.Args)-1]
			wheelhouse := hostPathForContainerMount(t, spec.Args, "/wheelhouse")
			return os.WriteFile(filepath.Join(wheelhouse, "demo_pkg-1.2.3-py3-none-any.whl"), []byte("demo\n"), 0o644)
		case containsInOrder(spec.Args, []string{"install", "--no-cache-dir", "--progress-bar", "off", "--root-user-action", "ignore", "--target"}):
			return nil
		case containsInOrder(spec.Args, []string{"run", "--rm", "--no-deps", "-e", "REPLOY_CONTAINER_COMMAND=__reploy_runtime_warmup", "app"}):
			return nil
		default:
			t.Fatalf("unexpected bundle command: %#v", spec.Args)
			return nil
		}
	})
	defer restore()

	if err := BundlePrepare(BundlePrepareOptions{Dir: deployDir, WheelhouseBackend: string(WheelhouseBackendPip)}); err != nil {
		t.Fatal(err)
	}
	if buildRequirements != "setuptools>=68\nwheel\n/wheelhouse/.source/demo-pkg\n" {
		t.Fatalf("build requirements = %q", buildRequirements)
	}
	if strings.Contains(buildScript, "uv build") {
		t.Fatalf("pip wheelhouse should not invoke uv:\n%s", buildScript)
	}
}

func TestBundleBackendsRejectPipWheelhouseWithUVBuildBackend(t *testing.T) {
	_, _, err := normalizeBundleBackends(string(WheelhouseBackendPip), string(PythonBuildBackendUV))
	if err == nil {
		t.Fatal("expected incompatible backend error")
	}
	if !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("error = %v", err)
	}
}

func TestPlanLocalWheelhouseBuildRebuildsChangedSourceOnly(t *testing.T) {
	previousBundle := t.TempDir()
	workingBundle := t.TempDir()
	sources := []bundleBuildSource{
		makeWheelhousePlanSource(t, "demo-suite", "1.2.3"),
		makeWheelhousePlanSource(t, "demo-imap", "1.2.3"),
	}
	roots := []deploy.ArtifactRoot{
		python.PackageRoot("demo-suite==1.2.3"),
		python.PackageRoot("demo-imap==1.2.3"),
	}
	manifest := wheelhouseManifest{
		SchemaVersion:           1,
		RequirementsFingerprint: wheelhouseRequirementsFingerprint(roots, sources),
		LocalSources:            map[string]wheelhouseManifestSource{},
	}
	for _, source := range sources {
		fingerprint, err := localSourceFingerprint(source.HostDir)
		if err != nil {
			t.Fatal(err)
		}
		wheel := strings.ReplaceAll(source.Name, "-", "_") + "-1.2.3-py3-none-any.whl"
		manifest.LocalSources[source.Name] = wheelhouseManifestSource{Fingerprint: fingerprint, Wheel: wheel}
		if err := os.WriteFile(filepath.Join(previousBundle, wheel), []byte(source.Name), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workingBundle, wheel), []byte(source.Name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeRawWheelhouseManifest(previousBundle, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sources[1].HostDir, "src", "changed.py"), []byte("changed = True\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := planLocalWheelhouseBuild(roots, sources, previousBundle, workingBundle)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SkipBuild || !plan.NoIndex || len(plan.StaleSources) != 1 || plan.StaleSources[0].Name != "demo-imap" {
		t.Fatalf("plan = %#v", plan)
	}
	if _, err := os.Stat(filepath.Join(workingBundle, "demo_imap-1.2.3-py3-none-any.whl")); !os.IsNotExist(err) {
		t.Fatalf("stale local wheel should be removed before rebuild: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workingBundle, "demo_suite-1.2.3-py3-none-any.whl")); err != nil {
		t.Fatalf("unchanged local wheel should remain: %v", err)
	}
	requirements, err := localBuildRequirements(roots, sources, plan.StaleSourceNames)
	if err != nil {
		t.Fatal(err)
	}
	text := string(requirements)
	if strings.Contains(text, "/wheelhouse/.source/demo-suite") || !strings.Contains(text, "/wheelhouse/.source/demo-imap") {
		t.Fatalf("requirements should rebuild only demo-imap:\n%s", text)
	}
}

func TestLocalSourceFingerprintSkipsGeneratedDirectories(t *testing.T) {
	source := makeWheelhousePlanSource(t, "demo-suite", "1.2.3")
	before, err := localSourceFingerprint(source.HostDir)
	if err != nil {
		t.Fatal(err)
	}
	generatedFiles := []string{
		filepath.Join(source.HostDir, ".venv", "lib", "python3.11", "site-packages", "demo.py"),
		filepath.Join(source.HostDir, ".nox", "tests", "tmp.py"),
		filepath.Join(source.HostDir, "node_modules", "left-pad", "index.js"),
		filepath.Join(source.HostDir, ".pytest_cache", "v", "cache", "nodeids"),
	}
	for _, path := range generatedFiles {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("generated\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	after, err := localSourceFingerprint(source.HostDir)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("fingerprint changed after generated directories: before=%s after=%s", before, after)
	}
}

func makeWheelhousePlanSource(t *testing.T, name string, version string) bundleBuildSource {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	packageDir := filepath.Join(dir, "src", strings.ReplaceAll(name, "-", "_"))
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pyproject := fmt.Sprintf(`[build-system]
requires = ["setuptools>=68", "wheel"]
build-backend = "setuptools.build_meta"

[project]
name = %q
version = %q
`, name, version)
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(pyproject), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "__init__.py"), []byte("__version__ = "+strconv.Quote(version)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	normalized := python.NormalizeRequirementName(name)
	return bundleBuildSource{
		Name:              normalized,
		HostDir:           dir,
		ContainerDir:      "/source/" + normalized,
		BuildDir:          "/wheelhouse/.source/" + normalized,
		BuildRequirements: []string{"setuptools>=68", "wheel"},
	}
}

func writeRawWheelhouseManifest(dir string, manifest wheelhouseManifest) error {
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	return os.WriteFile(filepath.Join(dir, wheelhouseManifestName), content, 0o644)
}

func TestBundleWarmRuntimeBuildsIfNeededAndRunsWarmup(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	commands := []string{}
	restore := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		switch {
		case containsInOrder(spec.Args, []string{"wheel", "--no-cache-dir"}):
			commands = append(commands, "build")
			wheelhouse := hostPathForContainerMount(t, spec.Args, "/wheelhouse")
			return os.WriteFile(filepath.Join(wheelhouse, "demo_suite-1.2.3-py3-none-any.whl"), []byte("suite\n"), 0o644)
		case containsInOrder(spec.Args, []string{"install", "--no-cache-dir", "--progress-bar", "off", "--root-user-action", "ignore", "--target"}):
			commands = append(commands, "check")
			return nil
		case containsInOrder(spec.Args, []string{"run", "--rm", "--no-deps", "-e", "REPLOY_CONTAINER_COMMAND=__reploy_runtime_warmup", "app"}):
			commands = append(commands, "warm runtime")
			compose := readFile(t, filepath.Join(deployDir, ComposeFileName))
			if !strings.Contains(compose, "__reploy_runtime_warmup") {
				t.Fatalf("runtime compose missing warmup exit path:\n%s", compose)
			}
			return nil
		default:
			t.Fatalf("unexpected bundle command: %#v", spec.Args)
			return nil
		}
	})
	defer restore()

	if err := BundleWarmRuntime(BundleWarmRuntimeOptions{Dir: deployDir}); err != nil {
		t.Fatal(err)
	}
	want := []string{"build", "check", "warm runtime"}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestBundleWarmRuntimeInitializesNamedRuntimeVolume(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	markTestBundlePrepared(t, deployDir)
	if _, err := upsertDockerEnvValues(deployDir, map[string]string{
		"REPLOY_RUNTIME_DIR":    "reploy-runtime-test",
		"REPLOY_CONTAINER_USER": "123:456",
	}); err != nil {
		t.Fatal(err)
	}

	var commands []string
	restoreBundle := stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		if containsInOrder(spec.Args, []string{"run", "--rm", "--no-deps", "-e", "REPLOY_CONTAINER_COMMAND=__reploy_runtime_warmup", "app"}) {
			commands = append(commands, "warm runtime")
			return nil
		}
		t.Fatalf("unexpected bundle command: %#v", spec.Args)
		return nil
	})
	defer restoreBundle()
	oldRunRuntimeVolumeInitCommand := runRuntimeVolumeInitCommand
	runRuntimeVolumeInitCommand = func(spec CommandSpec, options RunOptions) error {
		if containsInOrder(spec.Args, []string{"volume", "create", "reploy-runtime-test"}) {
			commands = append(commands, "create volume")
			return nil
		}
		if !containsInOrder(spec.Args, []string{"run", "--rm", "--no-deps", "--user", "0"}) {
			t.Fatalf("runtime volume init args = %#v", spec.Args)
		}
		if !containsInOrder(spec.Args, []string{"-e", "REPLOY_RUNTIME_OWNER=123:456", "app"}) {
			t.Fatalf("runtime volume init owner args = %#v", spec.Args)
		}
		commands = append(commands, "init")
		return nil
	}
	defer func() { runRuntimeVolumeInitCommand = oldRunRuntimeVolumeInitCommand }()

	if err := BundleWarmRuntime(BundleWarmRuntimeOptions{Dir: deployDir}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(commands, []string{"create volume", "init", "warm runtime"}) {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestBundleWarmRuntimeDryRunDoesNotMaterializeRuntimeCompose(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, ComposeFileName), []byte("stale compose\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	if err := BundleWarmRuntime(BundleWarmRuntimeOptions{Dir: deployDir, DryRun: true, Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"would build installation bundle:", "would warm Python runtime:", "__reploy_runtime_warmup"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
	if got := readFile(t, filepath.Join(deployDir, ComposeFileName)); got != "stale compose\n" {
		t.Fatalf("dry-run rewrote compose:\n%s", got)
	}
}

func TestEnsureBundlePreparedBuildsOnceAndBundleAddInvalidates(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	commands := []string{}
	restore := stubSuccessfulBundlePrepare(t, &commands)
	defer restore()

	built, err := EnsureBundlePrepared(BundleEnsureOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	if !built {
		t.Fatal("first ensure should build")
	}
	if len(commands) != 3 {
		t.Fatalf("ran %d commands, want build, check, and warm runtime", len(commands))
	}
	state := readDeploymentState(t, deployDir)
	if state.Bundle.PreparedFingerprint == "" {
		t.Fatal("prepared fingerprint was not recorded")
	}

	built, err = EnsureBundlePrepared(BundleEnsureOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	if built {
		t.Fatal("second ensure should reuse prepared bundle")
	}
	if len(commands) != 3 {
		t.Fatalf("ran %d commands after cached ensure, want 3", len(commands))
	}

	if _, err := BundleAdd(BundleRootOptions{Dir: deployDir, Source: "demo-imap==1.2.3"}); err != nil {
		t.Fatal(err)
	}
	state = readDeploymentState(t, deployDir)
	if state.Bundle.PreparedFingerprint != "" {
		t.Fatalf("prepared fingerprint should be cleared after bundle add: %q", state.Bundle.PreparedFingerprint)
	}

	built, err = EnsureBundlePrepared(BundleEnsureOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	if !built {
		t.Fatal("ensure after bundle add should rebuild")
	}
	if len(commands) != 6 {
		t.Fatalf("ran %d commands after invalidation, want 6", len(commands))
	}
}

func TestBundleCleanInvalidatesPreparedBundle(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	commands := []string{}
	restore := stubSuccessfulBundlePrepare(t, &commands)
	defer restore()

	if _, err := EnsureBundlePrepared(BundleEnsureOptions{Dir: deployDir}); err != nil {
		t.Fatal(err)
	}
	if got := readDeploymentState(t, deployDir).Bundle.PreparedFingerprint; got == "" {
		t.Fatal("prepared fingerprint was not recorded")
	}

	oldRunBundleCleanDockerCommand := runBundleCleanDockerCommand
	runBundleCleanDockerCommand = func(CommandSpec, RunOptions) error { return nil }
	defer func() { runBundleCleanDockerCommand = oldRunBundleCleanDockerCommand }()

	results, err := BundleClean(BundleCleanOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	assertResultStatus(t, results, filepath.Join(deployDir, StateFileName), deploy.UpdateStatusUpdated)
	if got := readDeploymentState(t, deployDir).Bundle.PreparedFingerprint; got != "" {
		t.Fatalf("prepared fingerprint should be cleared after clean: %q", got)
	}
}

func TestBundleCleanRemovesBundleDirectory(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	bundleDir := filepath.Join(deployDir, BundleDirName)
	builtWheel := filepath.Join(deployDir, BundleDirName, "demo_suite-1.2.3-py3-none-any.whl")
	if err := os.WriteFile(builtWheel, []byte("built\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generatedMetadata := filepath.Join(deployDir, BundleDirName, "pip-report.json")
	if err := os.WriteFile(generatedMetadata, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generatedDir := filepath.Join(deployDir, BundleDirName, ".source")
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeCompose := filepath.Join(deployDir, ComposeFileName)
	values, err := readDockerEnv(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	runtimeVolume := values["REPLOY_RUNTIME_DIR"]
	oldRunBundleCleanDockerCommand := runBundleCleanDockerCommand
	var cleanCommands []CommandSpec
	runBundleCleanDockerCommand = func(spec CommandSpec, options RunOptions) error {
		if _, err := os.Stat(bundleDir); err != nil {
			t.Fatalf("bundle dir should still exist while runtime volume is removed: %v", err)
		}
		cleanCommands = append(cleanCommands, spec)
		return nil
	}
	defer func() { runBundleCleanDockerCommand = oldRunBundleCleanDockerCommand }()

	results, err := BundleClean(BundleCleanOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	assertResultStatus(t, results, bundleDir, deploy.UpdateStatusRemoved)
	assertResultStatus(t, results, runtimeVolume, deploy.UpdateStatusRemoved)
	if _, err := os.Stat(bundleDir); !os.IsNotExist(err) {
		t.Fatalf("bundle dir still exists: %v", err)
	}
	if len(cleanCommands) != 1 || !reflect.DeepEqual(cleanCommands[0].Args, []string{"volume", "rm", "-f", runtimeVolume}) {
		t.Fatalf("runtime clean commands = %#v", cleanCommands)
	}
	if _, err := os.Stat(runtimeCompose); err != nil {
		t.Fatalf("runtime compose should be preserved: %v", err)
	}
}

func TestBundleCleanLeavesPreparedBundleWhenRuntimeVolumeRemovalFails(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	bundleDir := filepath.Join(deployDir, BundleDirName)
	if err := os.WriteFile(filepath.Join(bundleDir, "demo_suite-1.2.3-py3-none-any.whl"), []byte("built\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	markTestBundlePrepared(t, deployDir)

	oldRunBundleCleanDockerCommand := runBundleCleanDockerCommand
	runBundleCleanDockerCommand = func(CommandSpec, RunOptions) error {
		return errors.New("docker unavailable")
	}
	defer func() { runBundleCleanDockerCommand = oldRunBundleCleanDockerCommand }()

	_, err = BundleClean(BundleCleanOptions{Dir: deployDir})
	if err == nil || !strings.Contains(err.Error(), "remove runtime volume") {
		t.Fatalf("BundleClean error = %v, want runtime volume cleanup failure", err)
	}
	if _, statErr := os.Stat(bundleDir); statErr != nil {
		t.Fatalf("bundle dir should remain after failed runtime volume cleanup: %v", statErr)
	}
	if got := readDeploymentState(t, deployDir).Bundle.PreparedFingerprint; got == "" {
		t.Fatal("prepared fingerprint should remain after failed runtime volume cleanup")
	}
}

func TestBundleCleanMissingWheelhouseIsUpToDate(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(deployDir, BundleDirName)); err != nil {
		t.Fatal(err)
	}
	results, err := BundleClean(BundleCleanOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %#v, want none", results)
	}
}

func TestBundleAddAcceptsWheelAndSourceRoots(t *testing.T) {
	for _, source := range []string{"/opt/wheels/demo.whl", "/source/app/server"} {
		t.Run(source, func(t *testing.T) {
			root, err := classifyBundleRoot(source)
			if err != nil {
				t.Fatal(err)
			}
			if root.Provider != "python" || root.Source != source {
				t.Fatalf("root = %#v", root)
			}
		})
	}
}

func TestBundleAddAcceptsUnpinnedPackageRoot(t *testing.T) {
	root, err := classifyBundleRoot("demo-imap")
	if err != nil {
		t.Fatal(err)
	}
	if root.Kind != "package" || root.Source != "demo-imap" {
		t.Fatalf("root = %#v", root)
	}
}

func TestBundleAddRejectsInvalidRelativePathRoot(t *testing.T) {
	_, err := classifyBundleRoot("dist/demo.whl")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "package name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBundleRemoveSupportsUnpinnedPackRoots(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	if _, err := BundleRemove(BundleRootOptions{Dir: deployDir, Source: "demo-suite"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestUpdateInfersBundleRootsForOldState(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	state := readDeploymentState(t, deployDir)
	state.Bundle = deploy.BundleState{}
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, StateFileName), append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Update(UpdateOptions{Dir: deployDir}); err != nil {
		t.Fatal(err)
	}
	state = readDeploymentState(t, deployDir)
	if len(state.Bundle.Roots) != 1 || state.Bundle.Roots[0].Source != "demo-suite" {
		t.Fatalf("bundle roots = %#v", state.Bundle.Roots)
	}
}

func readDeploymentState(t *testing.T, dir string) deploy.DeploymentState {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	var state deploy.DeploymentState
	if err := json.Unmarshal(content, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func stubBundleRunner(run func(CommandSpec, RunOptions) error) func() {
	previousBundle := runBundleCommand
	previousRuntimeVolumeInit := runRuntimeVolumeInitCommand
	runBundleCommand = run
	runRuntimeVolumeInitCommand = func(CommandSpec, RunOptions) error { return nil }
	return func() {
		runBundleCommand = previousBundle
		runRuntimeVolumeInitCommand = previousRuntimeVolumeInit
	}
}

func stubSuccessfulBundlePrepare(t *testing.T, commands *[]string) func() {
	t.Helper()
	return stubBundleRunner(func(spec CommandSpec, options RunOptions) error {
		switch {
		case containsInOrder(spec.Args, []string{"wheel", "--no-cache-dir"}):
			*commands = append(*commands, "build")
			wheelhouse := hostPathForContainerMount(t, spec.Args, "/wheelhouse")
			return os.WriteFile(filepath.Join(wheelhouse, "demo_suite-1.2.3-py3-none-any.whl"), []byte("suite\n"), 0o644)
		case containsInOrder(spec.Args, []string{"install", "--no-cache-dir", "--progress-bar", "off", "--root-user-action", "ignore", "--target"}):
			*commands = append(*commands, "check")
			return nil
		case containsInOrder(spec.Args, []string{"run", "--rm", "--no-deps", "-e", "REPLOY_CONTAINER_COMMAND=__reploy_runtime_warmup", "app"}):
			*commands = append(*commands, "warm runtime")
			return nil
		default:
			t.Fatalf("unexpected bundle command: %#v", spec.Args)
			return nil
		}
	})
}

func markTestBundlePrepared(t *testing.T, dir string) {
	t.Helper()
	bundleDir := filepath.Join(dir, BundleDirName)
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "demo_suite-1.2.3-py3-none-any.whl"), []byte("suite\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := readDeploymentState(t, dir)
	state.Bundle.PreparedFingerprint = bundlePreparedFingerprint(state)
	if _, err := writeDeploymentStateIfChanged(dir, state); err != nil {
		t.Fatal(err)
	}
}

func hostPathForContainerMount(t *testing.T, args []string, containerPath string) string {
	t.Helper()
	for index := 0; index < len(args)-1; index++ {
		if args[index] != "-v" {
			continue
		}
		spec := args[index+1]
		for _, suffix := range []string{":" + containerPath, ":" + containerPath + ":ro", ":" + containerPath + ":rw"} {
			if strings.HasSuffix(spec, suffix) {
				return strings.TrimSuffix(spec, suffix)
			}
		}
	}
	t.Fatalf("container mount %s not found in %#v", containerPath, args)
	return ""
}

func TestHostPathForContainerMountAcceptsWindowsDrivePath(t *testing.T) {
	args := []string{"run", "-v", `C:\Users\runner\AppData\Local\Temp:/wheelhouse`, "python:3.11-slim"}
	if got := hostPathForContainerMount(t, args, "/wheelhouse"); got != `C:\Users\runner\AppData\Local\Temp` {
		t.Fatalf("host mount = %q", got)
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
