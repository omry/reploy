package python

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	providerapi "github.com/omry/reploy/internal/providers"
)

func TestPreparedBundleResolverReadsClosedArtifactsAndScripts(t *testing.T) {
	dir := t.TempDir()
	writeTestWheel(t, dir, "demo_server-1.2.3-py3-none-any.whl", "Demo-Server", "1.2.3", map[string]string{"demo-server": "demo:main"})
	resolver := PreparedBundleResolver{Dir: dir, BaseIdentity: "python@sha256:base"}
	resolved, err := resolver.ResolvePython(context.Background(), providerapi.ResolveRequest{
		Platform:   "linux/amd64",
		Components: []providerapi.Component{{Name: "application", Requirements: []string{"demo-server[http]>=1.2"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Artifacts) != 1 || resolved.Artifacts[0].Identifier != "demo-server" || len(resolved.Artifacts[0].SHA256) != 64 {
		t.Fatalf("artifacts = %#v", resolved.Artifacts)
	}
	if resolved.ConsoleScripts["demo-server"] != "demo-server" {
		t.Fatalf("scripts = %#v", resolved.ConsoleScripts)
	}
}

func TestPreparedBundleResolverEnforcesTranslationPrecedence(t *testing.T) {
	dir := t.TempDir()
	wheel := "demo_server-1.0-py3-none-any.whl"
	writeTestWheel(t, dir, wheel, "demo-server", "1.0", nil)
	request := providerapi.ResolveRequest{
		Components:   []providerapi.Component{{Name: "application", Requirements: []string{"demo-server"}}},
		Translations: []providerapi.Translation{{Name: "workspace", Root: "..", Mappings: map[string]string{"demo-server": "server"}}},
	}
	resolver := PreparedBundleResolver{Dir: dir, BaseIdentity: "python@sha256:base"}
	if _, err := resolver.ResolvePython(context.Background(), request); err == nil || !strings.Contains(err.Error(), "did not take precedence") {
		t.Fatalf("error = %v", err)
	}
	manifest := map[string]any{
		"schema_version": 1,
		"local_sources":  map[string]any{"demo-server": map[string]any{"wheel": wheel, "fingerprint": "test"}},
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, preparedBundleManifestName), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolvePython(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedBundleResolverRejectsTranslatedVersionOutsideRequirement(t *testing.T) {
	dir := t.TempDir()
	wheel := "demo_server-1.4-py3-none-any.whl"
	writeTestWheel(t, dir, wheel, "demo-server", "1.4", nil)
	manifest := map[string]any{
		"schema_version": 1,
		"local_sources":  map[string]any{"demo-server": map[string]any{"wheel": wheel, "fingerprint": "test"}},
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, preparedBundleManifestName), content, 0o644); err != nil {
		t.Fatal(err)
	}
	request := providerapi.ResolveRequest{
		Components:   []providerapi.Component{{Name: "application", Requirements: []string{"demo-server[imap]>=2.0,<3"}}},
		Translations: []providerapi.Translation{{Name: "workspace", Mappings: map[string]string{"demo-server": "server"}}},
	}
	_, err = (PreparedBundleResolver{Dir: dir, BaseIdentity: "python@sha256:base"}).ResolvePython(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), `built version 1.4 does not satisfy`) {
		t.Fatalf("error = %v", err)
	}
}

func TestRequirementAllowsVersion(t *testing.T) {
	tests := []struct {
		requirement string
		version     string
		want        bool
	}{
		{"demo>=1.4,<2", "1.7.2", true},
		{"demo>=1.4,<2", "2.0", false},
		{"demo==1.4.*", "1.4.9", true},
		{"demo!=1.4.*", "1.4.9", false},
		{"demo~=1.4.5", "1.4.9", true},
		{"demo~=1.4.5", "1.5", false},
	}
	for _, test := range tests {
		got, checked := requirementAllowsVersion(test.requirement, test.version)
		if !checked || got != test.want {
			t.Errorf("requirementAllowsVersion(%q, %q) = (%v, %v), want (%v, true)", test.requirement, test.version, got, checked, test.want)
		}
	}
}

func TestPreparedBundleResolverRejectsMetadataAndDuplicateCollisions(t *testing.T) {
	t.Run("filename metadata mismatch", func(t *testing.T) {
		dir := t.TempDir()
		writeTestWheel(t, dir, "demo-1.0-py3-none-any.whl", "other", "1.0", nil)
		_, err := (PreparedBundleResolver{Dir: dir, BaseIdentity: "base"}).ResolvePython(context.Background(), providerapi.ResolveRequest{})
		if err == nil || !strings.Contains(err.Error(), "metadata identifies") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("duplicate console script", func(t *testing.T) {
		dir := t.TempDir()
		writeTestWheel(t, dir, "one-1.0-py3-none-any.whl", "one", "1.0", map[string]string{"demo": "one:main"})
		writeTestWheel(t, dir, "two-1.0-py3-none-any.whl", "two", "1.0", map[string]string{"demo": "two:main"})
		_, err := (PreparedBundleResolver{Dir: dir, BaseIdentity: "base"}).ResolvePython(context.Background(), providerapi.ResolveRequest{})
		if err == nil || !strings.Contains(err.Error(), "provided by both") {
			t.Fatalf("error = %v", err)
		}
	})
}

func writeTestWheel(t *testing.T, dir string, filename string, name string, version string, scripts map[string]string) {
	t.Helper()
	file, err := os.Create(filepath.Join(dir, filename))
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	distInfo := strings.ReplaceAll(name, "-", "_") + "-" + version + ".dist-info/"
	writeZipFile(t, archive, distInfo+"METADATA", "Metadata-Version: 2.1\nName: "+name+"\nVersion: "+version+"\n\n")
	writeZipFile(t, archive, distInfo+"WHEEL", "Wheel-Version: 1.0\nGenerator: reploy-test\nRoot-Is-Purelib: true\nTag: py3-none-any\n")
	if len(scripts) > 0 {
		var entries strings.Builder
		entries.WriteString("[console_scripts]\n")
		for name, target := range scripts {
			entries.WriteString(name + " = " + target + "\n")
		}
		writeZipFile(t, archive, distInfo+"entry_points.txt", entries.String())
	}
	writeZipFile(t, archive, distInfo+"RECORD", "")
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeZipFile(t *testing.T, archive *zip.Writer, name string, content string) {
	t.Helper()
	writer, err := archive.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
}
