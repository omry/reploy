package toolcatalog

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func solverTestOperationV1() ResolutionOperationInputsV1 {
	envelope := func(schema string) canonical.Envelope {
		return canonical.Envelope{Schema: schema, Value: canonical.Object{"identity": schema}}
	}
	return ResolutionOperationInputsV1{
		Blueprint: envelope("test-blueprint-v1"),
		Reploy:    envelope("test-reploy-v1"),
		Platform:  envelope("test-platform-v1"),
		Catalog:   envelope("test-catalog-v1"),
		ActiveProviders: ActiveProviderConstraintsV1{
			Schema: ActiveProviderConstraintsSchemaV1, Sources: []ActiveProviderConstraintSourceV1{},
		},
	}
}

func solverTestActiveProvidersV1(sources ...ActiveProviderConstraintSourceV1) ActiveProviderConstraintsV1 {
	if sources == nil {
		sources = []ActiveProviderConstraintSourceV1{}
	}
	return ActiveProviderConstraintsV1{Schema: ActiveProviderConstraintsSchemaV1, Sources: sources}
}

func solverTestActiveSourceV1(scope string, kind string, id string) ActiveProviderConstraintSourceV1 {
	return ActiveProviderConstraintSourceV1{
		Scope: scope, Source: ConstraintSourceV1{Kind: kind, ID: id},
		NativePackages: []ActiveNativePackageConstraintV1{},
		PythonBindings: []ActivePythonBindingConstraintV1{}, InstallRoots: []string{},
		OwnedPaths: []ActiveFilesystemConstraintV1{}, Artifacts: []ActiveFilesystemConstraintV1{},
		Environment: []RecordEnvironmentVariableV1{}, Exports: []ToolExportV1{}, Capabilities: []ToolExportV1{},
	}
}

func solverTestDomainsV1(sharedExports bool) []ProviderDomainSetV1 {
	application := ProviderDomainSetV1{
		Scope: "application", PackageManager: "application/packages",
		Filesystem: "application/filesystem", Environment: "application/environment",
		Exports: "application/exports", Capabilities: "application/capabilities",
	}
	source := ProviderDomainSetV1{
		Scope: "source-builder", PackageManager: "source-builder/packages",
		Filesystem: "source-builder/filesystem", Environment: "source-builder/environment",
		Exports: "source-builder/exports", Capabilities: "source-builder/capabilities",
	}
	if sharedExports {
		application.Exports = "shared/exports"
		application.Capabilities = "shared/capabilities"
		source.Exports = application.Exports
		source.Capabilities = application.Capabilities
	}
	return []ProviderDomainSetV1{application, source}
}

func solverTestCandidateSetsV1(t *testing.T, catalog *CatalogV1) (ReleaseCandidateSetV1, ReleaseCandidateSetV1) {
	t.Helper()
	addCandidateReleaseV1(t, catalog, "2.0.0", func(contract *ReleaseContractV1) {
		contract.Exports = []ToolExportV1{{Name: "demo", Path: "/opt/demo-v2/bin/demo"}}
	}, nil)

	applicationGroup := candidateTestGroupV1()
	applicationGroup.VersionConstraints = []string{"1.2.3"}
	applicationCandidates, err := catalog.SelectReleaseCandidatesV1(
		applicationGroup, candidateTestObservedV1(), candidateTestClientV1(), nil)
	if err != nil {
		t.Fatalf("select application candidate: %v", err)
	}
	sourceGroup := candidateTestGroupV1()
	sourceGroup.Scope = "source-builder"
	sourceCandidates, err := catalog.SelectReleaseCandidatesV1(
		sourceGroup, candidateTestObservedV1(), candidateTestClientV1(), nil)
	if err != nil {
		t.Fatalf("select source-builder candidates: %v", err)
	}
	if len(applicationCandidates) != 1 || len(sourceCandidates) != 2 ||
		sourceCandidates[0].Manifest.Version != "2.0.0" {
		t.Fatalf("unexpected candidate fixture: application=%d source=%v",
			len(applicationCandidates), candidateVersionsV1(sourceCandidates))
	}
	return ReleaseCandidateSetV1{Group: applicationGroup, Candidates: applicationCandidates},
		ReleaseCandidateSetV1{Group: sourceGroup, Candidates: sourceCandidates}
}

func candidateVersionsV1(candidates []ReleaseCandidateV1) []string {
	versions := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		versions = append(versions, candidate.Manifest.Version)
	}
	return versions
}

func solverTestAddRecordV1(t *testing.T, catalog *CatalogV1, value any) RecordReferenceV1 {
	t.Helper()
	digest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, value)
	if err != nil {
		t.Fatal(err)
	}
	id := recordIDV1(value)
	catalog.records[recordKeyV1{ID: id, Digest: digest}] = loadedRecordV1{
		ID: id, Schema: recordSchemaV1(value), Digest: digest, Value: value,
	}
	return RecordReferenceV1{ID: id, Digest: digest}
}

func TestResolveSelectedClosuresBacktracksDeterministicallyAcrossSharedDomainsV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	application, source := solverTestCandidateSetsV1(t, catalog)
	domains := solverTestDomainsV1(true)

	first, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{source, application}, []ProviderDomainSetV1{domains[1], domains[0]},
		solverTestOperationV1())
	if err != nil {
		t.Fatalf("resolve reversed input: %v", err)
	}
	second, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{application, source}, domains, solverTestOperationV1())
	if err != nil {
		t.Fatalf("resolve canonical input: %v", err)
	}
	if len(first.Closures) != 2 || first.Closures[0].Scope != "application" ||
		first.Closures[1].Scope != "source-builder" {
		t.Fatalf("closure order = %+v, want application then source-builder", first.Closures)
	}
	if first.Closures[0].Provenance.Version != "1.2.3" ||
		first.Closures[1].Provenance.Version != "1.2.3" {
		t.Errorf("chosen versions = %s, %s; want shared-domain fallback to 1.2.3",
			first.Closures[0].Provenance.Version, first.Closures[1].Provenance.Version)
	}
	if first.VisitedStates != "3" {
		t.Errorf("visited states = %s, want 3 after one conflicting branch", first.VisitedStates)
	}
	if first.Snapshot.Digest != second.Snapshot.Digest ||
		first.Snapshot.CanonicalJSON != second.Snapshot.CanonicalJSON {
		t.Error("request or provider-domain input order changed the finalized operation")
	}
}

func TestResolveSelectedClosuresFallsBackAcrossDefinitionRevisionsV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	application, source := solverTestCandidateSetsV1(t, catalog)
	domains := solverTestDomainsV1(true)

	compatible := source.Candidates[1]
	compatible.Manifest = cloneReleaseManifestV1(&compatible.Manifest)
	compatible.Manifest.ID = "tool:demo/releases/1.2.3/revisions/2/manifest"
	compatible.Manifest.Revision = "2"
	conflicting := compatible
	conflicting.Manifest = cloneReleaseManifestV1(&compatible.Manifest)
	conflicting.Manifest.ID = "tool:demo/releases/1.2.3/revisions/10/manifest"
	conflicting.Manifest.Revision = "10"
	conflicting.Contract = cloneReleaseContractV1(&compatible.Contract)
	conflicting.Contract.Exports = []ToolExportV1{{Name: "demo", Path: "/opt/conflicting/bin/demo"}}
	conflicting.Exports = []ToolExportV1{{Name: "demo", Path: "/opt/conflicting/bin/demo"}}
	source.Candidates = []ReleaseCandidateV1{compatible, conflicting}

	result, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{source, application}, []ProviderDomainSetV1{domains[1], domains[0]},
		solverTestOperationV1())
	if err != nil {
		t.Fatal(err)
	}
	if result.Closures[1].Provenance.Version != "1.2.3" ||
		result.Closures[1].Provenance.Revision != "2" {
		t.Errorf("source choice = %s~%s, want fallback from revision 10 to 2",
			result.Closures[1].Provenance.Version, result.Closures[1].Provenance.Revision)
	}
	if result.VisitedStates != "3" {
		t.Errorf("visited states = %s, want 3 after the newer revision conflicts", result.VisitedStates)
	}
}

func TestResolveSelectedClosuresKeepsIsolatedScopesIndependentV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	application, source := solverTestCandidateSetsV1(t, catalog)
	result, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{source, application}, solverTestDomainsV1(false), solverTestOperationV1())
	if err != nil {
		t.Fatal(err)
	}
	if result.Closures[0].Provenance.Version != "1.2.3" ||
		result.Closures[1].Provenance.Version != "2.0.0" {
		t.Errorf("isolated choices = %s, %s; want application 1.2.3 and source-builder 2.0.0",
			result.Closures[0].Provenance.Version, result.Closures[1].Provenance.Version)
	}
}

func TestAssignmentClaimsRespectEveryProviderDomainPartitionV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	applicationSet, sourceSet := solverTestCandidateSetsV1(t, catalog)
	applicationBase := applicationSet.Candidates[0]
	sourceBase := sourceSet.Candidates[1]

	packageLeft := cloneNativePackageSetV1(validRecordValuesV1()[8].(*NativePackageSetV1))
	packageLeft.ID = "tool:demo/releases/1.2.3/package-sets/solver-left"
	packageLeft.Requirements = []string{"demo=1"}
	packageRight := cloneNativePackageSetV1(&packageLeft)
	packageRight.ID = "tool:demo/releases/1.2.3/package-sets/solver-right"
	packageRight.Requirements = []string{"demo=2"}
	packageLeftReference := solverTestAddRecordV1(t, catalog, &packageLeft)
	packageRightReference := solverTestAddRecordV1(t, catalog, &packageRight)

	payloadLeft := clonePayloadRecordV1(validRecordValuesV1()[6].(*PayloadRecordV1))
	payloadLogicalRight := clonePayloadRecordV1(&payloadLeft)
	payloadLogicalRight.Size = "43"
	payloadInstallRight := clonePayloadRecordV1(&payloadLogicalRight)
	payloadInstallRight.LogicalPath = "tools/demo/chromium-alt.zip"
	payloadLeftReference := solverTestAddRecordV1(t, catalog, &payloadLeft)
	payloadLogicalRightReference := solverTestAddRecordV1(t, catalog, &payloadLogicalRight)
	payloadInstallRightReference := solverTestAddRecordV1(t, catalog, &payloadInstallRight)

	artifactLeft := cloneBindingArtifactV1(validRecordValuesV1()[5].(*BindingArtifactRecordV1))
	artifactRight := cloneBindingArtifactV1(&artifactLeft)
	artifactRight.Size = "43"
	artifactLeftReference := solverTestAddRecordV1(t, catalog, &artifactLeft)
	artifactRightReference := solverTestAddRecordV1(t, catalog, &artifactRight)

	bindingLeft := cloneBindingContractV1(validRecordValuesV1()[4].(*BindingContractV1))
	bindingLeft.Requirements = []string{"demo==1"}
	bindingRight := cloneBindingContractV1(&bindingLeft)
	bindingRight.Requirements = []string{"demo==2"}
	bindingLeftReference := solverTestAddRecordV1(t, catalog, &bindingLeft)
	bindingRightReference := solverTestAddRecordV1(t, catalog, &bindingRight)

	for _, testCase := range []struct {
		name   string
		want   string
		mutate func(*ReleaseCandidateV1, *ReleaseCandidateV1)
		share  func(*ProviderDomainSetV1, *ProviderDomainSetV1)
	}{
		{
			name: "package manager", want: "package requirement conflict",
			mutate: func(left *ReleaseCandidateV1, right *ReleaseCandidateV1) {
				left.Contributions = []RecordReferenceV1{packageLeftReference}
				right.Contributions = []RecordReferenceV1{packageRightReference}
			},
			share: func(left *ProviderDomainSetV1, right *ProviderDomainSetV1) {
				right.PackageManager = left.PackageManager
			},
		},
		{
			name: "payload logical path", want: "artifact logical path conflict",
			mutate: func(left *ReleaseCandidateV1, right *ReleaseCandidateV1) {
				left.Contributions = []RecordReferenceV1{payloadLeftReference}
				right.Contributions = []RecordReferenceV1{payloadLogicalRightReference}
			},
			share: func(left *ProviderDomainSetV1, right *ProviderDomainSetV1) {
				right.Filesystem = left.Filesystem
			},
		},
		{
			name: "payload install directory", want: "filesystem conflict",
			mutate: func(left *ReleaseCandidateV1, right *ReleaseCandidateV1) {
				left.Contributions = []RecordReferenceV1{payloadLeftReference}
				right.Contributions = []RecordReferenceV1{payloadInstallRightReference}
			},
			share: func(left *ProviderDomainSetV1, right *ProviderDomainSetV1) {
				right.Filesystem = left.Filesystem
			},
		},
		{
			name: "binding artifact", want: "artifact logical path conflict",
			mutate: func(left *ReleaseCandidateV1, right *ReleaseCandidateV1) {
				left.Contributions = []RecordReferenceV1{artifactLeftReference}
				right.Contributions = []RecordReferenceV1{artifactRightReference}
			},
			share: func(left *ProviderDomainSetV1, right *ProviderDomainSetV1) {
				right.Filesystem = left.Filesystem
			},
		},
		{
			name: "binding requirement", want: "binding requirement conflict",
			mutate: func(left *ReleaseCandidateV1, right *ReleaseCandidateV1) {
				left.Contributions = []RecordReferenceV1{bindingLeftReference}
				right.Contributions = []RecordReferenceV1{bindingRightReference}
			},
			share: func(left *ProviderDomainSetV1, right *ProviderDomainSetV1) {
				right.PackageManager = left.PackageManager
			},
		},
		{
			name: "filesystem", want: "filesystem conflict",
			mutate: func(left *ReleaseCandidateV1, right *ReleaseCandidateV1) {
				left.Contract.Runtime = &RecordRuntimeV1{InstallRoot: "/opt/demo"}
				right.Contract.Runtime = &RecordRuntimeV1{InstallRoot: "/opt/demo/bin"}
			},
			share: func(left *ProviderDomainSetV1, right *ProviderDomainSetV1) {
				right.Filesystem = left.Filesystem
			},
		},
		{
			name: "environment", want: "environment conflict",
			mutate: func(left *ReleaseCandidateV1, right *ReleaseCandidateV1) {
				left.Contract.Runtime = &RecordRuntimeV1{Environment: []RecordEnvironmentVariableV1{{Name: "DEMO_HOME", Value: "left"}}}
				right.Contract.Runtime = &RecordRuntimeV1{Environment: []RecordEnvironmentVariableV1{{Name: "DEMO_HOME", Value: "right"}}}
			},
			share: func(left *ProviderDomainSetV1, right *ProviderDomainSetV1) {
				right.Environment = left.Environment
			},
		},
		{
			name: "export", want: "export conflict",
			mutate: func(left *ReleaseCandidateV1, right *ReleaseCandidateV1) {
				left.Exports = []ToolExportV1{{Name: "demo", Path: "/left/demo"}}
				right.Exports = []ToolExportV1{{Name: "demo", Path: "/right/demo"}}
			},
			share: func(left *ProviderDomainSetV1, right *ProviderDomainSetV1) {
				right.Exports = left.Exports
			},
		},
		{
			name: "capability", want: "capability conflict",
			mutate: func(left *ReleaseCandidateV1, right *ReleaseCandidateV1) {
				left.Exports = []ToolExportV1{{Name: "demo", Path: "/left/demo"}}
				right.Exports = []ToolExportV1{{Name: "demo", Path: "/right/demo"}}
			},
			share: func(left *ProviderDomainSetV1, right *ProviderDomainSetV1) {
				right.Capabilities = left.Capabilities
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			application := applicationBase
			source := sourceBase
			testCase.mutate(&application, &source)
			domains := solverTestDomainsV1(false)
			sets := []orderedCandidateSetV1{
				{group: applicationSet.Group, domains: domains[0]},
				{group: sourceSet.Group, domains: domains[1]},
			}
			if conflict, err := catalog.assignmentConflictV1(sets,
				[]ReleaseCandidateV1{application, source}, solverTestActiveProvidersV1()); err != nil || conflict != "" {
				t.Fatalf("isolated domains conflict = %q, %v", conflict, err)
			}
			testCase.share(&sets[0].domains, &sets[1].domains)
			conflict, err := catalog.assignmentConflictV1(
				sets, []ReleaseCandidateV1{application, source}, solverTestActiveProvidersV1())
			if err != nil || !strings.Contains(conflict, testCase.want) {
				t.Errorf("shared domain conflict = %q, %v; want %q", conflict, err, testCase.want)
			}
		})
	}
}

func TestRuntimeClaimsKeepFilesystemAndEnvironmentDomainsIndependentV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	applicationSet, sourceSet := solverTestCandidateSetsV1(t, catalog)
	application := applicationSet.Candidates[0]
	application.Contract = cloneReleaseContractV1(&application.Contract)
	application.Contract.Runtime = &RecordRuntimeV1{
		InstallRoot: "/opt/demo",
		Environment: []RecordEnvironmentVariableV1{{Name: "DEMO_HOME", Value: "application"}},
	}
	source := sourceSet.Candidates[1]
	source.Contract = cloneReleaseContractV1(&source.Contract)
	source.Contract.Runtime = &RecordRuntimeV1{
		InstallRoot: "/opt/demo",
		Environment: []RecordEnvironmentVariableV1{{Name: "DEMO_HOME", Value: "source-builder"}},
	}
	domains := solverTestDomainsV1(false)
	domains[1].Filesystem = domains[0].Filesystem
	sets := []orderedCandidateSetV1{
		{group: applicationSet.Group, domains: domains[0]},
		{group: sourceSet.Group, domains: domains[1]},
	}
	if conflict, err := catalog.assignmentConflictV1(
		sets, []ReleaseCandidateV1{application, source}, solverTestActiveProvidersV1()); err != nil || conflict != "" {
		t.Fatalf("shared filesystem with isolated environments conflict = %q, %v", conflict, err)
	}

	sets[1].domains.Environment = sets[0].domains.Environment
	conflict, err := catalog.assignmentConflictV1(
		sets, []ReleaseCandidateV1{application, source}, solverTestActiveProvidersV1())
	if err != nil || !strings.Contains(conflict, "environment conflict") {
		t.Fatalf("shared environment conflict = %q, %v; want environment conflict", conflict, err)
	}
}

func TestBindingRequirementClaimsAllowCompatibleProviderConstraintsV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	applicationSet, sourceSet := solverTestCandidateSetsV1(t, catalog)
	application := applicationSet.Candidates[0]
	source := sourceSet.Candidates[1]

	bindingLeft := cloneBindingContractV1(validRecordValuesV1()[4].(*BindingContractV1))
	bindingLeft.Requirements = []string{"demo>=1"}
	bindingRight := cloneBindingContractV1(&bindingLeft)
	bindingRight.Requirements = []string{"demo<3"}
	application.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &bindingLeft)}
	source.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &bindingRight)}

	domains := solverTestDomainsV1(false)
	domains[1].PackageManager = domains[0].PackageManager
	sets := []orderedCandidateSetV1{
		{group: applicationSet.Group, domains: domains[0]},
		{group: sourceSet.Group, domains: domains[1]},
	}
	if conflict, err := catalog.assignmentConflictV1(
		sets, []ReleaseCandidateV1{application, source}, solverTestActiveProvidersV1()); err != nil || conflict != "" {
		t.Fatalf("compatible binding requirements conflict = %q, %v", conflict, err)
	}
}

func TestBindingClaimsRequireSharedPythonInterpreterV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	applicationSet, sourceSet := solverTestCandidateSetsV1(t, catalog)
	application := applicationSet.Candidates[0]
	source := sourceSet.Candidates[1]

	bindingLeft := cloneBindingContractV1(validRecordValuesV1()[4].(*BindingContractV1))
	bindingLeft.SupportedPython = []string{"3.11", "3.12"}
	bindingRight := cloneBindingContractV1(&bindingLeft)
	bindingRight.SupportedPython = []string{"3.12", "3.13"}
	application.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &bindingLeft)}
	source.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &bindingRight)}

	domains := solverTestDomainsV1(false)
	domains[1].PackageManager = domains[0].PackageManager
	sets := []orderedCandidateSetV1{
		{group: applicationSet.Group, domains: domains[0]},
		{group: sourceSet.Group, domains: domains[1]},
	}
	if conflict, err := catalog.assignmentConflictV1(
		sets, []ReleaseCandidateV1{application, source}, solverTestActiveProvidersV1()); err != nil || conflict != "" {
		t.Fatalf("overlapping supported Python sets conflict = %q, %v", conflict, err)
	}

	bindingDisjoint := cloneBindingContractV1(&bindingLeft)
	bindingDisjoint.SupportedPython = []string{"3.13"}
	source.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &bindingDisjoint)}
	conflict, err := catalog.assignmentConflictV1(
		sets, []ReleaseCandidateV1{application, source}, solverTestActiveProvidersV1())
	if err != nil || !strings.Contains(conflict, "Python interpreter conflict") {
		t.Fatalf("disjoint supported Python sets conflict = %q, %v", conflict, err)
	}
}

func TestPayloadClaimsUseEffectiveRuntimeDestinationV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	applicationSet, sourceSet := solverTestCandidateSetsV1(t, catalog)
	application := applicationSet.Candidates[0]
	application.Contract = cloneReleaseContractV1(&application.Contract)
	application.Contract.Runtime = &RecordRuntimeV1{InstallRoot: "/opt/application"}
	source := sourceSet.Candidates[1]
	source.Contract = cloneReleaseContractV1(&source.Contract)
	source.Contract.Runtime = &RecordRuntimeV1{InstallRoot: "/opt/source"}

	payloadLeft := clonePayloadRecordV1(validRecordValuesV1()[6].(*PayloadRecordV1))
	payloadLeft.InstallDirectory = "browser"
	payloadRight := clonePayloadRecordV1(&payloadLeft)
	payloadRight.LogicalPath = "tools/demo/browser-alt.zip"
	payloadRight.Size = "43"
	application.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &payloadLeft)}
	source.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &payloadRight)}

	domains := solverTestDomainsV1(false)
	domains[1].Filesystem = domains[0].Filesystem
	sets := []orderedCandidateSetV1{
		{group: applicationSet.Group, domains: domains[0]},
		{group: sourceSet.Group, domains: domains[1]},
	}
	if conflict, err := catalog.assignmentConflictV1(
		sets, []ReleaseCandidateV1{application, source}, solverTestActiveProvidersV1()); err != nil || conflict != "" {
		t.Fatalf("distinct effective payload destinations conflict = %q, %v", conflict, err)
	}

	source.Contract.Runtime.InstallRoot = application.Contract.Runtime.InstallRoot
	conflict, err := catalog.assignmentConflictV1(
		sets, []ReleaseCandidateV1{application, source}, solverTestActiveProvidersV1())
	if err != nil || !strings.Contains(conflict, "filesystem conflict") {
		t.Fatalf("shared effective payload destination conflict = %q, %v", conflict, err)
	}
}

func TestNativePackageRepositoryRequirementsAreClaimedWithoutOverConstrainingV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	applicationSet, sourceSet := solverTestCandidateSetsV1(t, catalog)
	left := applicationSet.Candidates[0]
	right := sourceSet.Candidates[1]

	packageLeft := cloneNativePackageSetV1(validRecordValuesV1()[8].(*NativePackageSetV1))
	packageLeft.ID = "tool:demo/releases/1.2.3/package-sets/repository-left"
	packageLeft.Requirements = []string{"demo=1"}
	packageLeft.Repositories = []string{"debian-main"}
	packageLeft.ValidationMetadata = []string{"left-validation-only"}
	packageRight := cloneNativePackageSetV1(&packageLeft)
	packageRight.ID = "tool:demo/releases/1.2.3/package-sets/repository-right"
	packageRight.Repositories = []string{"vendor-main"}
	packageRight.ValidationMetadata = []string{"right-validation-only"}
	left.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &packageLeft)}
	right.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &packageRight)}

	domains := solverTestDomainsV1(false)
	domains[1].PackageManager = domains[0].PackageManager
	sets := []orderedCandidateSetV1{
		{group: applicationSet.Group, domains: domains[0]},
		{group: sourceSet.Group, domains: domains[1]},
	}
	if conflict, err := catalog.assignmentConflictV1(
		sets, []ReleaseCandidateV1{left, right}, solverTestActiveProvidersV1()); err != nil || conflict != "" {
		t.Fatalf("compatible repository requirements conflict = %q, %v", conflict, err)
	}

	claims := assignmentClaimsV1{
		semantic: make(map[string]semanticClaimV1), paths: make(map[string][]ownedPathClaimV1),
	}
	if conflict, err := catalog.addCandidateClaimsV1(
		&claims, domains[0], "application/demo", left); err != nil || conflict != "" {
		t.Fatalf("add repository claims = %q, %v", conflict, err)
	}
	claimKey := "package repository requirement\x00" + domains[0].PackageManager + "\x00apt/debian-main"
	if claim, exists := claims.semantic[claimKey]; !exists || claim.value != "debian-main" {
		t.Errorf("repository claim = %+v, %t; want exact provider requirement", claim, exists)
	}
	for key := range claims.semantic {
		if strings.Contains(key, "validation-only") {
			t.Errorf("validation-only metadata became a provider conflict claim: %q", key)
		}
	}
}

func TestActiveProviderConstraintsBacktrackAndAttributeConflictsV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	_, sourceSet := solverTestCandidateSetsV1(t, catalog)
	conflicting := sourceSet.Candidates[0]
	compatible := sourceSet.Candidates[1]

	bindingConflict := cloneBindingContractV1(validRecordValuesV1()[4].(*BindingContractV1))
	bindingConflict.Requirements = []string{"demo>=2"}
	bindingCompatible := cloneBindingContractV1(&bindingConflict)
	bindingCompatible.Requirements = []string{"demo<2"}
	conflicting.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &bindingConflict)}
	compatible.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &bindingCompatible)}
	sourceSet.Candidates = []ReleaseCandidateV1{conflicting, compatible}

	activeSource := solverTestActiveSourceV1("source-builder", "blueprint", "application/runtime")
	activeSource.PythonBindings = []ActivePythonBindingConstraintV1{{
		Name: "python", Requirements: []string{"demo==1.2.3"}, SupportedPython: []string{},
	}}
	operation := solverTestOperationV1()
	operation.ActiveProviders = solverTestActiveProvidersV1(activeSource)
	result, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{sourceSet}, solverTestDomainsV1(false), operation)
	if err != nil {
		t.Fatalf("resolve against active provider constraints: %v", err)
	}
	if len(result.Closures) != 1 || result.Closures[0].Provenance.Version != "1.2.3" ||
		result.VisitedStates != "2" {
		t.Errorf("active-provider fallback = closures %+v visited %s, want 1.2.3 after two states",
			result.Closures, result.VisitedStates)
	}
	if !strings.Contains(result.Snapshot.CanonicalJSON, `"kind":"blueprint"`) ||
		!strings.Contains(result.Snapshot.CanonicalJSON, `"demo==1.2.3"`) {
		t.Error("operation snapshot omitted active provider constraints or their provenance")
	}

	sourceSet.Candidates = sourceSet.Candidates[:1]
	_, err = catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{sourceSet}, solverTestDomainsV1(false), operation)
	if err == nil || !strings.Contains(err.Error(), `blueprint "application/runtime"`) ||
		!strings.Contains(err.Error(), `tool-candidate "source-builder/demo@2.0.0~1"`) ||
		!strings.Contains(err.Error(), `"demo==1.2.3" from blueprint`) ||
		!strings.Contains(err.Error(), `"demo>=2" from tool-candidate`) {
		t.Errorf("active-provider conflict diagnostic = %v, want both stable sources", err)
	}
}

func TestActiveProviderConstraintsAcceptEveryCanonicalFamilyV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	applicationSet, _ := solverTestCandidateSetsV1(t, catalog)
	binding := cloneBindingContractV1(validRecordValuesV1()[4].(*BindingContractV1))
	binding.Requirements = []string{"support<3"}
	binding.SupportedPython = []string{"3.11", "3.12"}
	applicationSet.Candidates[0].Contributions = append(
		applicationSet.Candidates[0].Contributions, solverTestAddRecordV1(t, catalog, &binding))
	digest := canonical.Digest("sha256:" + strings.Repeat("e", 64))
	source := solverTestActiveSourceV1("application", "active-provider", "application/current")
	source.NativePackages = []ActiveNativePackageConstraintV1{{
		Manager: "apt", Requirements: []string{"curl"}, Repositories: []string{"debian-main"},
	}}
	source.PythonBindings = []ActivePythonBindingConstraintV1{{
		Name: "python", Requirements: []string{"support>=1"}, SupportedPython: []string{"3.11"},
	}}
	source.InstallRoots = []string{"/srv/active"}
	source.OwnedPaths = []ActiveFilesystemConstraintV1{{Path: "/srv/active/data", Digest: digest}}
	source.Artifacts = []ActiveFilesystemConstraintV1{{Path: "active/data.zip", Digest: digest}}
	source.Environment = []RecordEnvironmentVariableV1{{Name: "ACTIVE_HOME", Value: "/srv/active"}}
	source.Exports = []ToolExportV1{{Name: "active-tool", Path: "/srv/active/bin/tool"}}
	source.Capabilities = []ToolExportV1{{Name: "active-capability", Path: "/srv/active/bin/tool"}}
	operation := solverTestOperationV1()
	operation.ActiveProviders = solverTestActiveProvidersV1(source)
	result, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{applicationSet}, solverTestDomainsV1(false)[:1], operation)
	if err != nil {
		t.Fatalf("resolve with every canonical active constraint family: %v", err)
	}
	for _, value := range []string{
		`"native_packages"`, `"python_bindings"`, `"install_roots"`, `"owned_paths"`,
		`"artifacts"`, `"environment"`, `"exports"`, `"capabilities"`,
	} {
		if !strings.Contains(result.Snapshot.CanonicalJSON, value) {
			t.Errorf("operation snapshot omitted active constraint family %s", value)
		}
	}
}

func TestActiveProviderConstraintsRespectIsolatedDomainsV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	applicationSet, sourceSet := solverTestCandidateSetsV1(t, catalog)
	application := applicationSet.Candidates[0]
	binding := cloneBindingContractV1(validRecordValuesV1()[4].(*BindingContractV1))
	binding.Requirements = []string{"demo>=2"}
	application.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &binding)}
	domains := solverTestDomainsV1(false)
	sets := []orderedCandidateSetV1{
		{group: applicationSet.Group, domains: domains[0]},
		{group: sourceSet.Group, domains: domains[1]},
	}
	activeSource := solverTestActiveSourceV1("source-builder", "lock-state", "source-builder/python")
	activeSource.PythonBindings = []ActivePythonBindingConstraintV1{{
		Name: "python", Requirements: []string{"demo==1"}, SupportedPython: []string{},
	}}
	if conflict, err := catalog.assignmentConflictV1(sets, []ReleaseCandidateV1{application},
		solverTestActiveProvidersV1(activeSource)); err != nil || conflict != "" {
		t.Fatalf("isolated active provider constrained application candidate: %q, %v", conflict, err)
	}
}

func TestActiveProviderConstraintDiagnosticsRetainCumulativeSourcesV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	applicationSet, _ := solverTestCandidateSetsV1(t, catalog)
	candidate := applicationSet.Candidates[0]
	binding := cloneBindingContractV1(validRecordValuesV1()[4].(*BindingContractV1))
	binding.Requirements = []string{"demo>=4"}
	candidate.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &binding)}
	first := solverTestActiveSourceV1("application", "blueprint", "application/base")
	first.PythonBindings = []ActivePythonBindingConstraintV1{{
		Name: "python", Requirements: []string{"demo>=1"}, SupportedPython: []string{},
	}}
	second := solverTestActiveSourceV1("application", "lock-state", "application/lock")
	second.PythonBindings = []ActivePythonBindingConstraintV1{{
		Name: "python", Requirements: []string{"demo<3"}, SupportedPython: []string{},
	}}
	domains := solverTestDomainsV1(false)
	sets := []orderedCandidateSetV1{{group: applicationSet.Group, domains: domains[0]}}
	conflict, err := catalog.assignmentConflictV1(sets, []ReleaseCandidateV1{candidate},
		solverTestActiveProvidersV1(first, second))
	for _, source := range []string{
		`blueprint "application/base"`, `lock-state "application/lock"`,
		`tool-candidate "application/demo@1.2.3~1"`,
	} {
		if err != nil || !strings.Contains(conflict, source) {
			t.Fatalf("cumulative conflict = %q, %v; missing source %q", conflict, err, source)
		}
	}
}

func TestActiveProviderConstraintsSeedEveryClaimFamilyV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	applicationSet, _ := solverTestCandidateSetsV1(t, catalog)
	domains := solverTestDomainsV1(false)
	sets := []orderedCandidateSetV1{{group: applicationSet.Group, domains: domains[0]}}
	otherDigest := canonical.Digest("sha256:" + strings.Repeat("f", 64))

	for _, testCase := range []struct {
		name   string
		want   string
		mutate func(*ReleaseCandidateV1, *ActiveProviderConstraintSourceV1)
	}{
		{name: "native package", want: "package requirement conflict", mutate: func(candidate *ReleaseCandidateV1, source *ActiveProviderConstraintSourceV1) {
			packages := cloneNativePackageSetV1(validRecordValuesV1()[8].(*NativePackageSetV1))
			packages.Requirements = []string{"demo=2"}
			candidate.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &packages)}
			source.NativePackages = []ActiveNativePackageConstraintV1{{
				Manager: "apt", Requirements: []string{"demo=1"}, Repositories: []string{},
			}}
		}},
		{name: "Python requirement", want: "binding requirement conflict", mutate: func(candidate *ReleaseCandidateV1, source *ActiveProviderConstraintSourceV1) {
			binding := cloneBindingContractV1(validRecordValuesV1()[4].(*BindingContractV1))
			binding.Requirements = []string{"demo>=2"}
			candidate.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &binding)}
			source.PythonBindings = []ActivePythonBindingConstraintV1{{
				Name: "python", Requirements: []string{"demo==1"}, SupportedPython: []string{},
			}}
		}},
		{name: "Python interpreter", want: "Python interpreter conflict", mutate: func(candidate *ReleaseCandidateV1, source *ActiveProviderConstraintSourceV1) {
			binding := cloneBindingContractV1(validRecordValuesV1()[4].(*BindingContractV1))
			binding.Requirements = []string{"demo>=1"}
			binding.SupportedPython = []string{"3.12"}
			candidate.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &binding)}
			source.PythonBindings = []ActivePythonBindingConstraintV1{{
				Name: "python", Requirements: []string{}, SupportedPython: []string{"3.11"},
			}}
		}},
		{name: "install root", want: "filesystem conflict", mutate: func(candidate *ReleaseCandidateV1, source *ActiveProviderConstraintSourceV1) {
			candidate.Contract = cloneReleaseContractV1(&candidate.Contract)
			candidate.Contract.Runtime = &RecordRuntimeV1{InstallRoot: "/opt/demo/bin"}
			source.InstallRoots = []string{"/opt/demo"}
		}},
		{name: "owned path", want: "filesystem conflict", mutate: func(candidate *ReleaseCandidateV1, source *ActiveProviderConstraintSourceV1) {
			payload := clonePayloadRecordV1(validRecordValuesV1()[6].(*PayloadRecordV1))
			payload.InstallDirectory = "payload"
			candidate.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &payload)}
			source.OwnedPaths = []ActiveFilesystemConstraintV1{{Path: "payload", Digest: otherDigest}}
		}},
		{name: "artifact", want: "artifact logical path conflict", mutate: func(candidate *ReleaseCandidateV1, source *ActiveProviderConstraintSourceV1) {
			payload := clonePayloadRecordV1(validRecordValuesV1()[6].(*PayloadRecordV1))
			candidate.Contributions = []RecordReferenceV1{solverTestAddRecordV1(t, catalog, &payload)}
			source.Artifacts = []ActiveFilesystemConstraintV1{{Path: payload.LogicalPath, Digest: otherDigest}}
		}},
		{name: "environment", want: "environment conflict", mutate: func(candidate *ReleaseCandidateV1, source *ActiveProviderConstraintSourceV1) {
			candidate.Contract = cloneReleaseContractV1(&candidate.Contract)
			candidate.Contract.Runtime = &RecordRuntimeV1{
				Environment: []RecordEnvironmentVariableV1{{Name: "DEMO_HOME", Value: "candidate"}},
			}
			source.Environment = []RecordEnvironmentVariableV1{{Name: "DEMO_HOME", Value: "active"}}
		}},
		{name: "export", want: "export conflict", mutate: func(candidate *ReleaseCandidateV1, source *ActiveProviderConstraintSourceV1) {
			candidate.Exports = []ToolExportV1{{Name: "demo", Path: "/candidate/demo"}}
			source.Exports = []ToolExportV1{{Name: "demo", Path: "/active/demo"}}
		}},
		{name: "capability", want: "capability conflict", mutate: func(candidate *ReleaseCandidateV1, source *ActiveProviderConstraintSourceV1) {
			candidate.Exports = []ToolExportV1{{Name: "demo", Path: "/candidate/demo"}}
			source.Capabilities = []ToolExportV1{{Name: "demo", Path: "/active/demo"}}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := applicationSet.Candidates[0]
			candidate.Contract = cloneReleaseContractV1(&candidate.Contract)
			candidate.Contract.Runtime = nil
			candidate.Contributions = []RecordReferenceV1{}
			candidate.Exports = []ToolExportV1{}
			source := solverTestActiveSourceV1("application", "blueprint", "application/runtime")
			testCase.mutate(&candidate, &source)
			conflict, err := catalog.assignmentConflictV1(sets, []ReleaseCandidateV1{candidate},
				solverTestActiveProvidersV1(source))
			if err != nil || !strings.Contains(conflict, testCase.want) ||
				!strings.Contains(conflict, `blueprint "application/runtime"`) ||
				!strings.Contains(conflict, `tool-candidate "application/demo@1.2.3~1"`) {
				t.Errorf("active %s conflict = %q, %v; want %q with both sources",
					testCase.name, conflict, err, testCase.want)
			}
		})
	}
}

func TestResolveSelectedClosuresReportsNoCompleteAssignmentV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	application, source := solverTestCandidateSetsV1(t, catalog)
	source.Candidates = source.Candidates[:1]
	_, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{source, application}, solverTestDomainsV1(true), solverTestOperationV1())
	if err == nil || !strings.Contains(err.Error(), "no complete assignment") ||
		!strings.Contains(err.Error(), "application/demo") ||
		!strings.Contains(err.Error(), "source-builder/demo") ||
		!strings.Contains(err.Error(), "export conflict") {
		t.Errorf("incompatible assignment error = %v", err)
	}
}

func TestJointAssignmentCapFailsClosedV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	application, source := solverTestCandidateSetsV1(t, catalog)
	ordered, err := catalog.prepareCandidateSetsV1(
		[]ReleaseCandidateSetV1{source, application}, solverTestDomainsV1(true))
	if err != nil {
		t.Fatal(err)
	}
	chosen, visited, err := catalog.solveCandidateSetsV1(ordered, solverTestActiveProvidersV1(), 1)
	if err == nil || !strings.Contains(err.Error(), "visited-state cap 1 exceeded") {
		t.Fatalf("cap error = %v, want fail-closed diagnostic", err)
	}
	if chosen != nil || visited != 2 {
		t.Errorf("cap result = chosen %v visited %d, want nil and 2", chosen, visited)
	}
}

func TestSelectedClosureIdentityExcludesValidationAndSourceOnlyDataV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	group := candidateTestGroupV1()
	candidates, err := catalog.SelectReleaseCandidatesV1(
		group, candidateTestObservedV1(), candidateTestClientV1(), nil)
	if err != nil {
		t.Fatal(err)
	}
	base, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{{Group: group, Candidates: candidates}}, solverTestDomainsV1(false)[:1],
		solverTestOperationV1())
	if err != nil {
		t.Fatal(err)
	}

	metadataOnly := candidates[0]
	metadataOnly.Manifest = cloneReleaseManifestV1(&candidates[0].Manifest)
	metadataOnly.Manifest.Revision = "2"
	metadataOnly.Manifest.Provenance = []string{"https://example.com/changed-provenance"}
	metadataOnly.Manifest.ArtifactSources = nil
	metadataOnly.Fixture = cloneIntegrationFixtureV1(&candidates[0].Fixture)
	metadataOnly.Fixture.BaseImage = "docker.io/library/debian:changed"
	metadataOnly.Profiles = []ValidationProfileRecordV1{cloneValidationProfileV1(&candidates[0].Profiles[0])}
	metadataOnly.Profiles[0].Probes[0].Args[0] = "--changed-validation"
	metadata, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{{Group: group, Candidates: []ReleaseCandidateV1{metadataOnly}}},
		solverTestDomainsV1(false)[:1], solverTestOperationV1())
	if err != nil {
		t.Fatal(err)
	}
	if base.Closures[0].Identity != metadata.Closures[0].Identity {
		t.Error("source, revision, fixture, or validation-only data changed selected identity")
	}

	validationOnly := candidates[0]
	validationOnly.Fixture = cloneIntegrationFixtureV1(&candidates[0].Fixture)
	validationOnly.Fixture.BaseImage = "docker.io/library/debian:validation-only"
	validationOnly.Profiles = []ValidationProfileRecordV1{
		cloneValidationProfileV1(&candidates[0].Profiles[0]),
	}
	validationOnly.Profiles[0].Probes[0].Args[0] = "--validation-only"
	validationResult, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{{Group: group, Candidates: []ReleaseCandidateV1{validationOnly}}},
		solverTestDomainsV1(false)[:1], solverTestOperationV1())
	if err != nil {
		t.Fatal(err)
	}
	if base.Closures[0].Identity != validationResult.Closures[0].Identity {
		t.Error("validation-only data changed selected identity")
	}
	if base.Snapshot.Digest == validationResult.Snapshot.Digest ||
		base.Snapshot.CanonicalJSON == validationResult.Snapshot.CanonicalJSON {
		t.Error("validation-only data was omitted from the immutable operation snapshot")
	}

	behavior := candidates[0]
	behavior.Contract = cloneReleaseContractV1(&candidates[0].Contract)
	behavior.Contract.Exports = []ToolExportV1{{Name: "demo", Path: "/opt/changed/bin/demo"}}
	behaviorResult, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{{Group: group, Candidates: []ReleaseCandidateV1{behavior}}},
		solverTestDomainsV1(false)[:1], solverTestOperationV1())
	if err != nil {
		t.Fatal(err)
	}
	if base.Closures[0].Identity == behaviorResult.Closures[0].Identity {
		t.Error("a selected contract export did not change selected identity")
	}
}

func TestSelectedClosureAndOperationSnapshotOwnTheirDataV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	group := candidateTestGroupV1()
	candidates, err := catalog.SelectReleaseCandidatesV1(
		group, candidateTestObservedV1(), candidateTestClientV1(), nil)
	if err != nil {
		t.Fatal(err)
	}
	operation := solverTestOperationV1()
	result, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{{Group: group, Candidates: candidates}}, solverTestDomainsV1(false)[:1], operation)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON := result.Snapshot.CanonicalJSON
	snapshotDigest := result.Snapshot.Digest
	operation.Blueprint.Value["identity"] = "mutated-after-resolution"
	if result.Snapshot.CanonicalJSON != snapshotJSON || result.Snapshot.Digest != snapshotDigest {
		t.Error("the immutable operation snapshot aliases caller-owned input")
	}
	changedOperation, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{{Group: group, Candidates: candidates}}, solverTestDomainsV1(false)[:1], operation)
	if err != nil {
		t.Fatal(err)
	}
	if changedOperation.Snapshot.Digest == snapshotDigest {
		t.Error("a changed canonical operation input reused the finalized snapshot")
	}

	closure := &result.Closures[0]
	if len(closure.Profiles) == 0 || len(closure.Records.Payloads) == 0 {
		t.Fatal("candidate fixture did not produce validation and payload records")
	}
	closure.Fixture.BaseImage = "mutated"
	closure.Profiles[0].Probes[0].Args[0] = "mutated"
	closure.Records.Payloads[0].Record.Executables[0] = "mutated"
	if result.Snapshot.CanonicalJSON != snapshotJSON || result.Snapshot.Digest != snapshotDigest {
		t.Error("the immutable operation snapshot aliases returned closure data")
	}
	again, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{{Group: group, Candidates: candidates}}, solverTestDomainsV1(false)[:1],
		solverTestOperationV1())
	if err != nil {
		t.Fatal(err)
	}
	if again.Closures[0].Fixture.BaseImage == "mutated" ||
		again.Closures[0].Profiles[0].Probes[0].Args[0] == "mutated" ||
		again.Closures[0].Records.Payloads[0].Record.Executables[0] == "mutated" {
		t.Error("a selected closure aliases a previous result or loaded catalog state")
	}
}

func TestOperationSnapshotOwnsActiveProviderConstraintsV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	group := candidateTestGroupV1()
	candidates, err := catalog.SelectReleaseCandidatesV1(
		group, candidateTestObservedV1(), candidateTestClientV1(), nil)
	if err != nil {
		t.Fatal(err)
	}
	source := solverTestActiveSourceV1("application", "active-provider", "application/environment")
	source.Environment = []RecordEnvironmentVariableV1{{Name: "ACTIVE_ONLY", Value: "original"}}
	operation := solverTestOperationV1()
	operation.ActiveProviders = solverTestActiveProvidersV1(source)
	result, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{{Group: group, Candidates: candidates}},
		solverTestDomainsV1(false)[:1], operation)
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON := result.Snapshot.CanonicalJSON
	snapshotDigest := result.Snapshot.Digest
	operation.ActiveProviders.Sources[0].Source.ID = "mutated"
	operation.ActiveProviders.Sources[0].Environment[0].Value = "mutated"
	if result.Snapshot.CanonicalJSON != snapshotJSON || result.Snapshot.Digest != snapshotDigest {
		t.Error("the immutable operation snapshot aliases caller-owned active constraints")
	}
	changed, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{{Group: group, Candidates: candidates}},
		solverTestDomainsV1(false)[:1], operation)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Snapshot.Digest == snapshotDigest {
		t.Error("changed active-provider provenance and constraints reused the operation snapshot")
	}
}

func TestSelectedClosureRecordsCloneEveryContributionFamilyV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	candidates, err := catalog.SelectReleaseCandidatesV1(
		candidateTestGroupV1(), candidateTestObservedV1(), candidateTestClientV1(), nil)
	if err != nil {
		t.Fatal(err)
	}
	bindingContract := cloneBindingContractV1(validRecordValuesV1()[4].(*BindingContractV1))
	bindingArtifact := cloneBindingArtifactV1(validRecordValuesV1()[5].(*BindingArtifactRecordV1))
	packageSet := cloneNativePackageSetV1(validRecordValuesV1()[8].(*NativePackageSetV1))
	references := []RecordReferenceV1{
		solverTestAddRecordV1(t, catalog, &bindingContract),
		solverTestAddRecordV1(t, catalog, &bindingArtifact),
		candidates[0].Contributions[0],
		solverTestAddRecordV1(t, catalog, &packageSet),
	}
	records, err := catalog.selectedClosureRecordsV1(references)
	if err != nil {
		t.Fatal(err)
	}
	if len(records.BindingContracts) != 1 || len(records.BindingArtifacts) != 1 ||
		len(records.Payloads) != 1 || len(records.PackageSets) != 1 {
		t.Fatalf("selected records = %+v, want one of every contribution family", records)
	}
	records.BindingContracts[0].Record.Requirements[0] = "mutated"
	records.BindingArtifacts[0].Record.Tags[0] = "mutated"
	records.Payloads[0].Record.Executables[0] = "mutated"
	records.PackageSets[0].Record.Requirements[0] = "mutated"

	again, err := catalog.selectedClosureRecordsV1(references)
	if err != nil {
		t.Fatal(err)
	}
	if again.BindingContracts[0].Record.Requirements[0] == "mutated" ||
		again.BindingArtifacts[0].Record.Tags[0] == "mutated" ||
		again.Payloads[0].Record.Executables[0] == "mutated" ||
		again.PackageSets[0].Record.Requirements[0] == "mutated" {
		t.Error("a selected contribution record aliases an earlier result or catalog state")
	}
}

func TestResolveSelectedClosuresRequiresCompleteInputsAndProviderDomainsV1(t *testing.T) {
	catalog := candidateTestCatalogV1(t)
	group := candidateTestGroupV1()
	candidates, err := catalog.SelectReleaseCandidatesV1(
		group, candidateTestObservedV1(), candidateTestClientV1(), nil)
	if err != nil {
		t.Fatal(err)
	}
	set := []ReleaseCandidateSetV1{{Group: group, Candidates: candidates}}
	if _, err := catalog.ResolveSelectedClosuresV1(set, nil, solverTestOperationV1()); err == nil ||
		!strings.Contains(err.Error(), "provider-domain mapping") {
		t.Errorf("missing provider domains error = %v", err)
	}
	incomplete := solverTestOperationV1()
	incomplete.Catalog = canonical.Envelope{}
	if _, err := catalog.ResolveSelectedClosuresV1(set, solverTestDomainsV1(false)[:1], incomplete); err == nil ||
		!strings.Contains(err.Error(), "catalog snapshot input must be complete") {
		t.Errorf("incomplete operation error = %v", err)
	}
	missingActive := solverTestOperationV1()
	missingActive.ActiveProviders = ActiveProviderConstraintsV1{}
	if _, err := catalog.ResolveSelectedClosuresV1(
		set, solverTestDomainsV1(false)[:1], missingActive); err == nil ||
		!strings.Contains(err.Error(), "active provider constraints") {
		t.Errorf("missing active-provider snapshot error = %v", err)
	}
	unknownScope := solverTestOperationV1()
	unknown := solverTestActiveSourceV1("unknown", "blueprint", "application/runtime")
	unknown.Environment = []RecordEnvironmentVariableV1{{Name: "DEMO_HOME", Value: "active"}}
	unknownScope.ActiveProviders = solverTestActiveProvidersV1(unknown)
	if _, err := catalog.ResolveSelectedClosuresV1(
		set, solverTestDomainsV1(false)[:1], unknownScope); err == nil ||
		!strings.Contains(err.Error(), "unknown scope") {
		t.Errorf("unknown active-provider scope error = %v", err)
	}
	badProvenance := solverTestOperationV1()
	bad := solverTestActiveSourceV1("application", "blueprint\n", "application/runtime")
	bad.Environment = []RecordEnvironmentVariableV1{{Name: "DEMO_HOME", Value: "active"}}
	badProvenance.ActiveProviders = solverTestActiveProvidersV1(bad)
	if _, err := catalog.ResolveSelectedClosuresV1(
		set, solverTestDomainsV1(false)[:1], badProvenance); err == nil ||
		!strings.Contains(err.Error(), "canonical provenance") {
		t.Errorf("noncanonical active-provider provenance error = %v", err)
	}
}
