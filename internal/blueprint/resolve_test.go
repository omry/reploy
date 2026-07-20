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
	if document.Environment.ControlScript != "demo" {
		t.Fatalf("control script = %q", document.Environment.ControlScript)
	}
	if got := document.Blueprint.Compatibility.Platforms; !reflect.DeepEqual(got, []Platform{
		{OS: "linux", Architecture: "amd64", Canonical: "linux/amd64"},
		{OS: "linux", Architecture: "arm64", Canonical: "linux/arm64"},
	}) {
		t.Fatalf("compatibility platforms = %#v", got)
	}
	if !reflect.DeepEqual(document.Environment.Executables["server"].Order, DefaultArgumentOrder) {
		t.Fatalf("order = %#v", document.Environment.Executables["server"].Order)
	}
	if document.Docker.Mounts["data"].Path.Update != UpdatePreserve {
		t.Fatalf("mount path = %#v", document.Docker.Mounts["data"].Path)
	}
	base := document.Environment.Components["base"]
	if base.Type != ComponentTypeBase || base.Base == nil || base.Python != nil || base.APT != nil || base.Base.Image != "python:3.13-slim" {
		t.Fatalf("base component = %#v", base)
	}
	application := document.Environment.Components["application"]
	if application.Type != ComponentTypePython || application.Base != nil || application.Python == nil || application.APT != nil {
		t.Fatalf("application component = %#v", application)
	}
	if application.Python.Interpreter != (CommandRequirement{Command: "python"}) {
		t.Fatalf("default Python interpreter = %#v", application.Python.Interpreter)
	}
}

func TestResolveCanonicalizesAdditionalDockerMountRoots(t *testing.T) {
	value := strings.Replace(minimalBlueprint, "docker:\n", "docker:\n  additional_mount_roots: [/srv/z, /etc/demo]\n", 1)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/etc/demo", "/srv/z"}
	if !reflect.DeepEqual(document.Docker.AdditionalMountRoots, want) {
		t.Fatalf("additional mount roots = %#v, want %#v", document.Docker.AdditionalMountRoots, want)
	}
}

func TestResolveRejectsUnsafeAdditionalDockerMountRoots(t *testing.T) {
	for _, test := range []struct {
		name  string
		roots string
		want  string
	}{
		{name: "relative", roots: "[srv/demo]", want: "normalized absolute"},
		{name: "root", roots: "[/]", want: "other than /"},
		{name: "unclean", roots: "[/srv/../etc]", want: "normalized absolute"},
		{name: "duplicate", roots: "[/srv/demo, /srv/demo]", want: "overlap"},
		{name: "nested", roots: "[/srv, /srv/demo]", want: "overlap"},
		{name: "nested separated by sort", roots: "[/srv, /srv-a, /srv/demo]", want: "overlap"},
		{name: "mnt", roots: "[/mnt]", want: "built-in /mnt"},
		{name: "beneath mnt", roots: "[/mnt/demo]", want: "built-in /mnt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := strings.Replace(minimalBlueprint, "docker:\n", "docker:\n  additional_mount_roots: "+test.roots+"\n", 1)
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
		{name: "missing", old: "    base:\n      image: python:3.13-slim\n", want: "environment.components.base is required"},
		{name: "type", old: "    base:\n      image: python:3.13-slim\n", new: "    base:\n      type: python\n      image: python:3.13-slim\n", want: ".type is not valid for the base component"},
		{name: "provider payload", old: "    base:\n      image: python:3.13-slim\n", new: "    base:\n      image: python:3.13-slim\n      requirements: []\n", want: ".requirements is not valid for the base component"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := strings.Replace(minimalBlueprint, tt.old, tt.new, 1)
			source, err := Decode([]byte(value))
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

func TestResolvePythonComponentOptions(t *testing.T) {
	value := strings.Replace(minimalBlueprint,
		"      requirements: [demo-server]\n",
		"      interpreter: {command: python, version: '>=3.11', supplier: base}\n      requirements: [demo-server, demo-server]\n      options:\n        imap:\n          description: Install IMAP support.\n          requirements: [demo-imap, demo-imap]\n", 1)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	component := document.Environment.Components["application"]
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

func TestResolveRejectsProviderOwnedUnionFields(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "Python packages", old: "      requirements: [demo-server]\n", new: "      requirements: [demo-server]\n      packages: []\n", want: ".packages is not valid for a Python component"},
		{name: "Python option packages", old: "      requirements: [demo-server]\n", new: "      requirements: [demo-server]\n      options:\n        imap:\n          description: IMAP\n          packages: []\n", want: ".packages is not valid for a Python option"},
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

func TestResolveAPTComponentSyntax(t *testing.T) {
	value := strings.Replace(minimalBlueprint,
		"    application:\n",
		"    system:\n      type: apt\n      packages:\n        - curl\n        - package: python3=3.11.2-1+deb12u1\n          exports:\n            python:\n              executable: /usr/bin/python3\n      options:\n        git:\n          description: Install Git.\n          packages: [git]\n    application:\n", 1)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	component, err := resolveComponent("system", source.Environment.Components["system"])
	if err != nil {
		t.Fatal(err)
	}
	if component.Type != ComponentTypeAPT || component.Base != nil || component.Python != nil || component.APT == nil {
		t.Fatalf("APT component union = %#v", component)
	}
	if got := component.APT.Packages; len(got) != 2 || got[0].Name != "curl" || got[1].Exports["python"].Executable != "/usr/bin/python3" {
		t.Fatalf("APT packages = %#v", got)
	}
	if got := component.Options["git"].APTPackages; len(got) != 1 || got[0].Name != "git" {
		t.Fatalf("APT option = %#v", got)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	if got := document.Environment.Components["system"]; got.Type != ComponentTypeAPT || got.APT == nil || len(got.APT.Packages) != 2 {
		t.Fatalf("resolved APT component = %#v", got)
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
			err := validateMount("mount", DockerMount{Mode: tt.mode, Source: tt.source, Name: tt.volume, Path: Path{Update: tt.update}})
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

func TestResolveRejectsDuplicateMountReferenceThatLeavesPathUnmapped(t *testing.T) {
	value := strings.Replace(minimalBlueprint,
		"    data:\n      container: /data\n      writable: true\n      update: preserve\n",
		"    data:\n      container: /data\n      writable: true\n      update: preserve\n    cache:\n      container: /cache\n      update: preserve\n", 1)
	value = strings.Replace(value,
		"      source: data\n  workload:\n    endpoints:\n",
		"      source: data\n    duplicate:\n      extends: environment.paths.data\n      mode: managed-bind\n      source: duplicate\n  workload:\n    endpoints:\n", 1)
	source, err := Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Resolve(source)
	if err == nil || !strings.Contains(err.Error(), "environment path \"cache\" must have exactly one Docker mount") {
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
		ID            string      `json:"id"`
		ControlScript string      `json:"control_script"`
		Component     Component   `json:"component"`
		Path          Path        `json:"path"`
		Executable    Executable  `json:"executable"`
		Endpoint      Endpoint    `json:"endpoint"`
		MountMode     MountMode   `json:"mount_mode"`
		Publication   Publication `json:"publication"`
	}
	actual, err := json.MarshalIndent(golden{
		ID: document.Environment.ID, ControlScript: document.Environment.ControlScript,
		Component: document.Environment.Components["application"],
		Path:      document.Environment.Paths["data"], Executable: document.Environment.Executables["server"],
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
	if strings.TrimSpace(string(actual)) != strings.TrimSpace(string(want)) {
		t.Fatalf("resolved golden mismatch\nactual:\n%s\nwant:\n%s", actual, want)
	}
}
