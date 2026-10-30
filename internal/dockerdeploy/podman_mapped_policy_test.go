package dockerdeploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func mappedCreatePolicyFixtureV1(t *testing.T) mappedSeccompPolicyV1 {
	t.Helper()
	policy, err := compileMappedSeccompV1(podmanDockerSeccompProfile, "amd64", 0, 0, 40)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func mappedApplicationSpecFixtureV1(t *testing.T, final uint64) CommandSpec {
	t.Helper()
	spec := sameIDApplicationSpecWithIdentity(t, true, 0, 0)
	spec.Args = slices.DeleteFunc(spec.Args, func(s string) bool { return s == "--tty" })
	entry := slices.Index(spec.Args, "--entrypoint")
	spec.Args = slices.Insert(spec.Args, entry+4, fmt.Sprintf("--mapped-capabilities=%016x", final))
	return spec
}

func mappedPolicyForSpecFixtureV1(t *testing.T, spec CommandSpec, abi string, final uint64) mappedSeccompPolicyV1 {
	t.Helper()
	body, _, err := podmanApplicationCreateRequest(spec, "fixture-namespace")
	if err != nil {
		t.Fatal(err)
	}
	setup := final
	for _, name := range body.HostConfig.CapAdd {
		bit := slices.Index(mappedCapabilityNamesV1, name)
		if bit < 0 {
			t.Fatal("unknown fixture capability", name)
		}
		setup |= uint64(1) << bit
	}
	policy, err := compileMappedSeccompV1(podmanDockerSeccompProfile, abi, final, setup, 40)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestMappedCreatePolicyOverlayPreservesOrdinaryRequestV1(t *testing.T) {
	spec := mappedApplicationSpecFixtureV1(t, 1<<18)
	args := slices.Clone(spec.Args)
	ordinary, _, err := podmanApplicationCreateRequest(spec, "fixture-namespace")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(ordinary)
	policy := mappedPolicyForSpecFixtureV1(t, spec, "amd64", 1<<18)
	request, err := mappedPodmanApplicationCreateRequestV1(spec, "fixture-namespace", policy)
	if err != nil {
		t.Fatal(err)
	}
	body := request.body
	if body.HostConfig.AutoRemove || body.HostConfig.RestartPolicy == nil || *body.HostConfig.RestartPolicy != (mappedRestartPolicyV1{Name: "no"}) ||
		!body.HostConfig.ReadonlyRootfs || body.User != "0:0" || !slices.Equal(body.HostConfig.CapDrop, []string{"ALL"}) ||
		!slices.Contains(body.HostConfig.CapAdd, "SYS_CHROOT") || !slices.Contains(body.HostConfig.CapAdd, "SETPCAP") ||
		!slices.Contains(body.HostConfig.SecurityOpt, "seccomp="+policy.profile) || !slices.Contains(body.HostConfig.SecurityOpt, "no-new-privileges=true") {
		t.Fatal("mapped overlay lost exact authority or fixed lifetime policy")
	}
	if body.Image != ordinary.Image || body.HostConfig.UsernsMode != ordinary.HostConfig.UsernsMode || !slices.Equal(body.Cmd, ordinary.Cmd) ||
		!slices.Equal(body.Env, ordinary.Env) || body.Tty != ordinary.Tty || body.OpenStdin != ordinary.OpenStdin || request.name == "" {
		t.Fatal("mapped overlay changed unrelated fields")
	}
	again, _, err := podmanApplicationCreateRequest(spec, "fixture-namespace")
	after, _ := json.Marshal(again)
	if err != nil || string(before) != string(after) || !ordinary.HostConfig.AutoRemove || ordinary.HostConfig.RestartPolicy != nil ||
		strings.Contains(string(after), "RestartPolicy") || !slices.Equal(spec.Args, args) {
		t.Fatal("ordinary request or source changed")
	}
}

func TestMappedCreateRejectsInvalidCompilerResultV1(t *testing.T) {
	spec := mappedApplicationSpecFixtureV1(t, 0)
	good := mappedPolicyForSpecFixtureV1(t, spec, "amd64", 0)
	for _, test := range []struct {
		name   string
		change func(*mappedSeccompPolicyV1)
	}{
		{"missing", func(p *mappedSeccompPolicyV1) { *p = mappedSeccompPolicyV1{} }},
		{"profile", func(p *mappedSeccompPolicyV1) { p.profile += " " }},
		{"identity", func(p *mappedSeccompPolicyV1) { p.identity = string(rendererDigest("a")) }},
		{"ABI", func(p *mappedSeccompPolicyV1) { p.abi = "arm64" }},
		{"final", func(p *mappedSeccompPolicyV1) { p.finalCapabilities = 1 }},
		{"setup", func(p *mappedSeccompPolicyV1) { p.setupCapabilities |= 1 << 18 }},
		{"kernel", func(p *mappedSeccompPolicyV1) { p.kernelLastCapability-- }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := good
			test.change(&bad)
			if _, err := mappedPodmanApplicationCreateRequestV1(spec, "fixture", bad); err == nil {
				t.Fatal("invalid compiler result accepted")
			}
		})
	}
	for _, setup := range []uint64{good.setupCapabilities | 1<<18, 0} {
		bad, err := compileMappedSeccompV1(podmanDockerSeccompProfile, good.abi, 0, setup, 40)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mappedPodmanApplicationCreateRequestV1(spec, "fixture", bad); err == nil {
			t.Fatal("inexact required setup union accepted")
		}
	}
}

func TestMappedStartupUsesActualProbeParserV1(t *testing.T) {
	spec := sameIDApplicationSpec(t)
	ordinary, _, err := podmanApplicationCreateRequest(spec, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy := mappedPolicyForSpecFixtureV1(t, spec, "amd64", 0)
	if _, err := mappedStartupCommandV1(ordinary.Cmd, policy); err == nil {
		t.Fatal("missing independent startup ceiling was inferred from policy")
	}
	command := append([]string{"sandbox-exec", "--mapped-capabilities=0000000000000000"}, ordinary.Cmd[1:]...)
	if again, err := mappedStartupCommandV1(command, policy); err != nil || !slices.Equal(again, command) {
		t.Fatal("exact explicit argument not accepted", err)
	}
	for _, kind := range []string{"wrong final", "duplicate", "displaced", "uppercase", "private", "wrong command", "nonzero UID caps"} {
		t.Run(kind, func(t *testing.T) {
			bad := slices.Clone(command)
			selected := policy
			switch kind {
			case "wrong final":
				bad[1] = "--mapped-capabilities=0000000000000001"
			case "duplicate":
				bad = append(bad[:2:2], append([]string{bad[1]}, bad[2:]...)...)
			case "displaced":
				bad = append(bad[:3:3], append([]string{bad[1]}, bad[3:]...)...)
			case "uppercase":
				bad[1] = "--mapped-capabilities=000000000000000A"
			case "private":
				bad = append(bad[:2:2], append([]string{"--private-environment"}, bad[2:]...)...)
			case "wrong command":
				bad[0] = "restricted-exec"
			case "nonzero UID caps":
				selected.finalCapabilities = 1
				bad[1] = "--mapped-capabilities=0000000000000001"
			}
			if _, err := mappedStartupCommandV1(bad, selected); err == nil {
				t.Fatal("unqualified startup accepted")
			}
		})
	}
}

func TestMappedCreateRejectsUnqualifiedStartupBeforeCreateV1(t *testing.T) {
	for _, kind := range []string{"missing", "mismatch", "duplicate", "displaced", "private environment", "TTY", "unsupported mode", "nonzero UID caps"} {
		t.Run(kind, func(t *testing.T) {
			const final uint64 = 1 << 18
			spec := mappedApplicationSpecFixtureV1(t, final)
			entry := slices.Index(spec.Args, "--entrypoint")
			switch kind {
			case "missing":
				spec.Args = slices.DeleteFunc(spec.Args, func(s string) bool { return s == "--mapped-capabilities=0000000000040000" })
			case "mismatch":
				index := slices.Index(spec.Args, "--mapped-capabilities=0000000000040000")
				spec.Args[index] = "--mapped-capabilities=0000000000000000"
			case "duplicate":
				spec.Args = slices.Insert(spec.Args, entry+5, "--mapped-capabilities=0000000000040000")
			case "displaced":
				index := slices.Index(spec.Args, "--mapped-capabilities=0000000000040000")
				spec.Args = slices.Delete(spec.Args, index, index+1)
				spec.Args = slices.Insert(spec.Args, index+2, "--mapped-capabilities=0000000000040000")
			case "private environment":
				spec.Args = slices.Insert(spec.Args, entry+5, "--private-environment")
			case "TTY":
				spec.Args = slices.Insert(spec.Args, entry, "--tty")
			case "unsupported mode":
				spec.Args[entry+3] = "restricted-exec"
			case "nonzero UID caps":
				index := slices.Index(spec.Args, "--uid")
				spec.Args[index+1] = "1000"
			}
			policy := mappedPolicyForSpecFixtureV1(t, spec, "amd64", final)
			if _, err := mappedPodmanApplicationCreateRequestV1(spec, "fixture", policy); err == nil {
				t.Fatal("unqualified startup reached the mapped create representation")
			}
		})
	}
}

func TestMappedKernelFactsAreExactCompilerIdentityV1(t *testing.T) {
	for _, abi := range []string{"amd64", "arm64"} {
		p, err := compileMappedSeccompV1(podmanDockerSeccompProfile, abi, 1<<18, 1<<18|1<<8, 40)
		if err != nil || p.facts().validate() != nil {
			t.Fatal("valid exact tuple refused", err)
		}
		for _, field := range []string{"identity", "ABI", "final", "setup", "last", "decimal", "short hex"} {
			t.Run(fmt.Sprint(abi, field), func(t *testing.T) {
				f := p.facts()
				switch field {
				case "identity":
					f.Identity = rendererDigest("a")
				case "ABI":
					f.ABI = "386"
				case "final":
					f.FinalCapabilities = "g000000000000000"
				case "setup":
					f.SetupCapabilities = "8000000000000000"
				case "last":
					f.KernelLastCapability = "63"
				case "decimal":
					f.KernelLastCapability = "040"
				case "short hex":
					f.FinalCapabilities = "40000"
				}
				if f.validate() == nil {
					t.Fatal("invalid exact tuple accepted")
				}
			})
		}
	}
}

func TestMappedPolicyActualCreateTransportV1(t *testing.T) {
	spec := mappedApplicationSpecFixtureV1(t, 1<<18)
	policy := mappedPolicyForSpecFixtureV1(t, spec, "amd64", 1<<18)
	request, err := mappedPodmanApplicationCreateRequestV1(spec, "exact-native-maps", policy)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("b", 64)
	var submitted map[string]json.RawMessage
	var labels map[string]string
	posts, inspections := 0, 0
	client := newPodmanApplicationClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			if r.URL.Path != "/v1.44/containers/create" || r.URL.Query().Get("name") != request.name {
				t.Error("wrong mapped create route")
			}
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Error(err)
			}
			if err := json.Unmarshal(submitted["Labels"], &labels); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"Id":%q}`, id)
			return
		}
		inspections++
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": id, "Name": "/" + request.name, "Image": request.body.Image, "Config": map[string]any{"Labels": labels}})
	})
	got, err := client.createApplication(t.Context(), spec, request.body, request.name, request.body.Image, func(CommandSpec, RunOptions) error { t.Error("successful create removed"); return nil })
	if err != nil || got != id || posts != 1 || inspections != 1 {
		t.Fatal("mapped transport", got, err, posts, inspections)
	}
	var host map[string]json.RawMessage
	if err := json.Unmarshal(submitted["HostConfig"], &host); err != nil {
		t.Fatal(err)
	}
	if string(host["AutoRemove"]) != "false" || string(host["RestartPolicy"]) != `{"Name":"no","MaximumRetryCount":0}` || string(host["UsernsMode"]) != `"exact-native-maps"` {
		t.Fatal("fixed mapped lifetime/map policy not transmitted", host)
	}
	var adds, drops, security []string
	if json.Unmarshal(host["CapAdd"], &adds) != nil || json.Unmarshal(host["CapDrop"], &drops) != nil || json.Unmarshal(host["SecurityOpt"], &security) != nil {
		t.Fatal("invalid mapped transport fields")
	}
	if !slices.Equal(adds, request.body.HostConfig.CapAdd) || !slices.Contains(adds, "SYS_CHROOT") || !slices.Equal(drops, []string{"ALL"}) || !slices.Equal(security, request.body.HostConfig.SecurityOpt) || !slices.Contains(security, "seccomp="+policy.profile) {
		t.Fatal("mapped policy differs on wire")
	}
}

func TestMappedWaiterRejectsUnboundPolicyBeforeCreateV1(t *testing.T) {
	for _, kind := range []string{"missing", "corrupt profile", "wrong platform", "wrong ABI", "extra setup", "missing startup argument", "wrong final argument", "TTY", "private environment"} {
		t.Run(kind, func(t *testing.T) {
			f := newWaiterFixtureV1(t)
			p := f.prepared
			switch kind {
			case "missing":
				p.policy = mappedSeccompPolicyV1{}
			case "corrupt profile":
				p.policy.profile += " "
			case "wrong platform":
				p.state.Platform.OS = "darwin"
			case "wrong ABI":
				var err error
				p.policy, err = compileMappedSeccompV1(podmanDockerSeccompProfile, "arm64", 0, p.policy.setupCapabilities, 40)
				if err != nil {
					t.Fatal(err)
				}
			case "extra setup":
				var err error
				p.policy, err = compileMappedSeccompV1(podmanDockerSeccompProfile, "amd64", 0, p.policy.setupCapabilities|1<<18, 40)
				if err != nil {
					t.Fatal(err)
				}
			case "missing startup argument":
				index := slices.Index(p.spec.Args, "--mapped-capabilities=0000000000000000")
				p.spec.Args = slices.Delete(p.spec.Args, index, index+1)
			case "wrong final argument", "private environment":
				entry := slices.Index(p.spec.Args, "--entrypoint") + 4
				arg := "--mapped-capabilities=0000000000000001"
				if kind == "private environment" {
					arg = "--private-environment"
				}
				if kind == "wrong final argument" {
					p.spec.Args[entry] = arg
				} else {
					p.spec.Args = slices.Insert(p.spec.Args, entry+1, arg)
				}
			case "TTY":
				entry := slices.Index(p.spec.Args, "--entrypoint")
				p.spec.Args = slices.Insert(p.spec.Args, entry, "--tty")
			}
			if a, err := startMappedWaiterWithBackendV1(f.ctx, p, f.binding, f.options, f.backend); err == nil || a != nil {
				t.Fatal("unbound policy started", err)
			}
			if f.created != 0 || f.admissions != 0 || f.application.Load() != 0 {
				t.Fatal("invalid policy reached effects")
			}
		})
	}
}

func TestMappedRuntimePolicyRefusalBeforeFrameV1(t *testing.T) {
	changes := []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing automatic removal", func(h map[string]any) { delete(h, "AutoRemove") }},
		{"null automatic removal", func(h map[string]any) { h["AutoRemove"] = nil }},
		{"automatic removal", func(h map[string]any) { h["AutoRemove"] = true }},
		{"missing restart policy", func(h map[string]any) { delete(h, "RestartPolicy") }},
		{"empty restart name", func(h map[string]any) { h["RestartPolicy"] = map[string]any{"Name": "", "MaximumRetryCount": 0} }},
		{"missing restart retries", func(h map[string]any) { h["RestartPolicy"] = map[string]any{"Name": "no"} }},
		{"restart retries", func(h map[string]any) { h["RestartPolicy"] = map[string]any{"Name": "no", "MaximumRetryCount": 1} }},
		{"restart always", func(h map[string]any) { h["RestartPolicy"] = map[string]any{"Name": "always", "MaximumRetryCount": 0} }},
		{"missing security", func(h map[string]any) { delete(h, "SecurityOpt") }},
		{"unconfined", func(h map[string]any) { h["SecurityOpt"] = []string{"seccomp=unconfined", "no-new-privileges=true"} }},
		{"ordinary seed", func(h map[string]any) {
			h["SecurityOpt"] = []string{"seccomp=" + string(podmanDockerSeccompProfile), "no-new-privileges=true"}
		}},
		{"missing NNP", func(h map[string]any) { h["SecurityOpt"] = []string{"seccomp=missing"} }},
	}
	for _, phase := range []string{"startup", "release"} {
		for _, change := range changes {
			t.Run(phase+"/"+change.name, func(t *testing.T) {
				f := newWaiterFixtureV1(t)
				mutate := func(o map[string]any) { change.change(o["HostConfig"].(map[string]any)) }
				if phase == "startup" {
					f.objectChange = mutate
					a, err := startMappedWaiterWithBackendV1(f.ctx, f.prepared, f.binding, f.options, f.backend)
					if err == nil || a != nil {
						t.Fatal("bad startup observation accepted")
					}
				} else {
					a := f.start(t)
					f.mu.Lock()
					f.objectChange = mutate
					f.mu.Unlock()
					if _, err := a.release(); err == nil {
						t.Fatal("changed observation released")
					}
				}
				if f.created != 1 || f.removed != 1 || f.admissions != 0 || f.application.Load() != 0 {
					t.Fatal("refusal lost exact cleanup or admitted", f.created, f.removed, f.admissions)
				}
				select {
				case <-f.frame:
					t.Fatal("refusal sent release frame")
				default:
				}
			})
		}
	}
}

func TestMappedSandboxRequiresFreshExactKernelPolicyV1(t *testing.T) {
	for _, kind := range []string{"missing policy", "wrong policy", "missing tuple", "wrong ABI", "wrong final", "wrong setup", "wrong kernel", "stale instance"} {
		t.Run(kind, func(t *testing.T) {
			f := newWaiterFixtureV1(t)
			a := f.start(t)
			f.effect.sandboxChange = func(proof *mappedSandboxProofV1) {
				switch kind {
				case "missing policy":
					proof.KernelPolicy = ""
				case "wrong policy":
					proof.KernelPolicy = rendererDigest("a")
				case "missing tuple":
					proof.KernelFacts = mappedKernelFactsV1{}
				case "wrong ABI":
					proof.KernelFacts.ABI = "arm64"
				case "wrong final":
					proof.KernelFacts.FinalCapabilities = "0000000000000001"
				case "wrong setup":
					proof.KernelFacts.SetupCapabilities = "0000000000000000"
				case "wrong kernel":
					proof.KernelFacts.KernelLastCapability = "39"
				case "stale instance":
					proof.Request = rendererDigest("a")
				}
			}
			if _, err := a.release(); err == nil {
				t.Fatal("wrong effective-policy proof released")
			}
			if f.removed != 1 || f.admissions != 0 || f.application.Load() != 0 {
				t.Fatal("bad proof lost ownership/refusal")
			}
			select {
			case <-f.frame:
				t.Fatal("bad proof released frame")
			default:
			}
		})
	}
}

func TestMappedNativeCapabilitiesRefuseBeforeFrameV1(t *testing.T) {
	for _, field := range []string{"BoundingCaps", "EffectiveCaps"} {
		for _, phase := range []string{"startup", "release"} {
			t.Run(field+"/"+phase, func(t *testing.T) {
				f := newWaiterFixtureV1(t)
				mutate := func(object map[string]any) { object[field] = []string{"CAP_SYS_ADMIN"} }
				if phase == "startup" {
					f.objectChange = mutate
					if a, err := startMappedWaiterWithBackendV1(f.ctx, f.prepared, f.binding, f.options, f.backend); err == nil || a != nil {
						t.Fatal("mismatched native capabilities started")
					}
				} else {
					a := f.start(t)
					f.mu.Lock()
					f.objectChange = mutate
					f.mu.Unlock()
					if _, err := a.release(); err == nil {
						t.Fatal("mismatched native capabilities released")
					}
				}
				if f.removed != 1 || f.admissions != 0 || f.application.Load() != 0 {
					t.Fatal("capability refusal lost exact cleanup or admitted")
				}
				select {
				case <-f.frame:
					t.Fatal("capability refusal sent release frame")
				default:
				}
			})
		}
	}
}

func TestMappedNativeCapabilitySetsV1(t *testing.T) {
	if mappedObservedCapabilitiesV1(nil, 0) {
		t.Fatal("missing native set accepted")
	}
	for _, test := range []struct {
		names []string
		mask  uint64
		want  bool
	}{
		{[]string{}, 0, true},
		{[]string{"CAP_SETUID", "CAP_CHOWN"}, 1 | 1<<7, true},
		{[]string{"CHOWN", "SETUID"}, 1 | 1<<7, true},
		{[]string{}, 1, false},
		{[]string{"CAP_CHOWN"}, 0, false},
		{[]string{"CAP_CHOWN", "CHOWN"}, 1, false},
		{[]string{"CAP_UNKNOWN"}, 0, false},
		{[]string{"ALL"}, 0, false},
		{[]string{"CAP_CAP_CHOWN"}, 1, false},
	} {
		if got := mappedObservedCapabilitiesV1(&test.names, test.mask); got != test.want {
			t.Fatalf("native set %v mask %x: %v", test.names, test.mask, got)
		}
	}
}

func TestMappedNativeSecurityProvenanceV1(t *testing.T) {
	profile := `{"defaultAction":"SCMP_ACT_ERRNO"}`
	if mappedObservedSecurityV1(nil, profile) {
		t.Fatal("missing security provenance accepted")
	}
	for _, nnp := range []string{"no-new-privileges", "no-new-privileges=true"} {
		for _, value := range []string{profile, "/run/containers/seccomp-profile.json"} {
			options := []string{"seccomp=" + value, nnp}
			if !mappedObservedSecurityV1(&options, profile) {
				t.Fatal("normalized Podman provenance rejected", options)
			}
		}
	}
	for _, options := range [][]string{
		{}, {"no-new-privileges"}, {"seccomp=/profile"},
		{"seccomp=unconfined", "no-new-privileges"},
		{"seccomp=", "no-new-privileges"},
		{"seccomp=default", "no-new-privileges"},
		{"seccomp=/profile", "no-new-privileges=false"},
		{"seccomp=/profile", "seccomp=/other"},
		{"no-new-privileges", "no-new-privileges=true"},
		{"seccomp=/a/../profile", "no-new-privileges"},
		{"seccomp=/profile\x00", "no-new-privileges"},
		{"seccomp=/profile", "no-new-privileges", "unmask=all"},
	} {
		if mappedObservedSecurityV1(&options, profile) {
			t.Fatal("unqualified security provenance accepted", options)
		}
	}
}

func TestMappedRequestHashBindsKernelTupleV1(t *testing.T) {
	f := newWaiterFixtureV1(t)
	a := f.start(t)
	r, err := a.sandboxRequest()
	if err != nil {
		t.Fatal(err)
	}
	original, err := canonical.Sum("mapped-sandbox", "v1", r)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"policy", "ABI", "final", "setup", "kernel"} {
		changed := r
		switch field {
		case "policy":
			changed.KernelPolicy = rendererDigest("a")
		case "ABI":
			changed.KernelFacts.ABI = "arm64"
		case "final":
			changed.KernelFacts.FinalCapabilities = "0000000000000001"
		case "setup":
			changed.KernelFacts.SetupCapabilities = "0000000000000000"
		case "kernel":
			changed.KernelFacts.KernelLastCapability = "39"
		}
		got, err := canonical.Sum("mapped-sandbox", "v1", changed)
		if err != nil || got == original {
			t.Fatal("kernel fact absent from request hash", field, err)
		}
		if err := verifyMappedSandboxV1(f.ctx, a.prepared.state, f.effect, changed); err == nil {
			t.Fatal("invalid request tuple accepted", field)
		}
	}
}

func TestMappedNonemptyFinalPolicyUsesExactPrivateHandoffV1(t *testing.T) {
	f := newWaiterFixtureV1(t)
	for _, flag := range []string{"--uid", "--gid"} {
		index := slices.Index(f.prepared.spec.Args, flag)
		if index < 0 {
			t.Fatal("fixture lacks explicit credential", flag)
		}
		f.prepared.spec.Args[index+1] = "0"
	}
	const final uint64 = 1 << 18 // SYS_CHROOT, separate from setup-only capabilities.
	index := slices.Index(f.prepared.spec.Args, "--mapped-capabilities=0000000000000000")
	f.prepared.spec.Args[index] = "--mapped-capabilities=0000000000040000"
	f.prepared.policy = mappedPolicyForSpecFixtureV1(t, f.prepared.spec, "amd64", final)
	a := f.start(t)
	request, err := a.sandboxRequest()
	if err != nil || request.KernelPolicy != canonical.Digest(f.prepared.policy.identity) || request.KernelFacts.FinalCapabilities != "0000000000040000" {
		t.Fatal("final ceiling absent from exact private request", err)
	}
	if !slices.Contains(f.body.Cmd, "--mapped-capabilities=0000000000040000") || !slices.Contains(f.body.HostConfig.CapAdd, "SYS_CHROOT") ||
		!slices.Contains(f.body.HostConfig.CapAdd, "SETPCAP") || f.body.HostConfig.AutoRemove {
		t.Fatal("startup argument, container setup union and policy not coherent")
	}
	if _, err := a.release(); err != nil {
		t.Fatal("injected exact handoff", err)
	}
	select {
	case frame := <-f.frame:
		if frame.Container != f.id {
			t.Fatal("wrong released instance")
		}
	case <-f.ctx.Done():
		t.Fatal("private handoff absent")
	}
	if err := <-f.startErr; err != nil {
		t.Fatal(err)
	}
	if f.admissions != 1 {
		t.Fatal("private handoff did not use the admission writer")
	}
	// This tests production orchestration with an injected trusted effect owner.
	// It is neither a live-engine nor a kernel/effective-policy qualification.
}
