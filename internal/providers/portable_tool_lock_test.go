package providers

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
)

const portableToolLockPayloadDigest = canonical.Digest("sha256:abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd")

func TestBuildPortableToolLockV1PersistsExactPlanSourcesAndSanitizedOutcomes(t *testing.T) {
	dag, releases, inputs := portableToolLockFixtureV1(t)
	lock, err := BuildPortableToolLockV1(dag, releases, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePortableToolLockV1(lock); err != nil {
		t.Fatal(err)
	}
	content, err := CanonicalPortableToolLockBytesV1(lock)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(content, []byte("redirected.example")) || bytes.Contains(content, []byte(inputs[0].Provenance.OperationID)) {
		t.Fatalf("lock retained process-local or redirect-target diagnostics: %s", content)
	}
	network := lock.Acquisitions[0].Outcome
	if network.Kind != providerstore.AcquisitionOutcomeNetwork || network.SuccessfulDeclaredLocator != "https://mirror.example/demo.whl" || network.RedirectHops != "2" {
		t.Fatalf("network outcome = %#v", network)
	}
	cache := lock.Acquisitions[1].Outcome
	if cache.Kind != providerstore.AcquisitionOutcomeCacheHit || cache.SuccessfulDeclaredLocator != "" || cache.RedirectHops != "0" {
		t.Fatalf("cache outcome = %#v", cache)
	}
	if len(cache.HistoricalLocators) != 1 || cache.HistoricalLocators[0] != "https://upstream.example/demo.tar" {
		t.Fatalf("historical locators = %#v", cache.HistoricalLocators)
	}

	inputs[0].Source.Record.Value["mirrors"].([]any)[0] = "https://mutated.example/demo.whl"
	dag.PortableToolPlan.Tools[0].Provenance.Revision = "9"
	if lock.Acquisitions[0].Source.Record.Value["mirrors"].([]any)[0] != "https://mirror.example/demo.whl" || lock.Plan.PortableToolPlan.Tools[0].Provenance.Revision != "1" {
		t.Fatal("portable tool lock did not clone its construction inputs")
	}
}

func TestValidatePortableToolLockV1RejectsIncompleteOrAmbiguousAcquisitionEvidence(t *testing.T) {
	dag, releases, inputs := portableToolLockFixtureV1(t)
	valid, err := BuildPortableToolLockV1(dag, releases, inputs)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*PortableToolLockV1)
		want   string
	}{
		{name: "missing artifact", mutate: func(lock *PortableToolLockV1) { lock.Acquisitions = lock.Acquisitions[:1] }, want: "every selected artifact"},
		{name: "undeclared successful locator", mutate: func(lock *PortableToolLockV1) {
			lock.Acquisitions[0].Outcome.SuccessfulDeclaredLocator = "https://redirected.example/secret"
		}, want: "successful declared locator"},
		{name: "cache locator", mutate: func(lock *PortableToolLockV1) {
			lock.Acquisitions[1].Outcome.SuccessfulDeclaredLocator = "https://mirror.example/demo.tar"
		}, want: "no locator was contacted"},
		{name: "historical relabeled", mutate: func(lock *PortableToolLockV1) {
			lock.Acquisitions[1].Outcome.HistoricalLocators = []string{"https://mirror.example/demo.tar"}
		}, want: "historical locators"},
		{name: "unsafe declared locator", mutate: func(lock *PortableToolLockV1) {
			lock.Acquisitions[0].Source.Record.Value["mirrors"] = []any{"http://mirror.example/demo.whl"}
			refreshPortableToolTestRecordDigest(&lock.Acquisitions[0].Source.Reference, lock.Acquisitions[0].Source.Record)
			mapping := lock.Releases[0].Manifest.Record.Value["artifact_sources"].([]any)[0].(canonical.Object)
			mapping["source"].(canonical.Object)["digest"] = string(lock.Acquisitions[0].Source.Reference.Digest)
			refreshPortableToolTestRecordDigest(&lock.Releases[0].Manifest.Reference, lock.Releases[0].Manifest.Record)
			lock.Plan.PortableToolPlan.Tools[0].Provenance.ManifestDigest = lock.Releases[0].Manifest.Reference.Digest
			lock.Acquisitions[0].Outcome.SuccessfulDeclaredLocator = "http://mirror.example/demo.whl"
		}, want: "credential-free HTTPS"},
		{name: "artifact bytes", mutate: func(lock *PortableToolLockV1) {
			lock.Acquisitions[1].Descriptor.SHA256 = portableToolTestDigest
		}, want: "selected artifact record"},
		{name: "binding kind", mutate: func(lock *PortableToolLockV1) {
			lock.Acquisitions[0].Descriptor.Kind = "deb"
		}, want: "selected binding artifact record"},
		{name: "binding filename", mutate: func(lock *PortableToolLockV1) {
			lock.Acquisitions[0].Descriptor.LogicalPath = "bindings/renamed.whl"
		}, want: "selected binding artifact record"},
		{name: "source record revision", mutate: func(lock *PortableToolLockV1) {
			setPortableToolTestRecordID(&lock.Acquisitions[1].Source.Reference, &lock.Acquisitions[1].Source.Record, "tool:demo/releases/1.2.3/revisions/2/sources/payload")
		}, want: "outside selected release revision"},
		{name: "nested source record name", mutate: func(lock *PortableToolLockV1) {
			setPortableToolTestRecordID(&lock.Acquisitions[1].Source.Reference, &lock.Acquisitions[1].Source.Record, "tool:demo/releases/1.2.3/revisions/1/sources/payload/extra")
			mapping := lock.Releases[0].Manifest.Record.Value["artifact_sources"].([]any)[1].(canonical.Object)
			mapping["source"].(canonical.Object)["id"] = lock.Acquisitions[1].Source.Reference.ID
			mapping["source"].(canonical.Object)["digest"] = string(lock.Acquisitions[1].Source.Reference.Digest)
			refreshPortableToolTestRecordDigest(&lock.Releases[0].Manifest.Reference, lock.Releases[0].Manifest.Record)
			lock.Plan.PortableToolPlan.Tools[0].Provenance.ManifestDigest = lock.Releases[0].Manifest.Reference.Digest
		}, want: "outside the selected release revision"},
		{name: "same revision unauthorized source", mutate: func(lock *PortableToolLockV1) {
			setPortableToolTestRecordID(&lock.Acquisitions[1].Source.Reference, &lock.Acquisitions[1].Source.Record, "tool:demo/releases/1.2.3/revisions/1/sources/unlisted-payload")
		}, want: "authorize the selected content and source record once"},
		{name: "missing manifest aliases", mutate: func(lock *PortableToolLockV1) {
			delete(lock.Releases[0].Manifest.Record.Value, "aliases")
			refreshPortableToolLockManifestDigestV1(lock)
		}, want: "canonical release-manifest fields"},
		{name: "empty manifest targets", mutate: func(lock *PortableToolLockV1) {
			lock.Releases[0].Manifest.Record.Value["targets"] = []any{}
			refreshPortableToolLockManifestDigestV1(lock)
		}, want: "targets must use a bounded nonempty array"},
		{name: "empty manifest validation profiles", mutate: func(lock *PortableToolLockV1) {
			lock.Releases[0].Manifest.Record.Value["validation_profiles"] = []any{}
			refreshPortableToolLockManifestDigestV1(lock)
		}, want: "validation_profiles must use a bounded nonempty array"},
		{name: "manifest alias repeats exact version", mutate: func(lock *PortableToolLockV1) {
			lock.Releases[0].Manifest.Record.Value["aliases"] = []any{"1.2.3"}
			refreshPortableToolLockManifestDigestV1(lock)
		}, want: "aliases must be canonical versions distinct"},
		{name: "manifest contract outside release", mutate: func(lock *PortableToolLockV1) {
			lock.Releases[0].Manifest.Record.Value["contract"].(canonical.Object)["id"] = "tool:demo/releases/other/contract"
			refreshPortableToolLockManifestDigestV1(lock)
		}, want: "exact reference to the selected release contract"},
		{name: "unsorted manifest provenance", mutate: func(lock *PortableToolLockV1) {
			lock.Releases[0].Manifest.Record.Value["provenance"] = []any{"https://z.example/release", "https://a.example/release"}
			refreshPortableToolLockManifestDigestV1(lock)
		}, want: "release provenance must be unique and sorted"},
		{name: "manifest mapping to impossible artifact record", mutate: func(lock *PortableToolLockV1) {
			mapping := lock.Releases[0].Manifest.Record.Value["artifact_sources"].([]any)[0].(canonical.Object)
			mapping["artifact"].(canonical.Object)["id"] = "tool:demo/releases/1.2.3/validation/profiles/default"
			refreshPortableToolLockManifestDigestV1(lock)
		}, want: "artifact must name a payload or binding artifact"},
		{name: "empty source provenance", mutate: func(lock *PortableToolLockV1) {
			lock.Acquisitions[1].Source.Record.Value["provenance"] = []any{}
			lock.Acquisitions[1].Outcome.HistoricalLocators = []string{}
			refreshPortableToolLockSourceDigestV1(lock, 1)
		}, want: "source record provenance must use a bounded nonempty array"},
		{name: "unsorted source provenance", mutate: func(lock *PortableToolLockV1) {
			values := []any{"https://z.example/demo.tar", "https://a.example/demo.tar"}
			lock.Acquisitions[1].Source.Record.Value["provenance"] = values
			lock.Acquisitions[1].Outcome.HistoricalLocators = []string{values[0].(string), values[1].(string)}
			refreshPortableToolLockSourceDigestV1(lock, 1)
		}, want: "source record provenance must be unique and sorted"},
		{name: "noncanonical source provenance URL", mutate: func(lock *PortableToolLockV1) {
			lock.Acquisitions[1].Source.Record.Value["provenance"] = []any{"https://UPSTREAM.example/demo.tar"}
			lock.Acquisitions[1].Outcome.HistoricalLocators = []string{"https://UPSTREAM.example/demo.tar"}
			refreshPortableToolLockSourceDigestV1(lock, 1)
		}, want: "canonical credential-free HTTPS URL"},
		{name: "missing source diagnostics", mutate: func(lock *PortableToolLockV1) {
			delete(lock.Acquisitions[1].Source.Record.Value, "diagnostics")
			refreshPortableToolLockSourceDigestV1(lock, 1)
		}, want: "canonical artifact-source fields"},
		{name: "extra source record field", mutate: func(lock *PortableToolLockV1) {
			lock.Acquisitions[1].Source.Record.Value["unexpected"] = "value"
			refreshPortableToolLockSourceDigestV1(lock, 1)
		}, want: "canonical artifact-source fields"},
		{name: "unsorted source diagnostics", mutate: func(lock *PortableToolLockV1) {
			lock.Acquisitions[1].Source.Record.Value["diagnostics"] = []any{"z", "a"}
			refreshPortableToolLockSourceDigestV1(lock, 1)
		}, want: "source record diagnostics must be unique and sorted"},
		{name: "noncanonical source mirror", mutate: func(lock *PortableToolLockV1) {
			lock.Acquisitions[1].Source.Record.Value["mirrors"] = []any{"https://MIRROR.example/demo.tar"}
			refreshPortableToolLockSourceDigestV1(lock, 1)
		}, want: "canonical credential-free HTTPS URL"},
		{name: "redirect count exceeds hard maximum", mutate: func(lock *PortableToolLockV1) {
			lock.Acquisitions[0].Outcome.RedirectHops = "4"
		}, want: "no greater than 3"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate, err := BuildPortableToolLockV1(dag, releases, inputs)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(&candidate)
			if err := ValidatePortableToolLockV1(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
	if err := ValidatePortableToolLockV1(valid); err != nil {
		t.Fatal(err)
	}
	mismatched := append([]PortableToolArtifactAcquisitionInputV1{}, inputs...)
	mismatched[0].Provenance.SourceID = "different-source"
	if _, err := BuildPortableToolLockV1(dag, releases, mismatched); err == nil || !strings.Contains(err.Error(), "provenance source") {
		t.Fatalf("mismatched acquisition source error = %v", err)
	}
	if _, err := BuildPortableToolLockV1(dag, nil, inputs); err == nil || !strings.Contains(err.Error(), "explicit arrays") {
		t.Fatalf("missing release manifests error = %v", err)
	}
}

func TestBuildPortableToolLockV1AuthorizesSelectedArtifactsByContentGroup(t *testing.T) {
	dag, _, inputs := portableToolLockFixtureV1(t)
	plan := clonePortableToolPlanForPortableToolDAGV1(dag.PortableToolPlan)
	payload := &plan.Tools[0].Responsibilities.Payloads[0]
	payload.Record.Value["sha256"] = string(portableToolTestDigest)
	payload.Record.Value["size"] = "12"
	refreshPortableToolTestRecordDigest(&payload.Reference, payload.Record)

	inputs[1].Artifact = payload.Reference
	inputs[1].Descriptor.SHA256 = portableToolTestDigest
	inputs[1].Descriptor.Size = "12"
	inputs[1].Source = inputs[0].Source
	inputs[1].Provenance.SourceID = inputs[0].Source.Reference.ID
	manifest := portableToolLockManifestV1("demo", "1.2.3", "1", inputs[:1])
	plan.Tools[0].Provenance.ManifestDigest = manifest.Reference.Digest
	groupedDAG, err := BuildPortableToolProviderDAGV1(dag.ProviderPlan, plan, dag.Domains)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := BuildPortableToolLockV1(groupedDAG, []PortableToolReleaseManifestInputV1{{
		Scope: "application", Tool: "demo", Manifest: manifest,
	}}, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Acquisitions) != 2 || lock.Acquisitions[0].Descriptor.SHA256 != lock.Acquisitions[1].Descriptor.SHA256 {
		t.Fatalf("content-group acquisitions = %#v", lock.Acquisitions)
	}
}

func TestPortableToolLockDistinguishesReleaseRevisionsSharingAClosure(t *testing.T) {
	firstDAG, firstReleases, firstInputs := portableToolLockFixtureV1(t)
	first, err := BuildPortableToolLockV1(firstDAG, firstReleases, firstInputs)
	if err != nil {
		t.Fatal(err)
	}

	secondPlan := clonePortableToolPlanForPortableToolDAGV1(firstDAG.PortableToolPlan)
	secondPlan.Tools[0].Provenance.Revision = "2"
	secondInputs := append([]PortableToolArtifactAcquisitionInputV1{}, firstInputs...)
	for index := range secondInputs {
		secondInputs[index].Source.Record.Value = clonePortableToolCanonicalObjectV1(firstInputs[index].Source.Record.Value)
		id := strings.Replace(secondInputs[index].Source.Reference.ID, "/revisions/1/", "/revisions/2/", 1)
		setPortableToolTestRecordID(&secondInputs[index].Source.Reference, &secondInputs[index].Source.Record, id)
		secondInputs[index].Provenance.SourceID = id
	}
	secondManifest := portableToolLockManifestV1("demo", "1.2.3", "2", secondInputs)
	secondPlan.Tools[0].Provenance.ManifestDigest = secondManifest.Reference.Digest
	secondDAG, err := BuildPortableToolProviderDAGV1(firstDAG.ProviderPlan, secondPlan, firstDAG.Domains)
	if err != nil {
		t.Fatal(err)
	}
	secondReleases := []PortableToolReleaseManifestInputV1{{Scope: "application", Tool: "demo", Manifest: secondManifest}}
	second, err := BuildPortableToolLockV1(secondDAG, secondReleases, secondInputs)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := CanonicalPortableToolLockBytesV1(first)
	secondBytes, _ := CanonicalPortableToolLockBytesV1(second)
	if bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("release revision and manifest identity did not distinguish portable tool locks")
	}
	if first.Plan.PortableToolPlan.Tools[0].SelectedClosureDigest != second.Plan.PortableToolPlan.Tools[0].SelectedClosureDigest {
		t.Fatal("test did not preserve selected closure identity")
	}
}

func portableToolLockFixtureV1(t *testing.T) (PortableToolProviderDAGV1, []PortableToolReleaseManifestInputV1, []PortableToolArtifactAcquisitionInputV1) {
	t.Helper()
	plan := clonePortableToolPlanForPortableToolDAGV1(representativePortableToolPlanV1())
	binding := &plan.Tools[0].Responsibilities.BindingArtifacts[0]
	binding.Record.Value["filename"] = "demo.whl"
	binding.Record.Value["size"] = "12"
	binding.Record.Value["sha256"] = string(portableToolTestDigest)
	refreshPortableToolTestRecordDigest(&binding.Reference, binding.Record)
	payload := &plan.Tools[0].Responsibilities.Payloads[0]
	setPortableToolTestRecordID(&payload.Reference, &payload.Record, "tool:demo/releases/1.2.3/payloads/demo-linux-amd64")
	payload.Record.Value["logical_path"] = "payloads/demo.tar"
	payload.Record.Value["kind"] = "tar"
	payload.Record.Value["size"] = "34"
	payload.Record.Value["sha256"] = string(portableToolLockPayloadDigest)
	refreshPortableToolTestRecordDigest(&payload.Reference, payload.Record)
	bindingSource := portableToolTestSelectedRecord(
		PortableToolArtifactSourceRecordSchemaV1,
		"tool:demo/releases/1.2.3/revisions/1/sources/binding",
		canonical.Object{
			"sha256":     string(portableToolTestDigest),
			"mirrors":    []any{"https://mirror.example/demo.whl", "https://fallback.example/demo.whl"},
			"provenance": []any{"https://upstream.example/demo.whl"}, "diagnostics": []any{},
		},
	)
	payloadSource := portableToolTestSelectedRecord(
		PortableToolArtifactSourceRecordSchemaV1,
		"tool:demo/releases/1.2.3/revisions/1/sources/payload",
		canonical.Object{
			"sha256":     string(portableToolLockPayloadDigest),
			"mirrors":    []any{"https://mirror.example/demo.tar"},
			"provenance": []any{"https://upstream.example/demo.tar"}, "diagnostics": []any{},
		},
	)
	inputs := []PortableToolArtifactAcquisitionInputV1{
		{
			Scope: "application", Tool: "demo", Artifact: binding.Reference,
			Descriptor: providerstore.ArtifactDescriptor{LogicalPath: "bindings/demo.whl", Kind: "wheel", Size: "12", SHA256: portableToolTestDigest},
			Source:     bindingSource,
			Provenance: providerstore.AcquisitionProvenance{
				OperationID: "local-operation", Outcome: providerstore.AcquisitionOutcomeNetwork,
				SourceID: bindingSource.Reference.ID, SuccessfulMirror: "https://mirror.example/demo.whl", Redirects: 2,
			},
		},
		{
			Scope: "application", Tool: "demo", Artifact: payload.Reference,
			Descriptor: providerstore.ArtifactDescriptor{LogicalPath: "payloads/demo.tar", Kind: "tar", Size: "34", SHA256: portableToolLockPayloadDigest},
			Source:     payloadSource,
			Provenance: providerstore.AcquisitionProvenance{
				OperationID: "local-cache-operation", Outcome: providerstore.AcquisitionOutcomeCacheHit,
				SourceID: payloadSource.Reference.ID,
			},
		},
	}
	manifest := portableToolLockManifestV1("demo", "1.2.3", "1", inputs)
	plan.Tools[0].Provenance.ManifestDigest = manifest.Reference.Digest
	dag, err := BuildPortableToolProviderDAGV1(
		portableToolProviderPlanFixtureV1(), plan,
		[]PortableToolProviderDomainSetV1{portableToolProviderDomainV1("application")},
	)
	if err != nil {
		t.Fatal(err)
	}
	return dag, []PortableToolReleaseManifestInputV1{{Scope: "application", Tool: "demo", Manifest: manifest}}, inputs
}

func portableToolLockManifestV1(tool, version, revision string, inputs []PortableToolArtifactAcquisitionInputV1) PortableToolSelectedRecordV1 {
	mappings := make([]any, 0, len(inputs))
	for _, input := range inputs {
		mappings = append(mappings, canonical.Object{
			"artifact_sha256": string(input.Descriptor.SHA256),
			"artifact":        canonical.Object{"id": input.Artifact.ID, "digest": string(input.Artifact.Digest)},
			"source":          canonical.Object{"id": input.Source.Reference.ID, "digest": string(input.Source.Reference.Digest)},
		})
	}
	return portableToolTestSelectedRecord(
		PortableToolReleaseManifestRecordSchemaV1,
		"tool:"+tool+"/releases/"+version+"/revisions/"+revision+"/manifest",
		canonical.Object{
			"tool": tool, "version": version, "revision": revision,
			"aliases": []any{}, "artifact_sources": mappings, "provenance": []any{},
			"targets":             []any{canonical.Object{"id": "tool:" + tool + "/releases/" + version + "/targets/debian/12/amd64", "digest": string(portableToolTestDigest)}},
			"validation_profiles": []any{canonical.Object{"id": "tool:" + tool + "/releases/" + version + "/validation/profiles/default", "digest": string(portableToolTestDigest)}},
			"contract":            canonical.Object{"id": "tool:" + tool + "/releases/" + version + "/contract", "digest": string(portableToolTestDigest)},
		},
	)
}

func refreshPortableToolLockManifestDigestV1(lock *PortableToolLockV1) {
	refreshPortableToolTestRecordDigest(&lock.Releases[0].Manifest.Reference, lock.Releases[0].Manifest.Record)
	lock.Plan.PortableToolPlan.Tools[0].Provenance.ManifestDigest = lock.Releases[0].Manifest.Reference.Digest
}

func refreshPortableToolLockSourceDigestV1(lock *PortableToolLockV1, acquisition int) {
	refreshPortableToolTestRecordDigest(&lock.Acquisitions[acquisition].Source.Reference, lock.Acquisitions[acquisition].Source.Record)
	mappings := lock.Releases[0].Manifest.Record.Value["artifact_sources"].([]any)
	for _, raw := range mappings {
		mapping := raw.(canonical.Object)
		artifact := mapping["artifact"].(canonical.Object)
		if artifact["id"] == lock.Acquisitions[acquisition].Artifact.ID {
			mapping["source"].(canonical.Object)["digest"] = string(lock.Acquisitions[acquisition].Source.Reference.Digest)
		}
	}
	refreshPortableToolLockManifestDigestV1(lock)
}
