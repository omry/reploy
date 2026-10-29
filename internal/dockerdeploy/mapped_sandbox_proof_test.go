package dockerdeploy

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
)

// Embedding only the old interface deliberately omits the new F observation
// contract. An existing generic Verify success cannot grant code authority.
type withoutMappedSandboxV1 struct {
	deploy.IdentityMappingEvidenceV1
}

func TestMappedSandboxRetainsCompiledProtectedRootsV1(t *testing.T) {
	f := newWaiterFixtureV1(t)
	a := f.start(t)
	lock, found, err := a.prepared.ops.Operation.ReadBuildLock(a.binding.Generation, registry.ValidateRequirementProfileV1)
	if err != nil || !found {
		t.Fatal(err)
	}
	lock.RuntimePolicy.ProtectedPaths = []deploy.ProtectedPathV1{{Path: "/opt/reploy", Kind: deploy.ProtectedPathReployRoot, Owner: "reploy"}}
	digest, err := a.prepared.ops.Operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	a.prepared.state.Current.BuildLockDigest = digest
	a.prepared.state.Current.RuntimePolicyDigest, err = deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		t.Fatal(err)
	}
	a.binding.Generation = digest
	request, err := a.sandboxRequest()
	if err != nil || !slices.Contains(request.RequiredCode, "/opt/reploy") {
		t.Fatal("compiled protected root was lost", request, err)
	}
	f.effect.sandboxChange = func(proof *mappedSandboxProofV1) {
		proof.Code = slices.DeleteFunc(proof.Code, func(code mappedCodePathV1) bool { return code.Path == "/opt/reploy" })
		proof.ObservedCode = slices.Clone(proof.Code)
	}
	if err := verifyMappedSandboxV1(f.ctx, a.prepared.state, f.effect, request); err == nil {
		t.Fatal("observation omitted a compiled protected root")
	}
}

func TestMappedSandboxProofV1(t *testing.T) {
	f := newWaiterFixtureV1(t)
	a := f.start(t)
	request, err := a.sandboxRequest()
	if err != nil {
		t.Fatal(err)
	}
	if request.Binding != f.binding || request.Container != f.id || request.PID != "1234" ||
		!slices.Contains(request.RequiredCode, "/reploy-probe") || len(request.RequiredCode) < 2 {
		t.Fatalf("incomplete exact plan: %+v", request)
	}
	if err := verifyMappedSandboxV1(f.ctx, f.prepared.state, f.effect, request); err != nil {
		t.Fatal(err)
	}
	if err := verifyMappedSandboxV1(f.ctx, f.prepared.state, withoutMappedSandboxV1{f.effect}, request); err == nil {
		t.Fatal("generic effect proof substituted for missing code proof")
	}
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	if err := verifyMappedSandboxV1(ctx, f.prepared.state, f.effect, request); err == nil {
		t.Fatal("cancelled proof accepted")
	}
	f.effect.sandboxFailure = errors.New("observation unavailable")
	if err := verifyMappedSandboxV1(f.ctx, f.prepared.state, f.effect, request); err == nil {
		t.Fatal("provider failure accepted")
	}
	f.effect.sandboxFailure = nil
	invalidRequest := request
	invalidRequest.RequiredCode = []string{"/invalid-\xff"}
	if err := verifyMappedSandboxV1(f.ctx, f.prepared.state, f.effect, invalidRequest); err == nil {
		t.Fatal("uncanonical request accepted")
	}
	finishing, finish := context.WithCancel(f.ctx)
	f.effect.sandboxChange = func(*mappedSandboxProofV1) { finish() }
	if err := verifyMappedSandboxV1(finishing, f.prepared.state, f.effect, request); err == nil {
		t.Fatal("proof completing after cancellation accepted")
	}
	for _, test := range []struct {
		name   string
		change func(*mappedSandboxProofV1)
	}{
		{"wrong request", func(p *mappedSandboxProofV1) { p.Request = rendererDigest("9") }},
		{"wrong network", func(p *mappedSandboxProofV1) { p.NetworkPolicy = rendererDigest("9") }},
		{"writable root", func(p *mappedSandboxProofV1) { p.ReadOnlyRoot = false }},
		{"missing closure", func(p *mappedSandboxProofV1) { p.Code = nil; p.ObservedCode = nil }},
		{"missing observation", func(p *mappedSandboxProofV1) { p.ObservedCode = nil }},
		{"substituted dependency", func(p *mappedSandboxProofV1) { p.ObservedCode[0].Resolved = "/tmp/replacement" }},
		{"missing mount inventory", func(p *mappedSandboxProofV1) { p.Mounts = nil }},
		{"omitted required code", func(p *mappedSandboxProofV1) { p.Code = p.Code[1:]; p.ObservedCode = slices.Clone(p.Code) }},
		{"relative code", func(p *mappedSandboxProofV1) { p.Code[0].Path = "relative"; p.ObservedCode = slices.Clone(p.Code) }},
		{"unclean code", func(p *mappedSandboxProofV1) { p.Code[0].Path = "/app/../app"; p.ObservedCode = slices.Clone(p.Code) }},
		{"root closure shortcut", func(p *mappedSandboxProofV1) { p.Code[0].Path = "/"; p.ObservedCode = slices.Clone(p.Code) }},
		{"invalid code bytes", func(p *mappedSandboxProofV1) { p.Code[0].Path = "/bad\x00path"; p.ObservedCode = slices.Clone(p.Code) }},
		{"relative resolution", func(p *mappedSandboxProofV1) { p.Code[0].Resolved = "relative"; p.ObservedCode = slices.Clone(p.Code) }},
		{"invalid code UTF8", func(p *mappedSandboxProofV1) { p.Code[0].Path = "/bad-\xff"; p.ObservedCode = slices.Clone(p.Code) }},
		{"duplicate closure", func(p *mappedSandboxProofV1) {
			p.Code = append(p.Code, p.Code[0])
			p.ObservedCode = slices.Clone(p.Code)
		}},
		{"unsorted closure", func(p *mappedSandboxProofV1) { slices.Reverse(p.Code); p.ObservedCode = slices.Clone(p.Code) }},
		{"relative mount", func(p *mappedSandboxProofV1) { p.Mounts = []mappedSandboxMountV1{{"relative", "/data"}} }},
		{"relative mount resolution", func(p *mappedSandboxProofV1) { p.Mounts = []mappedSandboxMountV1{{"/data", "relative"}} }},
		{"direct code shadow", func(p *mappedSandboxProofV1) { p.Mounts = []mappedSandboxMountV1{{p.Code[0].Path, "/data"}} }},
		{"resolved mount shadow", func(p *mappedSandboxProofV1) { p.Mounts = []mappedSandboxMountV1{{"/data", p.Code[0].Path}} }},
		{"code symlink shadow", func(p *mappedSandboxProofV1) {
			p.Code[0].Resolved = "/private/code"
			p.ObservedCode = slices.Clone(p.Code)
			p.Mounts = []mappedSandboxMountV1{{"/private", "/private"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f.effect.sandboxChange = test.change
			if err := verifyMappedSandboxV1(f.ctx, f.prepared.state, f.effect, request); err == nil {
				t.Fatal("invalid proof accepted")
			}
		})
	}
	f.effect.sandboxChange = func(p *mappedSandboxProofV1) {
		p.Mounts = []mappedSandboxMountV1{{"/unrelated-data", "/unrelated-data"}}
	}
	if err := verifyMappedSandboxV1(f.ctx, f.prepared.state, f.effect, request); err != nil {
		t.Fatal("unrelated data mount refused", err)
	}
	f.effect.sandboxChange = nil
	for _, change := range []func(*mappedSandboxRequestV1){
		func(r *mappedSandboxRequestV1) { r.Binding.Generation = rendererDigest("9") },
		func(r *mappedSandboxRequestV1) { r.Binding.Image = rendererDigest("9") },
		func(r *mappedSandboxRequestV1) { r.Binding.Revision = "99" },
		func(r *mappedSandboxRequestV1) { r.Binding.Role = "other-role" },
		func(r *mappedSandboxRequestV1) { r.Binding.Plan = rendererDigest("9") },
		func(r *mappedSandboxRequestV1) { r.Container = "another-container" },
		func(r *mappedSandboxRequestV1) { r.PID = "999" },
		func(r *mappedSandboxRequestV1) { r.RuntimePolicy = rendererDigest("9") },
	} {
		changed := request
		change(&changed)
		original, _ := canonical.Sum("mapped-sandbox", "v1", request)
		f.effect.sandboxChange = func(p *mappedSandboxProofV1) { p.Request = original }
		if err := verifyMappedSandboxV1(f.ctx, f.prepared.state, f.effect, changed); err == nil {
			t.Fatal("proof replayed for a different owner or plan")
		}
	}
}

func TestOwnedMappedWaiterRequiresSandboxProofV1(t *testing.T) {
	for _, mode := range []string{"stale", "missing", "missing-build-authority"} {
		t.Run(mode, func(t *testing.T) {
			f := newWaiterFixtureV1(t)
			a := f.start(t)
			if mode == "missing-build-authority" {
				a.prepared.ops.ValidateProfileOwner = nil
			} else if mode == "missing" {
				a.prepared.ops.Evidence = withoutMappedSandboxV1{f.effect}
			} else {
				f.effect.sandboxChange = func(p *mappedSandboxProofV1) { p.Request = rendererDigest("9") }
			}
			if _, err := a.release(); err == nil {
				t.Fatal("unproved sandbox released")
			}
			if f.admissions != 0 || f.application.Load() != 0 || f.removed != 1 {
				t.Fatal("refusal changed admission, exposed stdin or lost owned cleanup")
			}
			if _, err := a.release(); err == nil || f.removed != 1 {
				t.Fatal("failed sandbox attempt retried release or cleanup")
			}
		})
	}
}

func TestMappedSandboxSelectedPlanRefusalsV1(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ownedMappedWaiterV1)
	}{
		{"no operation", func(a *ownedMappedWaiterV1) { a.prepared.ops.Operation = nil }},
		{"no profile authority", func(a *ownedMappedWaiterV1) { a.prepared.ops.ValidateProfileOwner = nil }},
		{"no generation", func(a *ownedMappedWaiterV1) { a.prepared.state.Current = nil }},
		{"missing lock", func(a *ownedMappedWaiterV1) { a.prepared.state.Current.BuildLockDigest = rendererDigest("9") }},
		{"changed policy", func(a *ownedMappedWaiterV1) { a.prepared.state.Current.RuntimePolicyDigest = rendererDigest("9") }},
		{"changed config", func(a *ownedMappedWaiterV1) { a.binding.Image = rendererDigest("9") }},
		{"changed generation", func(a *ownedMappedWaiterV1) { a.binding.Generation = rendererDigest("9") }},
		{"changed image", func(a *ownedMappedWaiterV1) { a.prepared.state.Current.ImageDigest = rendererDigest("9") }},
		{"changed rootfs", func(a *ownedMappedWaiterV1) { a.prepared.state.Current.RootFSSubject = rendererDigest("9") }},
		{"invalid create", func(a *ownedMappedWaiterV1) { a.prepared.spec.Args = nil }},
		{"missing separator", func(a *ownedMappedWaiterV1) {
			a.prepared.spec.Args = slices.DeleteFunc(a.prepared.spec.Args, func(s string) bool { return s == "--" })
		}},
		{"missing executable", func(a *ownedMappedWaiterV1) {
			a.prepared.spec.Args = a.prepared.spec.Args[:slices.Index(a.prepared.spec.Args, "--")+1]
		}},
		{"relative executable", func(a *ownedMappedWaiterV1) {
			a.prepared.spec.Args[slices.Index(a.prepared.spec.Args, "--")+1] = "relative"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newWaiterFixtureV1(t)
			a := f.start(t)
			test.change(a)
			if _, err := a.sandboxRequest(); err == nil {
				t.Fatal("stale or incomplete selected plan accepted")
			}
		})
	}
}
