package dockerdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func reportAuditBackend(f currentBuildVerificationFixtureV1, inspections *int) currentBuildVerificationBackendV1 {
	return currentBuildVerificationBackendV1{
		verifyClosure:              deploy.BuildLockStoreClosure,
		verifyClosureWithArtifacts: deploy.BuildLockStoreClosureWithArtifacts,
		inspectImage: func(_ context.Context, c BuiltImageCandidate, _ blueprint.Platform) (InspectedImageCandidate, error) {
			*inspections++
			for _, image := range []InspectedImageCandidate{f.base, f.runtimeImage, f.final} {
				if c.ImageID == image.Image.ConfigDigest {
					return image, nil
				}
			}
			return InspectedImageCandidate{}, errors.New("unexpected image")
		},
	}
}
func reportAuditInput(f currentBuildVerificationFixtureV1, report bool) CurrentBuildVerificationInputV1 {
	return CurrentBuildVerificationInputV1{Store: f.store, Current: f.current, Runtime: f.runtime, ReportJSON: report,
		RunValidation: func(context.Context, FullImageValidationInput) ([]providers.ValidationEvidence, []providers.ExecutableEvidence, error) {
			return []providers.ValidationEvidence{}, []providers.ExecutableEvidence{}, nil
		},
	}
}
func TestVerifiedBuildReportUsesFullAuditAndInspectedSnapshot(t *testing.T) {
	f := baseOnlyCurrentBuildVerificationFixtureV1(t)
	// These private fields must never enter the public projection.
	secret := "PRIVATE_VERIFY_JSON_SENTINEL"
	for _, image := range []*InspectedImageCandidate{&f.base, &f.runtimeImage, &f.final} {
		image.Config.Environment = append(image.Config.Environment, deploy.ConfigEnvironmentVariable{Name: "PRIVATE", Value: secret})
		image.Labels["private.example/credential"] = secret
	}
	f.final.Labels["private.example/credential"] = secret
	inspections := 0
	result, err := verifyLoadedCurrentBuildV1(t.Context(), reportAuditInput(f, true), reportAuditBackend(f, &inspections))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeVerifiedBuildReport(result.Report)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	if len(object) != 12 || string(object["schema"]) != `"reploy:verified-build"` || string(object["schema_version"]) != "1" {
		t.Fatalf("unexpected public shape: %s", encoded)
	}
	if inspections != 3 || result.Images != 3 || result.StoreObjects != 1 || result.Report.Build.LockDigest != f.current.Generation.BuildLockDigest || result.Report.Build.LocalImageID != f.final.Image.ConfigDigest {
		t.Fatalf("audit observations: %+v inspections=%d", result, inspections)
	}
	if !reflect.DeepEqual(result.Report.Image.RootFSDiffIDs, f.final.Descriptor.RootFSDiffIDs) || !reflect.DeepEqual(result.Report.Base.RootFSDiffIDs, f.base.Descriptor.RootFSDiffIDs) {
		t.Fatal("report lost ordered inspected layers")
	}
	if string(object["artifacts"]) != `{"provider_bundles":[],"portable_acquisitions":[]}` {
		t.Fatalf("empty inventory: %s", object["artifacts"])
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), f.store.Root()) {
		t.Fatalf("report disclosed excluded data: %s", encoded)
	}
	if encoded[len(encoded)-1] != '\n' || strings.Count(string(encoded), "\n") != 1 {
		t.Fatal("invalid framing")
	}
	// Text audit performs the same inspections without constructing a report.
	inspections = 0
	text, err := verifyLoadedCurrentBuildV1(t.Context(), reportAuditInput(f, false), reportAuditBackend(f, &inspections))
	if err != nil || text.Report != nil || inspections != 3 || text.StoreObjects != result.StoreObjects {
		t.Fatalf("text audit changed: %+v %v", text, err)
	}
}
func TestVerifiedBuildReportRetainsOwnersAndNormalizesGroups(t *testing.T) {
	f := baseOnlyCurrentBuildVerificationFixtureV1(t)
	f.runtime.Docker.Sandbox.RuntimeUser.SupplementaryGIDs = []uint32{100, 2, 100, 9}
	c := newVerifiedBuildReportCollector()
	d := providerstore.ArtifactDescriptor{LogicalPath: "shared.tar", Kind: "archive", Size: "7", SHA256: rendererDigest("a")}
	for _, a := range []deploy.BuildLockClosureArtifact{
		{NodeID: "z", Descriptor: d}, {NodeID: "a", Descriptor: d},
		{Descriptor: d, Scope: "scope", Tool: "tool", Artifact: providers.PortableToolRecordReferenceV1{ID: "two", Digest: rendererDigest("b")}},
		{Descriptor: d, Scope: "scope", Tool: "tool", Artifact: providers.PortableToolRecordReferenceV1{ID: "one", Digest: rendererDigest("c")}},
	} {
		if err := c.collect(a); err != nil {
			t.Fatal(err)
		}
	}
	record, err := deploy.LoadPrefixValidation(f.store, f.current.Lock.ValidationRecord)
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.finish(f.current, f.runtime, f.final, record, CurrentBuildVerificationResultV1{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Artifacts.ProviderBundles) != 2 || r.Artifacts.ProviderBundles[0].NodeID != "a" || len(r.Artifacts.PortableAcquisitions) != 2 || r.Artifacts.PortableAcquisitions[0].ArtifactID != "one" {
		t.Fatalf("owners lost or unsorted: %+v", r.Artifacts)
	}
	if !reflect.DeepEqual(r.Runtime.PlannedSandbox.SupplementaryGIDs, []string{"2", "9", "100"}) {
		t.Fatal(r.Runtime.PlannedSandbox.SupplementaryGIDs)
	}
	f.current.Lock.Base.ManifestDigest = ""
	r, err = newVerifiedBuildReportCollector().finish(f.current, f.runtime, f.final, record, CurrentBuildVerificationResultV1{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := encodeVerifiedBuildReport(r)
	if !strings.Contains(string(encoded), `"manifest_digest":null`) {
		t.Fatalf("missing nullable manifest: %s", encoded)
	}
	if err := c.collect(deploy.BuildLockClosureArtifact{NodeID: "a", Descriptor: d}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.finish(f.current, f.runtime, f.final, record, CurrentBuildVerificationResultV1{}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate ownership accepted: %v", err)
	}
}
func TestVerifiedBuildReportExactSizeLimitAndEscaping(t *testing.T) {
	r := &VerifiedBuildReportV1{}
	initial, err := encodeVerifiedBuildReport(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Environment = strings.Repeat("a", VerifiedBuildReportMaxBytes-len(initial))
	encoded, err := encodeVerifiedBuildReport(r)
	if err != nil || len(encoded) != VerifiedBuildReportMaxBytes {
		t.Fatalf("exact limit: %d %v", len(encoded), err)
	}
	r.Environment += "a"
	if encoded, err := encodeVerifiedBuildReport(r); err == nil || encoded != nil {
		t.Fatal("over-limit report published")
	}
	r.Environment = strings.Repeat("\x01", VerifiedBuildReportMaxBytes/6)
	if _, err := encodeVerifiedBuildReport(r); err == nil {
		t.Fatal("JSON escaping omitted from limit")
	}
	c := newVerifiedBuildReportCollector()
	if err := c.collect(deploy.BuildLockClosureArtifact{NodeID: "apt", Descriptor: providerstore.ArtifactDescriptor{LogicalPath: strings.Repeat("x", VerifiedBuildReportMaxBytes)}}); err == nil || len(c.artifacts.ProviderBundles) != 0 {
		t.Fatal("unbounded inventory collection")
	}
}
func TestVerifiedBuildJSONIsWithheldUntilSuccessfulUnlock(t *testing.T) {
	for _, failure := range []string{"none", "audit", "missing-report", "encode", "unlock", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			f := baseOnlyCurrentBuildVerificationFixtureV1(t)
			inspections := 0
			dir := t.TempDir()
			var held *deploy.OperationLock
			unlocked := false
			result, err := verifyCurrentBuildV1(t.Context(), VerifyCurrentBuildInputV1{DeploymentDir: dir, ReportJSON: true}, verifyCurrentBuildBackendV1{
				acquire: func(ctx context.Context, dir string) (*deploy.OperationLock, error) {
					var err error
					held, err = deploy.AcquireOperationLock(ctx, dir)
					return held, err
				},
				unlock: func(lock *deploy.OperationLock) error {
					unlocked = true
					err := lock.Unlock()
					if failure == "unlock" {
						return errors.New("forced unlock failure")
					}
					return err
				},
				newStore:  func(string) (providerstore.Store, error) { return f.store, nil },
				readState: func(*deploy.OperationLock) (deploy.StateV1, bool, error) { return f.current.State, true, nil },
				loadCurrent: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
					return f.current, true, nil
				},
				plan: func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error) { return f.runtime, nil },
				verify: func(ctx context.Context, input CurrentBuildVerificationInputV1) (CurrentBuildVerificationResultV1, error) {
					if held.RequireHeld() != nil || !input.ReportJSON {
						t.Fatal("report audit escaped lock or was not requested")
					}
					if failure == "audit" {
						return CurrentBuildVerificationResultV1{}, errors.New("audit failed")
					}
					if failure == "cancel" {
						return CurrentBuildVerificationResultV1{}, context.Canceled
					}
					if failure == "missing-report" {
						return CurrentBuildVerificationResultV1{}, nil
					}
					result, err := verifyLoadedCurrentBuildV1(ctx, reportAuditInput(f, true), reportAuditBackend(f, &inspections))
					if failure == "encode" && result.Report != nil {
						result.Report.Environment = strings.Repeat("x", VerifiedBuildReportMaxBytes)
					}
					return result, err
				},
			})
			if !unlocked || held.RequireHeld() == nil {
				t.Fatal("lock was not settled")
			}
			if failure == "none" {
				if err != nil || !json.Valid(result.JSON) {
					t.Fatalf("success: %s %v", result.JSON, err)
				}
			} else if err == nil || len(result.JSON) != 0 {
				t.Fatalf("failure emitted success: %+v %v", result, err)
			}
		})
	}
}
