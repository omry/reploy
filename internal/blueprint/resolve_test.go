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
	if !reflect.DeepEqual(document.Environment.Executables["server"].Order, DefaultArgumentOrder) {
		t.Fatalf("order = %#v", document.Environment.Executables["server"].Order)
	}
	if document.Docker.Mounts["data"].Path.Update != UpdatePreserve {
		t.Fatalf("mount path = %#v", document.Docker.Mounts["data"].Path)
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
