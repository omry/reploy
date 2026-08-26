package blueprint

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestResolveProducesTypedEnvironment(t *testing.T) {
	source, err := Decode([]byte(minimalBlueprint))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	if document.Environment.ControlScript != "appctl" {
		t.Fatalf("control script = %q", document.Environment.ControlScript)
	}
	if document.Environment.AllowConcurrent != ConcurrentRunAuto {
		t.Fatalf("allow concurrent = %q", document.Environment.AllowConcurrent)
	}
	if document.Environment.Runtime.User != DefaultRuntimeUser {
		t.Fatalf("runtime user = %q", document.Environment.Runtime.User)
	}
	if document.Environment.Runtime.Network != (RuntimeNetwork{Public: NetworkAccessDeny, Local: NetworkAccessDeny, Ambiguous: AmbiguousNetworkAccessRequireBoth}) {
		t.Fatalf("runtime network = %#v", document.Environment.Runtime.Network)
	}
	if got := document.Blueprint.Compatibility.Platforms; !reflect.DeepEqual(got, []Platform{
		{OS: "linux", Architecture: "amd64", Canonical: "linux/amd64"},
		{OS: "linux", Architecture: "arm64", Canonical: "linux/arm64"},
	}) {
		t.Fatalf("compatibility platforms = %#v", got)
	}
	if !reflect.DeepEqual(document.Environment.Applications["application"].Executables["server"].Order, DefaultArgumentOrder) {
		t.Fatalf("order = %#v", document.Environment.Applications["application"].Executables["server"].Order)
	}
	if document.Docker.Mounts["data"].Contract.UpdatePolicy != UpdatePreserve {
		t.Fatalf("mount contract = %#v", document.Docker.Mounts["data"].Contract)
	}
	base := document.Environment.Components["base"]
	if base.Type != ComponentTypeBase || base.Base == nil || base.Python != nil || base.APT != nil || base.Base.Image != "python:3.13-slim" {
		t.Fatalf("base component = %#v", base)
	}
	application := document.Environment.Components[ApplicationContributionID("application", ContributionProviderPython)]
	if application.Type != ComponentTypePython || application.Base != nil || application.Python == nil || application.APT != nil {
		t.Fatalf("application component = %#v", application)
	}
	if application.Python.Interpreter != (CommandRequirement{Command: "python"}) {
		t.Fatalf("default Python interpreter = %#v", application.Python.Interpreter)
	}
}

func TestResolveValidatesWorkloadEndpointNames(t *testing.T) {
	for _, test := range []struct {
		name string
		ok   bool
	}{
		{name: "api", ok: true},
		{name: "api_v1", ok: true},
		{name: "api.v1", ok: true},
		{name: "api--v1", ok: true},
		{name: "api__internal", ok: true},
		{name: "2fa", ok: true},
		{name: "API"},
		{name: "-api"},
		{name: "api-"},
		{name: "api/v1"},
		{name: "api:v1"},
		{name: "api@sha256"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, err := Decode([]byte(minimalBlueprint))
			if err != nil {
				t.Fatal(err)
			}
			endpoint := source.Environment.Workload.Endpoints["http"]
			delete(source.Environment.Workload.Endpoints, "http")
			source.Environment.Workload.Endpoints[test.name] = endpoint
			dockerEndpoint := source.Docker.Workload.Endpoints["http"]
			dockerEndpoint.Extends = "environment.workload.endpoints." + test.name
			source.Docker.Workload.Endpoints["http"] = dockerEndpoint

			_, err = Resolve(source)
			if test.ok {
				if err != nil {
					t.Fatalf("Resolve() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "endpoint name") {
				t.Fatalf("Resolve() error = %v, want endpoint-name diagnostic", err)
			}
		})
	}
}

func TestResolveRuntimeNetwork(t *testing.T) {
	for _, test := range []struct {
		name   string
		source RuntimeNetworkSyntax
		want   RuntimeNetwork
		field  string
	}{
		{name: "default", want: RuntimeNetwork{Public: NetworkAccessDeny, Local: NetworkAccessDeny, Ambiguous: AmbiguousNetworkAccessRequireBoth}},
		{name: "public only", source: RuntimeNetworkSyntax{Public: "allow"}, want: RuntimeNetwork{Public: NetworkAccessAllow, Local: NetworkAccessDeny, Ambiguous: AmbiguousNetworkAccessRequireBoth}},
		{name: "local only", source: RuntimeNetworkSyntax{Local: "allow"}, want: RuntimeNetwork{Public: NetworkAccessDeny, Local: NetworkAccessAllow, Ambiguous: AmbiguousNetworkAccessRequireBoth}},
		{name: "both", source: RuntimeNetworkSyntax{Public: "allow", Local: "allow"}, want: RuntimeNetwork{Public: NetworkAccessAllow, Local: NetworkAccessAllow, Ambiguous: AmbiguousNetworkAccessRequireBoth}},
		{name: "ambiguous escape hatch", source: RuntimeNetworkSyntax{Ambiguous: "allow"}, want: RuntimeNetwork{Public: NetworkAccessDeny, Local: NetworkAccessDeny, Ambiguous: AmbiguousNetworkAccessAllow}},
		{name: "invalid public", source: RuntimeNetworkSyntax{Public: "yes"}, field: "environment.runtime.network.public"},
		{name: "invalid local", source: RuntimeNetworkSyntax{Local: "none"}, field: "environment.runtime.network.local"},
		{name: "invalid ambiguous", source: RuntimeNetworkSyntax{Ambiguous: "deny"}, field: "environment.runtime.network.ambiguous"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveRuntimeNetwork(test.source)
			if test.field == "" {
				if err != nil || got != test.want {
					t.Fatalf("runtime network = %#v, %v; want %#v", got, err, test.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("runtime network error = %v", err)
			}
		})
	}
}

func TestResolveRuntimeUser(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  string
		ok    bool
	}{
		{name: "default", want: DefaultRuntimeUser, ok: true},
		{name: "explicit", value: "omegaflow", want: "omegaflow", ok: true},
		{name: "underscore", value: "_agent-2", want: "_agent-2", ok: true},
		{name: "root", value: "root"},
		{name: "uppercase", value: "OmegaFlow"},
		{name: "leading digit", value: "2agent"},
		{name: "space", value: "agent user"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveRuntimeUser(test.value)
			if test.ok {
				if err != nil || got != test.want {
					t.Fatalf("runtime user = %q, %v; want %q", got, err, test.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "environment.runtime.user") {
				t.Fatalf("runtime user error = %v", err)
			}
		})
	}
}

func TestResolveAcceptsBaseOnlyEnvironment(t *testing.T) {
	value := strings.Replace(
		minimalBlueprint,
		"  applications:\n    application:\n      packages:\n        python:\n          requirements: [demo-server]\n      executables:\n        server:\n          source: python\n          binary: demo-server\n",
		"",
		1,
	)
	value = strings.Replace(
		value,
		"  commands:\n    serve:\n      executable: application.server\n      argv: [serve]\n  workload:\n    command: serve\n    endpoints:\n      http:\n        scheme: http\n        port: 8080\n",
		"",
		1,
	)
	value = strings.Replace(
		value,
		"  workload:\n    endpoints:\n      http:\n        extends: environment.workload.endpoints.http\n        bind: {address: 0.0.0.0}\n        publish: {address: 127.0.0.1, staging: 18080, deployed: 8080}\n",
		"",
		1,
	)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Environment.Applications) != 0 || len(document.Environment.Components) != 1 {
		t.Fatalf("base-only environment = %#v", document.Environment)
	}
}

func TestResolveAcceptsSmallestBlueprint(t *testing.T) {
	source, err := Decode([]byte(`blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64]
environment:
  id: minimal
  base:
    image: debian:13
`))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	if document.Environment.ID != "minimal" ||
		document.Environment.Base.Image != "debian:13" ||
		len(document.Environment.Applications) != 0 ||
		len(document.Environment.Components) != 1 {
		t.Fatalf("minimal document = %#v", document)
	}
}

func TestResolveConcurrentRunPolicy(t *testing.T) {
	for _, policy := range []ConcurrentRunPolicy{ConcurrentRunYes, ConcurrentRunNo, ConcurrentRunAuto} {
		t.Run(string(policy), func(t *testing.T) {
			value := strings.Replace(minimalBlueprint, "  base:\n", "  allow_concurrent: "+string(policy)+"\n  base:\n", 1)
			source, err := Decode([]byte(value))
			if err != nil {
				t.Fatal(err)
			}
			document, err := Resolve(source)
			if err != nil {
				t.Fatal(err)
			}
			if document.Environment.AllowConcurrent != policy {
				t.Fatalf("allow concurrent = %q, want %q", document.Environment.AllowConcurrent, policy)
			}
		})
	}
}

func TestResolveRejectsInvalidConcurrentRunPolicy(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "  base:\n", "  allow_concurrent: sometimes\n  base:\n", 1)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Resolve(source)
	if err == nil || !strings.Contains(err.Error(), "environment.allow_concurrent must be yes, no, or auto") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveRequiresQualifiedComponentExecutableReference(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "executable: application.server", "executable: server", 1)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Resolve(source)
	if err == nil || !strings.Contains(err.Error(), `missing qualified executable "server"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveRejectsInvalidExecutableProfileIdentifiers(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name: "profile name",
			value: strings.NewReplacer(
				"        server:\n", "        Server:\n",
				"executable: application.server", "executable: application.Server",
			).Replace(minimalBlueprint),
			want: "environment.applications.application.executables must match [a-z][a-z0-9_-]*",
		},
		{
			name:  "binary output name",
			value: strings.Replace(minimalBlueprint, "binary: demo-server", "binary: demo.server", 1),
			want:  "environment.applications.application.executables.server.binary must match [a-z][a-z0-9_-]*",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := Decode([]byte(test.value))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Resolve(source)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestResolveRejectsInvalidCommandIdentifier(t *testing.T) {
	value := strings.NewReplacer(
		"    serve:\n", "    'serve\\e[2J':\n",
		"    command: serve\n", "    command: 'serve\\e[2J'\n",
	).Replace(minimalBlueprint)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Resolve(source)
	if err == nil || !strings.Contains(err.Error(), "environment.commands must match [a-z][a-z0-9_-]*") {
		t.Fatalf("invalid command identifier error = %v", err)
	}
}

func TestResolveAllowsEqualExecutableProfileNamesInDifferentApplications(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "    application:\n", "    tools:\n      packages:\n        os: [curl]\n      executables:\n        server:\n          source: os\n          binary: curl\n    application:\n", 1)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	if document.Environment.Applications["tools"].Executables["server"].Binary != "curl" || document.Environment.Applications["application"].Executables["server"].Binary != "demo-server" {
		t.Fatalf("applications = %#v", document.Environment.Applications)
	}
}

func TestResolveDefaultsEnvironmentMountTarget(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "      target: /data\n", "", 1)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	if target := document.Environment.Mounts["data"].Target; target != "/mnt/data" {
		t.Fatalf("default mount target = %q", target)
	}
}

func TestResolveRejectsUnsafeEnvironmentMountTargets(t *testing.T) {
	for _, test := range []struct {
		name   string
		target string
		want   string
	}{
		{name: "relative", target: "data", want: "normalized absolute"},
		{name: "root", target: "/", want: "filesystem root"},
		{name: "unclean", target: "/srv/../etc", want: "normalized absolute"},
		{name: "device subtree", target: "/dev/shm", want: `reserved container path "/dev"`},
		{name: "resolver parent", target: "/etc", want: `reserved container path "/etc/hostname"`},
		{name: "Docker resolver file", target: "/etc/resolv.conf", want: `reserved container path "/etc/resolv.conf"`},
		{name: "account database", target: "/etc/passwd", want: `reserved container path "/etc/passwd"`},
		{name: "group database", target: "/etc/group", want: `reserved container path "/etc/group"`},
		{name: "secrets", target: "/run/secrets/app", want: `reserved container path "/run/secrets"`},
		{name: "provider root", target: "/opt/reploy/providers", want: `reserved container path "/opt/reploy"`},
		{name: "temporary home parent", target: "/mnt", want: `reserved container path "/mnt/reploy-home"`},
		{name: "output", target: "/mnt/reploy-output", want: `reserved container path "/mnt/reploy-output"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := strings.Replace(minimalBlueprint, "      target: /data\n", "      target: "+test.target+"\n", 1)
			source, err := Decode([]byte(value))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Resolve(source)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestResolveRequiresStrictBaseComponent(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "missing", old: "  base:\n    image: python:3.13-slim\n", want: "environment.base.image is required"},
		{name: "type", old: "  base:\n    image: python:3.13-slim\n", new: "  base:\n    type: python\n    image: python:3.13-slim\n", want: "field type not found"},
		{name: "provider payload", old: "  base:\n    image: python:3.13-slim\n", new: "  base:\n    image: python:3.13-slim\n    requirements: []\n", want: "field requirements not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := strings.Replace(minimalBlueprint, tt.old, tt.new, 1)
			source, err := Decode([]byte(value))
			if err != nil {
				if !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("decode error = %v, want %q", err, tt.want)
				}
				return
			}
			_, err = Resolve(source)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestResolvePythonComponentOptions(t *testing.T) {
	value := strings.Replace(minimalBlueprint,
		"          requirements: [demo-server]\n",
		"          interpreter: {command: python, version: '>=3.11', supplier: base}\n          requirements: [demo-server, demo-server]\n      options:\n        imap:\n          description: Install IMAP support.\n          packages:\n            python:\n              requirements: [demo-imap, demo-imap]\n", 1)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	component := document.Environment.Components[ApplicationContributionID("application", ContributionProviderPython)]
	if component.Python.Interpreter != (CommandRequirement{Command: "python", Version: ">=3.11", Supplier: "base"}) {
		t.Fatalf("interpreter = %#v", component.Python.Interpreter)
	}
	if !reflect.DeepEqual(component.Python.Requirements, []string{"demo-server"}) {
		t.Fatalf("requirements = %#v", component.Python.Requirements)
	}
	if got := component.Options["imap"].PythonRequirements; !reflect.DeepEqual(got, []string{"demo-imap"}) {
		t.Fatalf("option requirements = %#v", got)
	}
}

func TestResolveRejectsOptionSpecificPythonInterpreter(t *testing.T) {
	value := strings.Replace(minimalBlueprint,
		"      executables:\n",
		"      options:\n        debug:\n          description: Install debugging support.\n          packages:\n            python:\n              interpreter: {command: python, supplier: base}\n              requirements: [debugpy]\n      executables:\n",
		1,
	)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Resolve(source)
	if err == nil || !strings.Contains(err.Error(), "options.debug.packages.python.interpreter is not valid") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveRejectsOptionSpecificPortableToolsFieldWhenEmpty(t *testing.T) {
	for _, tools := range []string{"[]", "null"} {
		t.Run(tools, func(t *testing.T) {
			option := strings.Replace(
				"      options:\n        debug:\n          description: Install debugging support.\n          packages:\n            os: [curl]\n            tools: TOOLS\n      executables:\n",
				"TOOLS", tools, 1,
			)
			value := strings.Replace(minimalBlueprint, "      executables:\n", option, 1)
			source, err := Decode([]byte(value))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Resolve(source)
			if err == nil || !strings.Contains(err.Error(), "options.debug.packages.tools is not valid") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestDecodeRejectsProviderOwnedApplicationFields(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "application type", old: "    application:\n", new: "    application:\n      type: python\n", want: "field type not found"},
		{name: "application requirements", old: "    application:\n", new: "    application:\n      requirements: [demo-server]\n", want: "field requirements not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode([]byte(strings.Replace(minimalBlueprint, tt.old, tt.new, 1)))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestResolveOSContributionSyntax(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "  applications:\n",
		"  packages:\n    os:\n      - curl\n      - package: python3=3.11.2-1+deb12u1\n        exports:\n          python:\n            executable: /usr/bin/python3\n  applications:\n", 1)
	value = strings.Replace(value, "    application:\n",
		"    application:\n      options:\n        git:\n          description: Install Git.\n          packages:\n            os: [git]\n", 1)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	if got := document.Environment.Packages.OS; len(got) != 2 || got[0].Name != "curl" || got[1].Exports["python"].Executable != "/usr/bin/python3" {
		t.Fatalf("environment OS packages = %#v", got)
	}
	if got := document.Environment.Applications["application"].Options["git"].Packages.OS; len(got) != 1 || got[0].Name != "git" {
		t.Fatalf("application OS option = %#v", got)
	}
}

func TestResolveRequiresBlueprintCompatibilityPlatforms(t *testing.T) {
	withoutCompatibility := strings.Replace(minimalBlueprint, "  compatibility:\n    platforms: [linux/amd64, linux/arm64]\n", "", 1)
	source, err := Decode([]byte(withoutCompatibility))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Resolve(source)
	if err == nil || !strings.Contains(err.Error(), "blueprint.compatibility.platforms must not be empty") {
		t.Fatalf("Resolve() error = %v", err)
	}
}

func TestResolveMountUpdateMatrix(t *testing.T) {
	tests := []struct {
		name   string
		mode   MountMode
		update UpdatePolicy
		source string
		volume string
		ok     bool
	}{
		{name: "managed preserve", mode: MountManagedBind, update: UpdatePreserve, source: "data", ok: true},
		{name: "managed replace", mode: MountManagedBind, update: UpdateReplace, source: "data", ok: true},
		{name: "managed unmanaged", mode: MountManagedBind, update: UpdateUnmanaged, source: "data"},
		{name: "volume preserve", mode: MountVolume, update: UpdatePreserve, volume: "data", ok: true},
		{name: "volume replace", mode: MountVolume, update: UpdateReplace, volume: "data", ok: true},
		{name: "volume unmanaged", mode: MountVolume, update: UpdateUnmanaged, volume: "data"},
		{name: "bind unmanaged", mode: MountBind, update: UpdateUnmanaged, source: "/srv/data", ok: true},
		{name: "bind preserve", mode: MountBind, update: UpdatePreserve, source: "/srv/data"},
		{name: "tmpfs preserve", mode: MountTmpfs, update: UpdatePreserve, ok: true},
		{name: "tmpfs replace", mode: MountTmpfs, update: UpdateReplace, ok: true},
		{name: "tmpfs unmanaged", mode: MountTmpfs, update: UpdateUnmanaged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMount("mount", DockerMount{Mode: tt.mode, Source: tt.source, Name: tt.volume, Contract: EnvironmentMount{UpdatePolicy: tt.update}})
			if (err == nil) != tt.ok {
				t.Fatalf("error = %v, ok = %v", err, tt.ok)
			}
		})
	}
}

func TestResolveArgumentOrderValidation(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		ok     bool
	}{
		{name: "default", ok: true},
		{name: "binary only", values: []string{"binary"}, ok: true},
		{name: "suffix before forwarded", values: []string{"binary", "command", "suffix", "forwarded"}, ok: true},
		{name: "binary not first", values: []string{"command", "binary"}},
		{name: "duplicate", values: []string{"binary", "command", "command"}},
		{name: "unknown", values: []string{"binary", "mystery"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveOrder(tt.values)
			if (err == nil) != tt.ok {
				t.Fatalf("error = %v, ok = %v", err, tt.ok)
			}
		})
	}
}

func TestResolveRejectsInvalidReadinessAndCommandExposure(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "readiness path", old: "        port: 8080\n", new: "        port: 8080\n        readiness: {path: relative}\n", want: "must begin with /"},
		{name: "readiness scheme", old: "        scheme: http\n", new: "        scheme: smtp\n        readiness: {path: /ready}\n", want: "requires http or https"},
		{name: "deployed not native", old: "      argv: [serve]\n", new: "      argv: [serve]\n      deployed_command: true\n", want: "requires native_command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, err := Decode([]byte(strings.Replace(minimalBlueprint, tt.old, tt.new, 1)))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Resolve(source)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestResolveCommandMountWritabilityOverrides(t *testing.T) {
	value := strings.Replace(minimalBlueprint,
		"    serve:\n      executable: application.server\n      argv: [serve]\n",
		"    serve:\n      executable: application.server\n      argv: [serve]\n      mounts:\n        data:\n          writable: false\n", 1)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	override := document.Environment.Commands["serve"].Mounts["data"]
	if override.Writable {
		t.Fatalf("command mount override = %#v", override)
	}
}

func TestResolveRejectsInvalidCommandMountOverrides(t *testing.T) {
	for _, test := range []struct {
		name     string
		mount    string
		writable string
		want     string
	}{
		{name: "unknown mount", mount: "missing", writable: "true", want: `references unknown environment mount "missing"`},
		{name: "missing writable", mount: "data", want: "writable is required"},
		{name: "non-boolean writable", mount: "data", writable: "sometimes", want: "must resolve to a boolean"},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := "        " + test.mount + ":\n"
			if test.writable != "" {
				entry += "          writable: " + test.writable + "\n"
			}
			value := strings.Replace(minimalBlueprint,
				"    serve:\n      executable: application.server\n      argv: [serve]\n",
				"    serve:\n      executable: application.server\n      argv: [serve]\n      mounts:\n"+entry, 1)
			source, err := Decode([]byte(value))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Resolve(source)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestResolveRejectsDuplicateMountReferenceThatLeavesPathUnmapped(t *testing.T) {
	value := strings.Replace(minimalBlueprint,
		"    data:\n      target: /data\n      writable: true\n      update_policy: preserve\n",
		"    data:\n      target: /data\n      writable: true\n      update_policy: preserve\n    cache:\n      target: /cache\n      update_policy: preserve\n", 1)
	value = strings.Replace(value,
		"      source: data\n  workload:\n    endpoints:\n",
		"      source: data\n    duplicate:\n      extends: environment.mounts.data\n      mode: managed-bind\n      source: duplicate\n  workload:\n    endpoints:\n", 1)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Resolve(source)
	if err == nil || !strings.Contains(err.Error(), "environment mount \"cache\" must have exactly one Docker mount") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveStaticStringRejectsUnknownAndRuntimeReferences(t *testing.T) {
	for _, value := range []string{"/{{ missing }}", "{{ reploy.phase }}"} {
		if _, err := resolveStaticString(value, map[string]any{}); err == nil {
			t.Fatalf("resolveStaticString(%q) succeeded", value)
		}
	}
}

func TestResolvedMinimalGolden(t *testing.T) {
	source, err := Decode([]byte(minimalBlueprint))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	type golden struct {
		ID            string           `json:"id"`
		ControlScript string           `json:"control_script"`
		Component     Component        `json:"component"`
		Mount         EnvironmentMount `json:"mount"`
		Executable    Executable       `json:"executable"`
		Endpoint      Endpoint         `json:"endpoint"`
		MountMode     MountMode        `json:"mount_mode"`
		Publication   Publication      `json:"publication"`
	}
	actual, err := json.MarshalIndent(golden{
		ID: document.Environment.ID, ControlScript: document.Environment.ControlScript,
		Component: document.Environment.Components[ApplicationContributionID("application", ContributionProviderPython)],
		Mount:     document.Environment.Mounts["data"], Executable: document.Environment.Applications["application"].Executables["server"],
		Endpoint: document.Environment.Workload.Endpoints["http"], MountMode: document.Docker.Mounts["data"].Mode,
		Publication: document.Docker.Workload.Endpoints["http"].Publish,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/resolved_minimal.json")
	if err != nil {
		t.Fatal(err)
	}
	normalizedWant := strings.ReplaceAll(string(want), "\r\n", "\n")
	if strings.TrimSpace(string(actual)) != strings.TrimSpace(normalizedWant) {
		t.Fatalf("resolved golden mismatch\nactual:\n%s\nwant:\n%s", actual, want)
	}
}
