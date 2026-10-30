package dockerdeploy

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/mappedlaunch"
	"github.com/omry/reploy/internal/providers"
)

func mappedEntryPlanFixtureV1(t *testing.T, profile string) mappedEntryInputV1 {
	t.Helper()
	c, original := runtimeCurrentBuildFixture(t)
	d := buildIdentityDocumentV1(original.Document)
	d.Environment.Runtime.IdentityPolicy.Users["root"] = blueprint.IdentityPolicyUserV1{UID: "0", PrimaryGroup: "root", SupplementaryGroups: []string{}}
	d.Environment.Runtime.IdentityPolicy.Groups["root"] = "0"
	d.Environment.Runtime.IdentityPolicy.Groups["read"] = "201"
	worker := d.Environment.Runtime.IdentityPolicy.Users["worker"]
	worker.SupplementaryGroups = []string{"read"}
	d.Environment.Runtime.IdentityPolicy.Users["worker"] = worker
	d.Environment.Runtime.IdentityPolicy.Roles["master"] = blueprint.IdentityPolicyRoleV1{User: "root", Capabilities: []string{"CAP_CHOWN"}}
	d.Environment.Applications = map[string]blueprint.Application{"application": {
		Packages: blueprint.ApplicationPackages{Python: &blueprint.PythonComponent{Interpreter: blueprint.CommandRequirement{Command: "python"}}},
		Executables: map[string]blueprint.Executable{
			"main":   {Source: "python", Binary: "python", Role: "app", ArgvPrefix: []string{"-u"}, ArgvSuffix: []string{"tail"}},
			"master": {Source: "python", Binary: "python", Role: "master"},
		},
	}}
	d.Environment.Commands = map[string]blueprint.Command{
		"serve":  {Executable: "application.main", NativeCommand: true, Trigger: []string{"serve"}, Argv: []string{"script.py"}, ForwardFlags: []string{"--verbose"}, Order: blueprint.DefaultArgumentOrder},
		"master": {Executable: "application.master", NativeCommand: true, Trigger: []string{"master"}, Order: blueprint.DefaultArgumentOrder},
	}
	d.Environment.Workload = &blueprint.Workload{Command: "serve", Endpoints: map[string]blueprint.Endpoint{}}
	if err := d.Environment.RebuildProviderContributions(); err != nil {
		t.Fatal(err)
	}
	data := providers.CanonicalProviderData{Schema: "mapped-entry-fixture-v1", Value: canonical.Object{}}
	observation := pythonConsumerObservation("python", "/usr/bin/python3")
	evidence, err := ExecutableEvidenceFromProbe(observation, ProbeExecutableBinding{Output: providers.QualifiedOutput{Component: "application/application/python", Name: "python"}, Facts: data})
	if err != nil {
		t.Fatal(err)
	}
	c.Lock.Catalog = []providers.RealizedOutput{{SupplierNode: "base", SupplierComponent: "application/application/python", Name: "python", Candidate: providers.ExecutableCandidate{InvocationPath: "/usr/bin/python3", Provenance: data}, Evidence: evidence}}
	c.State.Blueprint = testResolvedBlueprintV1(t, d)
	c.Lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, d)
	accounts, err := deploy.VerifyImageIdentityFilesV1(d.Environment.Runtime.IdentityPolicy, c.Lock.FinalImage, c.Lock.Platform, map[string][]byte{
		"etc/passwd":        []byte("root:x:0:0::/:/bin/false\nworker:x:200:200::/:/bin/false\n"),
		"etc/group":         []byte("root:x:0:\nworker:x:200:\nread:x:201:worker\n"),
		"etc/nsswitch.conf": []byte("passwd: files\ngroup: files\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	c.Lock.IdentityPolicy = &deploy.IdentityPolicyLockV1{Schema: deploy.IdentityPolicyLockSchemaV1, Policy: *d.Environment.Runtime.IdentityPolicy, Evidence: accounts}
	runtime := original.DockerPlan
	runtime.Phase = blueprint.PhaseStaged
	runtime.Workload = &WorkloadExecutionPlan{Command: "serve", Endpoints: map[string]EndpointExecutionPlan{}}
	plans, err := RuntimePlansV1(d, runtime)
	if err != nil {
		t.Fatal(err)
	}
	c.Lock.RuntimePolicy, err = CompileRuntimePolicyFromLockV1(d, c.Lock, plans)
	if err != nil {
		t.Fatal(err)
	}
	refreshCurrentBuildReuseGeneration(t, &c)
	runtime.Image = c.Generation.Reference
	c.State.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	c.State.BlueprintSource = "synthetic private E4.1.1 fixture; not image or runtime qualification"
	emptyUIDs, emptyGIDs := []deploy.IdentityMappingIntervalV1{}, []deploy.IdentityMappingIntervalV1{}
	uids := []deploy.IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "1"}, {ContainerStart: "200", HostStart: "100200", Count: "1"}}
	gids := []deploy.IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "1"}, {ContainerStart: "200", HostStart: "100200", Count: "2"}}
	if profile == "bounded" {
		uids = []deploy.IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: "100000", Count: "2048"}}
		gids = slices.Clone(uids)
	}
	geometry := deploy.IdentityMappingGeometryV1{Profile: profile, UIDs: uids, GIDs: gids}
	c.State.IdentityMapping = &deploy.IdentityMappingStateV1{
		Schema: deploy.IdentityMappingStateSchemaV2, InstallationID: "installation", OwnerDir: t.TempDir(), Revision: "1", OperatorUID: "1000", OperatorGID: "1000", Profile: profile,
		ProcessModel: deploy.IdentityMappingNativeProcessModelV1, AcceptedUIDs: &emptyUIDs, AcceptedGIDs: &emptyGIDs, RetainedStorage: []string{},
		PendingOperation: &deploy.IdentityMappingOperationV1{OperationID: "operation", Kind: "native-execute", ExpectedRevision: "1", ExpectedGeneration: c.State.Current, Candidate: geometry,
			OwnedObjects: []string{}, Phase: "effects-pending", Inhibited: true, Native: &deploy.NativeIdentityOperationV1{Active: geometry, Engine: "local-rootless-podman", Process: "fresh-instance", RuntimeUsers: []string{"root"}, RuntimeGroups: []string{"root"}}},
	}
	if err := deploy.ValidateStateV1(c.State); err != nil {
		t.Fatal(err)
	}
	return mappedEntryInputV1{Current: c, Runtime: runtime, Kind: mappedApplicationCommandV1, Command: "serve", Forwarded: []string{"--verbose"}, Environment: []string{"Z=last", "A=first"}, Stdin: true, KernelLastCapability: 40}
}

func TestMappedEntryPlanV1CompleteIdentityAndStartup(t *testing.T) {
	for _, profile := range []string{"exact", "bounded"} {
		for _, kind := range []mappedEntryKindV1{mappedWorkloadStartV1, mappedApplicationCommandV1, mappedRuntimeTestV1, mappedLifecycleHookV1, mappedHostReplacementV1} {
			t.Run(profile+"/"+string(rune(kind+'0')), func(t *testing.T) {
				input := mappedEntryPlanFixtureV1(t, profile)
				input.Kind = kind
				before, _ := json.Marshal(input)
				plan, err := constructMappedEntryPlanV1(input)
				if err != nil {
					t.Fatal(err)
				}
				facts, err := plan.facts()
				if err != nil {
					t.Fatal(err)
				}
				if facts.Mode != strconv.Itoa(int(mappedEntryFreshInstanceV1)) || facts.Kind != strconv.Itoa(int(kind)) || facts.UID != "200" || facts.GID != "200" || !slices.Equal(facts.Groups, []string{"201"}) || facts.Kernel.FinalCapabilities != "0000000000000000" {
					t.Fatalf("wrong entry authority: %+v", facts)
				}
				if !slices.Equal(facts.Argv, []string{"/usr/bin/python3", "-u", "script.py", "--verbose", "tail"}) || !slices.Equal(facts.Environment, []string{"A=first", "Z=last"}) {
					t.Fatalf("wrong argument/environment plan: %+v", facts)
				}
				if _, err := mappedStartupCommandV1(facts.Startup, mustCompileEntryKernelV1(t, facts)); err != nil {
					t.Fatal(err)
				}
				digest, err := canonical.Sum("mapped-entry", "v1", facts)
				if err != nil || digest != plan.binding.Plan || plan.binding.Generation != input.Current.Generation.BuildLockDigest || plan.binding.Image != input.Current.Lock.FinalImage.ConfigDigest || plan.binding.Operation != "operation" || plan.binding.Revision != "1" || plan.binding.Role != "app" {
					t.Fatalf("incorrect canonical binding: %+v, %v", plan.binding, err)
				}
				if facts.Active.Profile != profile || facts.RuntimePolicy != input.Current.Generation.RuntimePolicyDigest || !slices.Contains(facts.RequiredCode, "/usr/bin/python3") || !slices.Contains(facts.RequiredCode, deploy.ApplicationStartupVerifierPathV1) {
					t.Fatal("plan omitted maps, runtime or protected executable")
				}
				after, _ := json.Marshal(input)
				if string(before) != string(after) {
					t.Fatal("constructor changed its input")
				}
				if _, err := unmappedRuntimeDocumentV1(input.Current.State); err == nil {
					t.Fatal("private plan removed public mapped guard")
				}
			})
		}
	}
}

func mustCompileEntryKernelV1(t *testing.T, facts mappedEntryPayloadV1) mappedSeccompPolicyV1 {
	t.Helper()
	if err := facts.Kernel.validate(); err != nil {
		t.Fatal(err)
	}
	final, _ := strconv.ParseUint(facts.Kernel.FinalCapabilities, 16, 64)
	setup, _ := strconv.ParseUint(facts.Kernel.SetupCapabilities, 16, 64)
	last, _ := strconv.Atoi(facts.Kernel.KernelLastCapability)
	p, err := compileMappedSeccompV1(podmanDockerSeccompProfile, facts.Kernel.ABI, final, setup, last)
	if err != nil || p.facts() != facts.Kernel {
		t.Fatalf("kernel facts do not match compiler: %v", err)
	}
	return p
}

func TestMappedEntryPlanV1BindsSelectedMountPermissions(t *testing.T) {
	for _, kind := range []mappedEntryKindV1{mappedApplicationCommandV1, mappedRuntimeTestV1, mappedLifecycleHookV1, mappedWorkloadStartV1, mappedHostReplacementV1} {
		t.Run(strconv.Itoa(int(kind)), func(t *testing.T) {
			i := mappedEntryPlanFixtureV1(t, "exact")
			i.Kind = kind
			i.Runtime.Mounts = []MountExecutionPlan{{Name: "data", Mode: blueprint.MountManagedBind, Source: "/private/data", Target: "/data"}}
			rebindMappedEntryFixtureV1(t, &i, func(d *blueprint.Document) {
				d.Environment.Mounts = map[string]blueprint.EnvironmentMount{"data": {Target: "/data", Writable: true, UpdatePolicy: blueprint.UpdatePreserve}}
				c := d.Environment.Commands["serve"]
				c.Mounts = map[string]blueprint.CommandMountOverride{"data": {Writable: false}}
				d.Environment.Commands["serve"] = c
			})
			before, _ := json.Marshal(i)
			p, err := constructMappedEntryPlanV1(i)
			if err != nil {
				t.Fatal(err)
			}
			f, err := p.facts()
			if err != nil {
				t.Fatal(err)
			}
			wantReadOnly := kind != mappedWorkloadStartV1 && kind != mappedHostReplacementV1
			if len(f.Mounts) != 1 || f.Mounts[0].ReadOnly != wantReadOnly {
				t.Fatalf("selected mount permission mismatch: mounts=%+v, want readonly=%t", f.Mounts, wantReadOnly)
			}
			var locked *deploy.RuntimeMountV1
			for n := range f.RuntimePlan.Mounts {
				if f.RuntimePlan.Mounts[n].Destination == "/data" {
					locked = &f.RuntimePlan.Mounts[n]
				}
			}
			if locked == nil || locked.ReadOnly != f.Mounts[0].ReadOnly {
				t.Fatalf("delivery mounts differ from locked admission plan: %+v", f)
			}
			after, _ := json.Marshal(i)
			if string(before) != string(after) {
				t.Fatal("command override changed the base workload input")
			}
			i.Runtime.Mounts[0].Source = "/mutated/source"
			f.Mounts[0].ReadOnly = !wantReadOnly
			retained, err := p.facts()
			if err != nil || retained.Mounts[0].Source != "/private/data" || retained.Mounts[0].ReadOnly != wantReadOnly {
				t.Fatalf("retained mount payload mutated: %+v, %v", retained.Mounts, err)
			}
		})
	}
}

func TestMappedEntryPlanV1RejectsUnsupportedKindsAndStreams(t *testing.T) {
	for _, kind := range []mappedEntryKindV1{0, mappedShellV1, mappedControlledSessionV1, mappedDeployedCommandV1, mappedHostNativeCommandV1, 255} {
		input := mappedEntryPlanFixtureV1(t, "exact")
		input.Kind = kind
		plan, err := constructMappedEntryPlanV1(input)
		if err == nil || plan != (mappedEntryPlanV1{}) {
			t.Fatalf("kind %d produced a plan: %+v, %v", kind, plan, err)
		}
		if kind == mappedShellV1 || kind == mappedControlledSessionV1 || kind == mappedDeployedCommandV1 {
			if !errors.Is(err, errMappedOccupiedEntryV1) {
				t.Fatal(err)
			}
		}
	}
	for _, name := range []string{"tty", "private", "runtime-private", "observed-exit"} {
		input := mappedEntryPlanFixtureV1(t, "exact")
		switch name {
		case "tty":
			input.TTY = true
		case "private":
			input.PrivateEnvironment = true
		case "runtime-private":
			input.Runtime.PrivateEnvironment = true
		case "observed-exit":
			input.ObservedExit = true
		}
		plan, err := constructMappedEntryPlanV1(input)
		if err == nil || !strings.Contains(err.Error(), "unqualified") || plan != (mappedEntryPlanV1{}) {
			t.Fatalf("%s produced a plan: %+v, %v", name, plan, err)
		}
	}
}

func TestMappedEntryPlanV1InputAndReadbackCannotMutatePlan(t *testing.T) {
	input := mappedEntryPlanFixtureV1(t, "exact")
	plan, err := constructMappedEntryPlanV1(input)
	if err != nil {
		t.Fatal(err)
	}
	before, err := plan.facts()
	if err != nil {
		t.Fatal(err)
	}
	input.Environment[0] = "EVIL=changed"
	input.Forwarded[0] = "changed"
	input.Current.Lock.IdentityPolicy.Policy.Groups["worker"] = "999"
	input.Current.State.IdentityMapping.PendingOperation.Native.Active.UIDs[0].HostStart = "999"
	changed, _ := plan.facts()
	changed.Argv[0] = "/bin/false"
	changed.Startup[0] = "raw-exec"
	changed.Groups[0] = "0"
	changed.Active.UIDs[0].HostStart = "0"
	changed.RequiredCode[0] = "/tmp/mutable"
	after, err := plan.facts()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("plan mutated: %v", err)
	}
	if _, err := (mappedEntryPlanV1{}).facts(); err == nil {
		t.Fatal("zero plan accepted")
	}
	bad := plan
	bad.binding.Plan = rendererDigest("a")
	if _, err := bad.facts(); err == nil {
		t.Fatal("substituted digest accepted")
	}
	if plan.binding == (mappedlaunch.Binding{}) {
		t.Fatal("binding missing")
	}
}

func TestMappedEntryPlanV1RejectsStaleOrIncompleteAuthority(t *testing.T) {
	tests := []struct {
		name   string
		change func(*mappedEntryInputV1)
	}{
		{"no generation", func(i *mappedEntryInputV1) { i.Current.State.Current = nil }},
		{"terminal removal", func(i *mappedEntryInputV1) { i.Current.State.TerminalRemoval = true }},
		{"wrong runtime phase", func(i *mappedEntryInputV1) { i.Runtime.Phase = blueprint.PhaseInstalled }},
		{"unexpected scope", func(i *mappedEntryInputV1) { scope := blueprint.InstallScopeUser; i.Runtime.Scope = &scope }},
		{"different generation", func(i *mappedEntryInputV1) { i.Current.Generation.Reference += "-other" }},
		{"different platform", func(i *mappedEntryInputV1) { i.Current.State.Platform.Architecture = "arm64" }},
		{"wrong build digest", func(i *mappedEntryInputV1) { i.Current.Generation.BuildLockDigest = rendererDigest("a") }},
		{"wrong image", func(i *mappedEntryInputV1) { i.Current.Lock.FinalImage.ConfigDigest = rendererDigest("b") }},
		{"changed runtime policy", func(i *mappedEntryInputV1) {
			i.Current.Lock.RuntimePolicy.Network.Public = blueprint.NetworkAccessAllow
		}},
		{"changed image evidence", func(i *mappedEntryInputV1) {
			i.Current.Lock.IdentityPolicy.Evidence.Image.ConfigDigest = rendererDigest("a")
		}},
		{"changed role", func(i *mappedEntryInputV1) {
			i.Current.Lock.IdentityPolicy.Policy.Roles["app"] = blueprint.IdentityPolicyRoleV1{User: "root", Capabilities: []string{}}
		}},
		{"changed catalog path", func(i *mappedEntryInputV1) { i.Current.Lock.Catalog[0].Candidate.InvocationPath = "/tmp/substitute" }},
		{"no identity state", func(i *mappedEntryInputV1) { i.Current.State.IdentityMapping = nil }},
		{"revoked", func(i *mappedEntryInputV1) { i.Current.State.IdentityMapping.Revoked = true }},
		{"no operation", func(i *mappedEntryInputV1) { i.Current.State.IdentityMapping.PendingOperation = nil }},
		{"no native binding", func(i *mappedEntryInputV1) { i.Current.State.IdentityMapping.PendingOperation.Native = nil }},
		{"not inhibited", func(i *mappedEntryInputV1) { i.Current.State.IdentityMapping.PendingOperation.Inhibited = false }},
		{"wrong revision", func(i *mappedEntryInputV1) { i.Current.State.IdentityMapping.PendingOperation.ExpectedRevision = "2" }},
		{"old pending generation", func(i *mappedEntryInputV1) {
			copy := *i.Current.State.Current
			copy.BuildLockDigest = rendererDigest("a")
			i.Current.State.IdentityMapping.PendingOperation.ExpectedGeneration = &copy
		}},
		{"unknown runtime account", func(i *mappedEntryInputV1) {
			i.Current.State.IdentityMapping.PendingOperation.Native.RuntimeUsers = []string{"undeclared"}
		}},
		{"unknown runtime group", func(i *mappedEntryInputV1) {
			i.Current.State.IdentityMapping.PendingOperation.Native.RuntimeGroups = []string{"undeclared"}
		}},
		{"changed active map", func(i *mappedEntryInputV1) {
			i.Current.State.IdentityMapping.PendingOperation.Native.Active.UIDs = slices.Clone(i.Current.State.IdentityMapping.PendingOperation.Native.Active.UIDs)
			i.Current.State.IdentityMapping.PendingOperation.Native.Active.UIDs[0].HostStart = "100001"
		}},
		{"unknown command", func(i *mappedEntryInputV1) { i.Command = "missing" }},
		{"workload substitution", func(i *mappedEntryInputV1) { i.Kind = mappedWorkloadStartV1; i.Command = "master" }},
		{"runtime image reference", func(i *mappedEntryInputV1) { i.Runtime.Image += "-other" }},
		{"writable root", func(i *mappedEntryInputV1) { i.Runtime.Sandbox.ReadOnlyRoot = false }},
		{"weakened network", func(i *mappedEntryInputV1) { i.Runtime.Sandbox.Network.Public = blueprint.NetworkAccessAllow }},
		{"unqualified kernel", func(i *mappedEntryInputV1) { i.KernelLastCapability = 99 }},
		{"unknown flag", func(i *mappedEntryInputV1) { i.Forwarded = []string{"--not-approved"} }},
		{"NUL argument", func(i *mappedEntryInputV1) { i.Forwarded = []string{"bad\x00argument"} }},
		{"invalid UTF8 argument", func(i *mappedEntryInputV1) { i.Forwarded = []string{string([]byte{255})} }},
		{"NUL environment", func(i *mappedEntryInputV1) { i.Environment = []string{"A=value\x00"} }},
		{"invalid UTF8 environment", func(i *mappedEntryInputV1) { i.Environment = []string{"A=" + string([]byte{255})} }},
		{"duplicate environment", func(i *mappedEntryInputV1) { i.Environment = []string{"A=one", "A=two"} }},
		{"bare environment", func(i *mappedEntryInputV1) { i.Environment = []string{"A"} }},
		{"empty environment key", func(i *mappedEntryInputV1) { i.Environment = []string{"=value"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := mappedEntryPlanFixtureV1(t, "exact")
			tt.change(&i)
			p, err := constructMappedEntryPlanV1(i)
			if err == nil || p != (mappedEntryPlanV1{}) {
				t.Fatalf("invalid state produced plan: %+v, %v", p, err)
			}
		})
	}
}

// Rebind only synthetic fixture authority. This does not provide real image
// provenance, F observations, or any runtime/public qualification.
func rebindMappedEntryFixtureV1(t *testing.T, i *mappedEntryInputV1, change func(*blueprint.Document)) {
	t.Helper()
	d, err := blueprint.DecodeResolvedDocumentV1(i.Current.State.Blueprint)
	if err != nil {
		t.Fatal(err)
	}
	change(&d)
	i.Current.State.Blueprint = testResolvedBlueprintV1(t, d)
	i.Current.Lock.BlueprintDigest = testResolvedBlueprintDigestV1(t, d)
	accounts, err := deploy.VerifyImageIdentityFilesV1(d.Environment.Runtime.IdentityPolicy, i.Current.Lock.FinalImage, i.Current.Lock.Platform, map[string][]byte{
		"etc/passwd":        []byte("root:x:0:0::/:/bin/false\nworker:x:200:200::/:/bin/false\n"),
		"etc/group":         []byte("root:x:0:\nworker:x:200:\nread:x:201:worker\n"),
		"etc/nsswitch.conf": []byte("passwd: files\ngroup: files\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	i.Current.Lock.IdentityPolicy = &deploy.IdentityPolicyLockV1{Schema: deploy.IdentityPolicyLockSchemaV1, Policy: *d.Environment.Runtime.IdentityPolicy, Evidence: accounts}
	plans, err := RuntimePlansV1(d, i.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	i.Current.Lock.RuntimePolicy, err = CompileRuntimePolicyFromLockV1(d, i.Current.Lock, plans)
	if err != nil {
		t.Fatal(err)
	}
	refreshCurrentBuildReuseGeneration(t, &i.Current)
	i.Current.State.IdentityMapping.PendingOperation.ExpectedGeneration = i.Current.State.Current
}

func TestMappedEntryPlanV1CapabilityTupleComesFromLockedRole(t *testing.T) {
	for _, privileged := range []bool{false, true} {
		i := mappedEntryPlanFixtureV1(t, "exact")
		i.Command = "master"
		i.Forwarded = nil
		if !privileged {
			rebindMappedEntryFixtureV1(t, &i, func(d *blueprint.Document) {
				r := d.Environment.Runtime.IdentityPolicy.Roles["master"]
				r.Capabilities = []string{}
				d.Environment.Runtime.IdentityPolicy.Roles["master"] = r
			})
		}
		p, err := constructMappedEntryPlanV1(i)
		if err != nil {
			t.Fatal(err)
		}
		f, err := p.facts()
		if err != nil {
			t.Fatal(err)
		}
		want := "0000000000000000"
		if privileged {
			want = "0000000000000001"
		}
		if f.UID != "0" || f.GID != "0" || len(f.Groups) != 0 || f.Kernel.FinalCapabilities != want || p.binding.Role != "master" {
			t.Fatalf("wrong root tuple: %+v", f)
		}
		kernel := mustCompileEntryKernelV1(t, f)
		if kernel.setupCapabilities != kernel.finalCapabilities|1<<6|1<<8|1<<12 {
			t.Fatalf("wrong exact setup union: %x", kernel.setupCapabilities)
		}
		if _, err := mappedStartupCommandV1(f.Startup, kernel); err != nil {
			t.Fatal(err)
		}
	}
	i := mappedEntryPlanFixtureV1(t, "exact")
	rebindMappedEntryFixtureV1(t, &i, func(d *blueprint.Document) {
		a := d.Environment.Applications["application"]
		e := a.Executables["main"]
		e.Role = "helper"
		a.Executables["main"] = e
		d.Environment.Applications["application"] = a
	})
	p, err := constructMappedEntryPlanV1(i)
	if err == nil || !strings.Contains(err.Error(), "nonzero UID") || p != (mappedEntryPlanV1{}) {
		t.Fatalf("unqualified nonzero UID + caps: %+v, %v", p, err)
	}
}

func TestMappedEntryPlanV1CanonicalDigestBindsEveryLaunchFact(t *testing.T) {
	base := mappedEntryPlanFixtureV1(t, "exact")
	p, err := constructMappedEntryPlanV1(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		change func(*mappedEntryInputV1)
	}{
		{"environment", func(i *mappedEntryInputV1) { i.Environment = []string{"A=changed"} }},
		{"arguments", func(i *mappedEntryInputV1) { i.Forwarded = []string{"--", "$(literal);not-a-shell"} }},
		{"stdin", func(i *mappedEntryInputV1) { i.Stdin = false }},
		{"entry-kind", func(i *mappedEntryInputV1) { i.Kind = mappedRuntimeTestV1 }},
		{"installation", func(i *mappedEntryInputV1) { i.Current.State.IdentityMapping.InstallationID = "different-installation" }},
		{"operation", func(i *mappedEntryInputV1) {
			i.Current.State.IdentityMapping.PendingOperation.OperationID = "different-operation"
		}},
		{"run", func(i *mappedEntryInputV1) {
			i.Current.State.IdentityMapping.PendingOperation.Native.Process = "different-instance"
		}},
		{"engine", func(i *mappedEntryInputV1) {
			i.Current.State.IdentityMapping.PendingOperation.Native.Engine = "different-engine"
		}},
		{"revision", func(i *mappedEntryInputV1) {
			i.Current.State.IdentityMapping.Revision = "2"
			i.Current.State.IdentityMapping.PendingOperation.ExpectedRevision = "2"
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			i := mappedEntryPlanFixtureV1(t, "exact")
			tt.change(&i)
			q, err := constructMappedEntryPlanV1(i)
			if err != nil {
				t.Fatal(err)
			}
			if q.binding.Plan == p.binding.Plan {
				t.Fatal("launch fact absent from plan digest")
			}
		})
	}
	// Source-owner directories are not authorization payload facts. Use one
	// input to check exact canonical stability independently of fixture setup.
	base.Environment = []string{"A=first", "Z=last"}
	q, err := constructMappedEntryPlanV1(base)
	if err != nil || q != p {
		t.Fatalf("environment order changed identity: %v", err)
	}
	base.Environment = nil
	a, err := constructMappedEntryPlanV1(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Environment = []string{}
	b, err := constructMappedEntryPlanV1(base)
	if err != nil || a != b {
		t.Fatalf("empty environment representations differ: %v", err)
	}
}

func TestMappedEntryPlanV1InstalledUserSnapshot(t *testing.T) {
	i := mappedEntryPlanFixtureV1(t, "exact")
	scope := blueprint.InstallScopeUser
	i.Runtime.Phase, i.Runtime.Scope = blueprint.PhaseInstalled, &scope
	i.Runtime.ContainerName, i.Runtime.NetworkName = "demo", "demo"
	i.Current.State.Staging = nil
	i.Current.State.Deployment = &deploy.DeploymentStateV1{Schema: deploy.DeploymentStateSchemaV1, Installation: deploy.InstallationStateV1{
		Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusReady, Scope: string(scope),
		TargetDir: i.Current.State.IdentityMapping.OwnerDir, Service: "demo", InstanceID: "fixture-installation",
		ComposeProject: "demo", ContainerName: "demo", NetworkName: "demo", Ports: []deploy.InstallationPortBindingV1{},
	}}
	instance := "fixture-installation"
	i.Current.State.IdentityMapping.InstallationBinding = &instance
	p, err := constructMappedEntryPlanV1(i)
	if err != nil {
		t.Fatal(err)
	}
	f, err := p.facts()
	if err != nil || f.Installation != "installation" || f.Operation != "operation" {
		t.Fatalf("wrong installed plan: %+v, %v", f, err)
	}
	wrong := blueprint.InstallScopeSystem
	i.Runtime.Scope = &wrong
	if p, err := constructMappedEntryPlanV1(i); err == nil || p != (mappedEntryPlanV1{}) {
		t.Fatal("mismatched installed scope accepted")
	}
}

func TestMappedEntryPlanV1PreservesReservedAndAcceptedHistory(t *testing.T) {
	i := mappedEntryPlanFixtureV1(t, "exact")
	m := i.Current.State.IdentityMapping
	n := m.PendingOperation.Native
	oldProcess, engine := "old-instance", n.Engine
	m.NativeProcess, m.Engine, m.AdmittedGeneration = &oldProcess, &engine, i.Current.State.Current
	m.UIDAssignments = append(slices.Clone(n.Active.UIDs), deploy.IdentityMappingIntervalV1{ContainerStart: "500", HostStart: "100500", Count: "1"})
	m.GIDAssignments = append(slices.Clone(n.Active.GIDs), deploy.IdentityMappingIntervalV1{ContainerStart: "500", HostStart: "100500", Count: "1"})
	acceptedUIDs, acceptedGIDs := slices.Clone(m.UIDAssignments), slices.Clone(m.GIDAssignments)
	m.AcceptedUIDs, m.AcceptedGIDs = &acceptedUIDs, &acceptedGIDs
	m.DormantUIDs = []deploy.IdentityMappingIntervalV1{{ContainerStart: "500", HostStart: "100500", Count: "1"}}
	m.DormantGIDs = slices.Clone(m.DormantUIDs)
	m.PendingOperation.PriorAccepted = m.AdmittedGeneration
	before, _ := json.Marshal(i.Current.State)
	p, err := constructMappedEntryPlanV1(i)
	if err != nil {
		t.Fatal(err)
	}
	f, err := p.facts()
	if err != nil || !reflect.DeepEqual(f.Active, n.Active) {
		t.Fatalf("wrong active geometry: %+v, %v", f.Active, err)
	}
	after, _ := json.Marshal(i.Current.State)
	if string(before) != string(after) {
		t.Fatal("plan consumed or changed reservation/admission history")
	}
	// A merely reserved, never-admitted row cannot authorize surplus current C.
	extra := deploy.IdentityMappingIntervalV1{ContainerStart: "600", HostStart: "100600", Count: "1"}
	m.UIDAssignments = append(m.UIDAssignments, extra)
	m.GIDAssignments = append(m.GIDAssignments, extra)
	m.PendingOperation.Candidate.UIDs = append(slices.Clone(m.PendingOperation.Candidate.UIDs), extra)
	m.PendingOperation.Candidate.GIDs = append(slices.Clone(m.PendingOperation.Candidate.GIDs), extra)
	if p, err := constructMappedEntryPlanV1(i); err == nil || p != (mappedEntryPlanV1{}) {
		t.Fatal("never-admitted reservation enlarged execution authority")
	}
}
