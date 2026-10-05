package dockerdeploy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

// Only Docker observations are substituted. Archive publication, selected
// provenance, extraction, inventory sealing and generation publication are real.
type portableVerificationFixtureV1 struct {
	currentBuildVerificationFixtureV1
	dir        string
	operation  *deploy.OperationLock
	portable   InspectedImageCandidate
	images     *pendingPublicationImagesV1
	inventory  []portableRuntimeInventoryEntryV1
	archive    []byte
	response   []byte
	inspected  []canonical.Digest
	aliasReads int
}

func newPortableVerificationFixtureV1(t *testing.T, environmentOnly bool) *portableVerificationFixtureV1 {
	t.Helper()
	f := &portableVerificationFixtureV1{currentBuildVerificationFixtureV1: baseOnlyCurrentBuildVerificationFixtureV1(t)}
	f.dir = filepath.Dir(filepath.Dir(f.store.Root()))
	var err error
	f.current.Lock.Base.AuthorReference, err = deploy.EffectiveBaseImageV1(f.runtime.Document, deploy.EmptyPackageOverridesV1("demo"))
	if err != nil {
		t.Fatal(err)
	}
	f.base.Descriptor.AuthorReference = f.current.Lock.Base.AuthorReference
	request, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{Image: f.current.Lock.Base.AuthorReference, Exports: map[string]blueprint.BaseExecutableExport{}})
	if err != nil {
		t.Fatal(err)
	}
	baseNode, err := providers.BaseNodeSpec(request)
	if err != nil {
		t.Fatal(err)
	}
	tools := buildLockAssemblyPortableToolsV1(t, f.store, providers.ProviderPlanV1{Schema: providers.ProviderPlanSchemaV1, Nodes: []providers.NodeSpec{baseNode}, Edges: []providers.ProviderEdgeV1{}}, "base")
	tools.Plan.PortableToolPlan.Tools[0].Runtime = &providers.PortableToolRuntimeProjectionV1{InstallRoot: "/opt/reploy/tools", Environment: []providers.PortableToolEnvironmentVariableV1{{Name: "DEMO_HOME", Value: "/opt/reploy/tools/demo"}}}
	if environmentOnly {
		tools.Plan.PortableToolPlan.Tools[0].Responsibilities.Payloads = []providers.PortableToolSelectedRecordV1{}
		tools.Acquisitions = []providers.PortableToolArtifactAcquisitionLockV1{}
	} else {
		entries := []portableRuntimeTestInventoryTarEntry{
			{path: "/demo", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o755},
			{path: "/demo/bin", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o755},
			{path: "/demo/bin/demo", kind: providerstore.ArchiveEntryKindRegular, mode: 0o755, content: []byte("exact offline executable")},
		}
		var compressed bytes.Buffer
		zipper := gzip.NewWriter(&compressed)
		if _, err := zipper.Write(portableRuntimeTestInventoryTar(t, entries...)); err != nil {
			t.Fatal(err)
		}
		if err := zipper.Close(); err != nil {
			t.Fatal(err)
		}
		descriptor, err := f.store.Publish(t.Context(), "portable/demo.tar.gz", "jdk-archive", bytes.NewReader(compressed.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		artifact := &tools.Plan.PortableToolPlan.Tools[0].Responsibilities.Payloads[0]
		for key, value := range (canonical.Object{"logical_path": descriptor.LogicalPath, "size": descriptor.Size, "sha256": string(descriptor.SHA256), "entries": "3", "archive_root": "demo", "executables": []any{"demo/bin/demo"}, "unpacked_size": fmt.Sprint(len(entries[2].content))}) {
			artifact.Record.Value[key] = value
		}
		artifact.Reference.Digest, err = canonical.Sum("portable-tool-record", "portable-tool-record-v1", artifact.Record.Value)
		if err != nil {
			t.Fatal(err)
		}
		acquisition := &tools.Acquisitions[0]
		acquisition.Artifact, acquisition.Descriptor = artifact.Reference, descriptor
		acquisition.Source.Record.Value["sha256"] = string(descriptor.SHA256)
		acquisition.Source.Reference.Digest, err = canonical.Sum("portable-tool-record", "portable-tool-record-v1", acquisition.Source.Record.Value)
		if err != nil {
			t.Fatal(err)
		}
		manifest := &tools.Releases[0].Manifest
		manifest.Record.Value["artifact_sources"] = []any{canonical.Object{"artifact_sha256": string(descriptor.SHA256), "artifact": canonical.Object{"id": artifact.Reference.ID, "digest": string(artifact.Reference.Digest)}, "source": canonical.Object{"id": acquisition.Source.Reference.ID, "digest": string(acquisition.Source.Reference.Digest)}}}
		manifest.Reference.Digest, err = canonical.Sum("portable-tool-record", "portable-tool-record-v1", manifest.Record.Value)
		if err != nil {
			t.Fatal(err)
		}
		tools.Plan.PortableToolPlan.Tools[0].Provenance.ManifestDigest = manifest.Reference.Digest
	}
	tools.Plan, err = providers.BuildPortableToolProviderDAGV1(tools.Plan.ProviderPlan, tools.Plan.PortableToolPlan, tools.Plan.Domains)
	if err != nil {
		t.Fatal(err)
	}
	if err := providers.ValidatePortableToolLockV1(tools); err != nil {
		t.Fatal(err)
	}
	lock := &f.current.Lock
	lock.PortableTools = &tools
	lock.BasePlanDigest, err = providers.ProviderNodePlanDigest(baseNode)
	if err != nil {
		t.Fatal(err)
	}
	f.portable = f.base
	f.portable.Descriptor.ConfigDigest = rendererDigest("c")
	f.portable.Descriptor.AuthorReference = string(f.portable.Descriptor.ConfigDigest)
	f.portable.Descriptor.ImmutableReference = f.portable.Descriptor.AuthorReference
	f.portable.Descriptor.RootFSDiffIDs = append([]canonical.Digest{}, f.base.Descriptor.RootFSDiffIDs...)
	if !environmentOnly {
		f.portable.Descriptor.RootFSDiffIDs = append(f.portable.Descriptor.RootFSDiffIDs, rendererDigest("d"))
	}
	f.portable.Image, err = realizedImageFromDescriptor(f.portable.Descriptor)
	if err != nil {
		t.Fatal(err)
	}
	f.portable.Config.Environment = []deploy.ConfigEnvironmentVariable{{Name: "DEMO_HOME", Value: "/opt/reploy/tools/demo"}}
	transaction, err := deploy.PortableRuntimeLayerTransactionDigestV1(tools, f.base.Image, f.portable.Image)
	if err != nil {
		t.Fatal(err)
	}
	lock.PortableRuntimeLayer = &deploy.PortableRuntimeLayerV1{Schema: deploy.PortableRuntimeLayerSchemaV1, Upstream: f.base.Image, Result: f.portable.Image, TransactionDigest: transaction}
	f.runtimeImage.Descriptor.RootFSDiffIDs = append(append([]canonical.Digest{}, f.portable.Descriptor.RootFSDiffIDs...), rendererDigest("e"), rendererDigest("f"))
	f.runtimeImage.Image, err = realizedImageFromDescriptor(f.runtimeImage.Descriptor)
	if err != nil {
		t.Fatal(err)
	}
	f.runtimeImage.Config = f.portable.Config
	lock.RuntimeLayer = testApplicationRuntimeLayerV1(t, lock.Platform, f.portable.Image, f.runtimeImage.Image)
	f.final.Descriptor.RootFSDiffIDs = f.runtimeImage.Descriptor.RootFSDiffIDs
	f.final.Image, err = realizedImageFromDescriptor(f.final.Descriptor)
	if err != nil {
		t.Fatal(err)
	}
	f.final.Config = f.portable.Config
	lock.FinalImage = f.final.Image
	policy, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	lock.ValidationRecord, err = deploy.PublishPrefixValidation(t.Context(), f.store, deploy.PrefixValidationV1{Schema: deploy.PrefixValidationSchemaV1, SubjectRootFS: lock.FinalImage.RootFSSubject, RuntimePolicy: policy, Profiles: []providers.ValidationEvidence{}, ExposedOutputs: []providers.ExecutableEvidence{}})
	if err != nil {
		t.Fatal(err)
	}
	f.final.Labels = map[string]string{"org.example.vendor": "inherited"}
	labels, err := deploy.PrefixValidationLabels(lock.FinalImage.RootFSSubject, lock.ValidationRecord)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range labels {
		f.final.Labels[label.Name] = label.Value
	}
	if err := deploy.ValidateBuildLockV1(*lock, registry.ValidateRequirementProfileV1); err != nil {
		t.Fatal(err)
	}
	payloads, err := MaterializePortableRuntimePayloadsLockedV1(t.Context(), f.store, tools)
	if err != nil {
		t.Fatal(err)
	}
	f.inventory = append([]portableRuntimeInventoryEntryV1{}, payloads.sealedInventory...)
	if err := payloads.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if !environmentOnly {
		f.archive = portableRuntimeTestInventoryTar(t,
			portableRuntimeTestInventoryTarEntry{path: "/demo", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o555},
			portableRuntimeTestInventoryTarEntry{path: "/demo/bin", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o555},
			portableRuntimeTestInventoryTarEntry{path: "/demo/bin/demo", kind: providerstore.ArchiveEntryKindRegular, mode: 0o555, content: []byte("exact offline executable")})
		f.response = portableRuntimeTestProbeResponse(t, f.inventory)
	}
	f.operation, err = deploy.AcquireOperationLock(t.Context(), f.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.operation.Unlock() })
	f.images = &pendingPublicationImagesV1{images: map[string]providers.RealizedImageV1{}, operation: f.operation, t: t, sequence: 74}
	f.current.State, err = publishBuild(t.Context(), f.operation, f.store, publicationInput(t, f.dir, *lock), f.images.backend(f.store, f.dir))
	if err != nil {
		t.Fatal(err)
	}
	f.current.Generation = *f.current.State.Current
	previousDocker, previousPrepare, previousOpen := runDockerOutput, preparePortableRuntimeProbeWorkspaceV1, openPortableRuntimeProbeSessionV1
	t.Cleanup(func() {
		runDockerOutput, preparePortableRuntimeProbeWorkspaceV1, openPortableRuntimeProbeSessionV1 = previousDocker, previousPrepare, previousOpen
	})
	runDockerOutput = func(_ context.Context, args ...string) (string, error) {
		f.aliasReads++
		image, found := f.images.images[args[len(args)-1]]
		if !found {
			return "", errors.New("Error response from daemon: No such image: " + args[len(args)-1])
		}
		if len(args) == 5 && args[2] == "--format" {
			return string(image.ConfigDigest), nil
		}
		if len(args) != 3 || args[0] != "image" || args[1] != "inspect" {
			t.Fatalf("unexpected Docker mutation: %v", args)
		}
		record := dockerImageInspectRecord{ID: string(image.ConfigDigest), OS: "linux", Architecture: "amd64", Config: &dockerImageConfig{}}
		for _, digest := range f.portable.Descriptor.RootFSDiffIDs {
			record.RootFS.Layers = append(record.RootFS.Layers, string(digest))
		}
		data, err := json.Marshal([]dockerImageInspectRecord{record})
		return string(data), err
	}
	preparePortableRuntimeProbeWorkspaceV1 = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return PreparedProbeWorkspace{}, func() error { return nil }, nil
	}
	openPortableRuntimeProbeSessionV1 = func(_ context.Context, descriptor deploy.ImageDescriptor, _ PreparedProbeWorkspace) (*ImageValidationSession, error) {
		if !reflect.DeepEqual(descriptor, f.final.Descriptor) {
			t.Fatal("content probe did not inspect actual final image")
		}
		session := portableRuntimeInventoryOwnershipTestSession(t, f.archive, f.response)
		run := session.runDocker
		session.runDocker = func(spec CommandSpec, options RunOptions) error {
			if spec.Args[0] == "rm" {
				return nil
			}
			if spec.Args[0] == "cp" && spec.Args[1] != "portable-runtime-inventory:/opt/reploy/tools/demo" {
				t.Fatalf("wrong inventory destination: %v", spec.Args)
			}
			return run(spec, options)
		}
		return session, nil
	}
	return f
}

func (f *portableVerificationFixtureV1) verify(t *testing.T, current CurrentBuild) (CurrentBuildVerificationResultV1, error) {
	t.Helper()
	return verifyLoadedCurrentBuildV1(t.Context(), CurrentBuildVerificationInputV1{Operation: f.operation, DeploymentDir: f.dir, Store: f.store, Current: current, Runtime: f.runtime, RunValidation: func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
		return []providers.ValidationEvidence{}, []providers.ExecutableEvidence{}, nil
	}}, currentBuildVerificationBackendV1{verifyClosure: deploy.BuildLockStoreClosure, inspectImage: func(_ context.Context, input BuiltImageCandidate, platform blueprint.Platform) (InspectedImageCandidate, error) {
		f.inspected = append(f.inspected, input.ImageID)
		if platform != current.Lock.Platform {
			t.Fatal("wrong image inspection target")
		}
		for _, image := range []InspectedImageCandidate{f.base, f.portable, f.runtimeImage, f.final} {
			if image.Image.ConfigDigest == input.ImageID {
				return image, nil
			}
		}
		return InspectedImageCandidate{}, errors.New("unexpected image")
	}})
}

func TestPortableCurrentVerificationUsesExactFinalImageV1(t *testing.T) {
	for _, environmentOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(environmentOnly), func(t *testing.T) {
			f := newPortableVerificationFixtureV1(t, environmentOnly)
			before := pendingOwnedFilesystemSnapshotV1(t, f.dir, f.store.Root())
			result, err := f.verify(t, f.current)
			if err != nil {
				t.Fatal(err)
			}
			if result.Images != 4 || f.aliasReads != 6 || !reflect.DeepEqual(f.inspected, []canonical.Digest{f.base.Image.ConfigDigest, f.portable.Image.ConfigDigest, f.runtimeImage.Image.ConfigDigest, f.final.Image.ConfigDigest}) {
				t.Fatalf("result=%+v images=%v alias reads=%d", result, f.inspected, f.aliasReads)
			}
			if after := pendingOwnedFilesystemSnapshotV1(t, f.dir, f.store.Root()); !reflect.DeepEqual(before, after) {
				t.Fatal("read-only audit changed durable ownership or storage")
			}
		})
	}
}

func TestPortableVerificationRejectsTargetAndProvenanceDriftV1(t *testing.T) {
	for _, fault := range []string{"target", "provenance", "upstream", "environment-only filesystem"} {
		t.Run(fault, func(t *testing.T) {
			f := newPortableVerificationFixtureV1(t, fault == "environment-only filesystem")
			switch fault {
			case "target":
				f.portable.Descriptor.Platform, _ = blueprint.ParsePlatform("linux/arm64")
			case "provenance":
				f.current.Lock.PortableTools.Acquisitions[0].Source.Record.Value["sha256"] = string(rendererDigest("9"))
			case "upstream":
				f.current.Lock.PortableRuntimeLayer.Upstream = f.final.Image
			case "environment-only filesystem":
				f.current.Lock.PortableRuntimeLayer.Result.RootFSSubject = rendererDigest("9")
				if err := verifyCurrentPortableContentV1(t.Context(), f.store, f.current.Lock, f.final); err == nil {
					t.Fatal("environment-only audit accepted a changed upstream filesystem")
				}
				return
			}
			if _, err := f.verify(t, f.current); err == nil {
				t.Fatalf("accepted changed %s", fault)
			}
		})
	}
}

func TestPortableCurrentVerificationRejectsFinalContentDriftV1(t *testing.T) {
	for _, fault := range []string{"substituted", "missing", "extra", "mode", "ownership", "environment", "probe cleanup", "staging cleanup", "stored bytes", "missing primary", "missing companion", "retarget primary", "retarget companion", "after metadata", "during content"} {
		t.Run(fault, func(t *testing.T) {
			f := newPortableVerificationFixtureV1(t, false)
			pairs, err := ProjectEnvironmentOwnedReferencesV1(f.current.Generation, f.current.Lock, "demo", f.dir)
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "substituted", "missing", "extra", "mode":
				entries := []portableRuntimeTestInventoryTarEntry{{path: "/demo", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o555}, {path: "/demo/bin", kind: providerstore.ArchiveEntryKindDirectory, mode: 0o555}, {path: "/demo/bin/demo", kind: providerstore.ArchiveEntryKindRegular, mode: 0o555, content: []byte("exact offline executable")}}
				switch fault {
				case "substituted":
					entries[2].content = []byte("substituted")
				case "missing":
					entries = entries[:2]
				case "extra":
					entries = append(entries, portableRuntimeTestInventoryTarEntry{path: "/demo/extra", kind: providerstore.ArchiveEntryKindRegular, mode: 0o444, content: []byte("extra")})
				case "mode":
					entries[2].mode = 0o777
				}
				f.archive = portableRuntimeTestInventoryTar(t, entries...)
			case "ownership":
				f.response = bytes.ReplaceAll(f.response, []byte(`"uid":"0"`), []byte(`"uid":"1000"`))
			case "environment":
				f.runtimeImage.Config.Environment = []deploy.ConfigEnvironmentVariable{{Name: "DEMO_HOME", Value: "/wrong"}}
				f.portable.Config.Environment = f.runtimeImage.Config.Environment
				f.final.Config.Environment = []deploy.ConfigEnvironmentVariable{{Name: "DEMO_HOME", Value: "/wrong"}}
			case "stored bytes":
				file, err := f.store.InspectArtifactPath(f.current.Lock.PortableTools.Acquisitions[0].Descriptor)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(file, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing primary", "missing companion":
				index := 0
				if fault == "missing companion" {
					index = 1
				}
				delete(f.images.images, pairs[index].Reference)
			case "retarget primary", "retarget companion":
				index := 0
				if fault == "retarget companion" {
					index = 1
				}
				image := f.images.images[pairs[index].Reference]
				image.ConfigDigest = rendererDigest("9")
				f.images.images[pairs[index].Reference] = image
			case "after metadata":
				previous := runDockerOutput
				runDockerOutput = func(ctx context.Context, args ...string) (string, error) {
					if f.aliasReads == 2 {
						delete(f.images.images, pairs[1].Reference)
					}
					return previous(ctx, args...)
				}
			case "during content":
				previous := openPortableRuntimeProbeSessionV1
				openPortableRuntimeProbeSessionV1 = func(ctx context.Context, descriptor deploy.ImageDescriptor, workspace PreparedProbeWorkspace) (*ImageValidationSession, error) {
					delete(f.images.images, pairs[1].Reference)
					return previous(ctx, descriptor, workspace)
				}
			case "probe cleanup":
				previous := openPortableRuntimeProbeSessionV1
				openPortableRuntimeProbeSessionV1 = func(ctx context.Context, descriptor deploy.ImageDescriptor, workspace PreparedProbeWorkspace) (*ImageValidationSession, error) {
					session, err := previous(ctx, descriptor, workspace)
					run := session.runDocker
					session.runDocker = func(spec CommandSpec, options RunOptions) error {
						if spec.Args[0] == "rm" {
							return errors.New("probe cleanup failed")
						}
						return run(spec, options)
					}
					return session, err
				}
			case "staging cleanup":
				previous := openPortableRuntimeProbeSessionV1
				openPortableRuntimeProbeSessionV1 = func(ctx context.Context, descriptor deploy.ImageDescriptor, workspace PreparedProbeWorkspace) (*ImageValidationSession, error) {
					matches, err := filepath.Glob(filepath.Join(f.store.Root(), "tmp", "runtime-payloads-*"))
					if err != nil || len(matches) != 1 {
						t.Fatalf("staging workspaces: %v %v", matches, err)
					}
					if err := removeSourceBuilderPortableToolWorkspaceV1(matches[0]); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(matches[0], []byte("replacement file"), 0o600); err != nil {
						t.Fatal(err)
					}
					return previous(ctx, descriptor, workspace)
				}
			}
			if result, err := f.verify(t, f.current); err == nil || result != (CurrentBuildVerificationResultV1{}) {
				t.Fatalf("accepted %s: %+v %v", fault, result, err)
			}
		})
	}
}

func TestPortableReuseChecksBothOwnersWithoutFullVerificationV1(t *testing.T) {
	for _, validated := range []bool{false, true} {
		for _, fault := range []string{"none", "missing primary", "missing companion", "retarget primary", "retarget companion"} {
			t.Run(fmt.Sprintf("validated=%v/%s", validated, fault), func(t *testing.T) {
				f := newPortableVerificationFixtureV1(t, false)
				current := f.current
				overrides := deploy.EmptyPackageOverridesV1("demo")
				if validated {
					inputs, err := ValidatedBuildInputs(f.runtime.Document, current.State.Overlay, overrides, f.dir, current.State.Platform)
					if err != nil {
						t.Fatal(err)
					}
					images := &validatedPublicationImagesV1{pendingPublicationImagesV1: *f.images}
					if _, err := publishValidatedBuild(t.Context(), f.operation, f.store, "demo", f.dir, current.Lock, inputs, images.backend(f.dir)); err != nil {
						t.Fatal(err)
					}
					candidate, found, err := LoadValidatedBuildCandidate(t.Context(), f.operation, f.store, f.runtime.Document, current.State, overrides, f.dir, false, false)
					if err != nil || !found || f.aliasReads != 0 {
						t.Fatalf("offline candidate: %v %v reads=%d", found, err, f.aliasReads)
					}
					current = candidate.Current
					if result, err := f.verify(t, current); err != nil || result.Images != 4 {
						t.Fatalf("validated content audit: %+v %v", result, err)
					}
				}
				pairs, err := ProjectEnvironmentOwnedReferencesV1(current.Generation, current.Lock, "demo", f.dir)
				if err != nil {
					t.Fatal(err)
				}
				index := 0
				if fault == "missing companion" || fault == "retarget companion" {
					index = 1
				}
				if fault != "none" {
					if fault == "missing primary" || fault == "missing companion" {
						delete(f.images.images, pairs[index].Reference)
					} else {
						image := f.images.images[pairs[index].Reference]
						image.ConfigDigest = rendererDigest("9")
						f.images.images[pairs[index].Reference] = image
					}
				}
				// Cheap runtime checks remain usable after portable bytes are cleaned.
				if _, err := f.store.Remove(); err != nil {
					t.Fatal(err)
				}
				reads := f.aliasReads
				var found bool
				if validated {
					_, found, err = LoadValidatedBuildCandidate(t.Context(), f.operation, f.store, f.runtime.Document, current.State, overrides, f.dir, false, true)
				} else {
					_, found, err = ValidateCurrentBuild(t.Context(), f.operation, f.store, "demo", f.dir)
				}
				if (fault == "none") != (err == nil && found) || f.aliasReads == reads {
					t.Fatalf("cheap acceptance: found=%v err=%v reads=%d", found, err, f.aliasReads-reads)
				}
				reads = f.aliasReads
				if _, found, err := LoadRecordedCurrentBuildV1(t.Context(), f.operation, f.store, "demo", f.dir); err != nil || !found || f.aliasReads != reads {
					t.Fatalf("offline current: %v %v", found, err)
				}
			})
		}
	}
}

func TestPortableProviderReuseCannotSkipOwnerChecksV1(t *testing.T) {
	for _, validated := range []bool{false, true} {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("validated=%v/missing=%v", validated, missing), func(t *testing.T) {
				f := newPortableVerificationFixtureV1(t, false)
				candidate := ValidatedBuildCandidateV1{Current: f.current}
				if validated {
					inputs, err := ValidatedBuildInputs(f.runtime.Document, f.current.State.Overlay, deploy.EmptyPackageOverridesV1("demo"), f.dir, f.current.State.Platform)
					if err != nil {
						t.Fatal(err)
					}
					images := &validatedPublicationImagesV1{pendingPublicationImagesV1: *f.images}
					if _, err := publishValidatedBuild(t.Context(), f.operation, f.store, "demo", f.dir, f.current.Lock, inputs, images.backend(f.dir)); err != nil {
						t.Fatal(err)
					}
					var found bool
					candidate, found, err = LoadValidatedBuildCandidate(t.Context(), f.operation, f.store, f.runtime.Document, f.current.State, deploy.EmptyPackageOverridesV1("demo"), f.dir, false, false)
					if err != nil || !found || candidate.Current.Generation == f.current.Generation {
						t.Fatalf("independent trial owner: %v %v", found, err)
					}
				}
				pairs, err := ProjectEnvironmentOwnedReferencesV1(candidate.Current.Generation, candidate.Current.Lock, "demo", f.dir)
				if err != nil {
					t.Fatal(err)
				}
				if missing {
					delete(f.images.images, pairs[1].Reference)
				}
				executed := false
				result, err := runLockedProviderBuildV1(t.Context(), LockedProviderBuildRunInputV1{Operation: f.operation, Store: f.store, DeploymentDir: f.dir, Runtime: StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux, UID: 1000, GID: 1000}}, providerBuildRunBackend{
					prepare: func(context.Context, LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error) {
						return LockedProviderBuildPreparationV1{Reused: true, Current: &f.current, ReusedCandidate: validated, ValidatedCandidate: &candidate}, nil
					},
					execute: func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error) {
						executed = true
						return LockedProviderBuildExecutionResultV1{Reused: true}, nil
					},
				})
				if missing && (err == nil || executed) || !missing && (err != nil || !executed || !result.Reused) {
					t.Fatalf("result=%+v error=%v executed=%v", result, err, executed)
				}
				if f.aliasReads != 2 {
					t.Fatalf("optional verification skipped paired owner checks: %d", f.aliasReads)
				}
			})
		}
	}
}

func TestPortablePublishedLifecyclePreservesInstalledOwnerV1(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(fmt.Sprintf("replacement=%v", replacement), func(t *testing.T) {
			f := newPortableVerificationFixtureV1(t, false)
			f.current.State.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
			f.current.State.BlueprintSource = "synthetic portable lifecycle"
			if err := f.operation.CommitStateV1(f.current.State.Current, f.current.State); err != nil {
				t.Fatal(err)
			}
			inputs, err := ValidatedBuildInputs(f.runtime.Document, f.current.State.Overlay, deploy.EmptyPackageOverridesV1("demo"), f.dir, f.current.State.Platform)
			if err != nil {
				t.Fatal(err)
			}
			images := &validatedPublicationImagesV1{pendingPublicationImagesV1: *f.images}
			record, err := publishValidatedBuild(t.Context(), f.operation, f.store, "demo", f.dir, f.current.Lock, inputs, images.backend(f.dir))
			if err != nil {
				t.Fatal(err)
			}
			candidate, found, err := LoadValidatedBuildCandidate(t.Context(), f.operation, f.store, f.runtime.Document, f.current.State, deploy.EmptyPackageOverridesV1("demo"), f.dir, true, true)
			if err != nil || !found {
				t.Fatalf("validated acceptance: %v %v", found, err)
			}
			if _, err := f.verify(t, candidate.Current); err != nil {
				t.Fatal(err)
			}
			dest := t.TempDir()
			destOperation, err := deploy.AcquireOperationLock(t.Context(), dest)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = destOperation.Unlock() })
			destStore, err := providerstore.NewStore(dest)
			if err != nil {
				t.Fatal(err)
			}
			destImages := &pendingPublicationImagesV1{images: images.images, operation: destOperation, t: t}
			installed, err := publishInstalledBuildV1(t.Context(), f.operation, destOperation, f.store, destStore, InstalledBuildPublicationInputV1{Environment: "demo", SourceDeploymentDir: f.dir, DestinationDeploymentDir: dest, Source: f.current, Build: f.current.Lock, Installation: installedBuildPublicationInstallation(dest), References: fixedPublicationReferences(t, dest, 83)}, installedOwnedPublicationBackendV1(t, f.operation, destImages, destStore, dest))
			if err != nil {
				t.Fatal(err)
			}
			destPairs, err := ProjectEnvironmentOwnedReferencesV1(*installed.Current, f.current.Lock, "demo", dest)
			if err != nil {
				t.Fatal(err)
			}
			sourcePairs, err := ProjectEnvironmentOwnedReferencesV1(f.current.Generation, f.current.Lock, "demo", f.dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.operation.Unlock(); err != nil {
				t.Fatal(err)
			}
			backend := testStagedRemovalBackendV1(f.store)
			backend.acquire = func(ctx context.Context, dir string) (*deploy.OperationLock, error) {
				op, err := deploy.AcquireExistingOperationLock(ctx, dir)
				if err == nil {
					images.operation = op
				}
				return op, err
			}
			retirement := validatedRetirementImagesBackendV1(t, images, "", false)
			bindStagedRetirementBackendV1(&backend, retirement)
			if replacement {
				document := f.runtime.Document
				document.Environment.ID = "replacement"
				_, err = forceReplaceStagedDesiredStateV1(t.Context(), ForceReplaceStagedDesiredStateInputV1{DesiredState: DesiredStateStageInputV1{DeploymentDir: f.dir, Document: document, BlueprintSource: "replacement"}}, forceReplaceStagedDesiredStateBackendV1{acquire: backend.acquire, newStore: backend.newStore, recoverPending: backend.recoverPending, admit: backend.admit, complete: backend.complete, stopOwned: backend.stopOwned, removeReference: retirement.removeReference, removeCompanion: retirement.removeCompanion, cleanupStorage: cleanupValidatedBuildStorage, commit: func(op *deploy.OperationLock, expected *deploy.EnvironmentGenerationState, state deploy.StateV1) error {
					for _, ref := range []string{sourcePairs[0].Reference, sourcePairs[1].Reference, record.ImageReference, record.Companion.Reference} {
						if _, found := images.images[ref]; found {
							t.Fatal("replacement committed before all former owners retired")
						}
					}
					return op.CommitStateV1(expected, state)
				}, stageSame: StageDesiredStateV1})
			} else {
				_, err = removeStagedDeploymentV1(t.Context(), StagedDeploymentRemoveInputV1{DeploymentDir: f.dir}, backend)
				if err == nil {
					if _, statErr := os.Stat(f.dir); !errors.Is(statErr, os.ErrNotExist) {
						t.Fatalf("source directory survived removal: %v", statErr)
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(images.images) != 2 {
				t.Fatalf("former source owners survived: %+v", images.images)
			}
			for _, pair := range destPairs {
				if images.images[pair.Reference] != pair.Image {
					t.Fatal("installed owner's independent pair was retired")
				}
			}
			if _, err := deploy.BuildLockStoreClosure(f.current.Lock, destStore, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1); err != nil {
				t.Fatalf("installed closure was not transferred: %v", err)
			}
		})
	}
}
