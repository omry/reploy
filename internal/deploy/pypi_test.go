package deploy

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPackFromPinnedPyPIWheel(t *testing.T) {
	wheel := testPackWheel(t, "demo_pkg/reploy")
	indexURL := testPyPIIndex(t, wheel, "1.2.3")
	t.Setenv("REPLOY_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	ref, err := ParsePackRef("pypi:demo-pkg==1.2.3//demo_pkg/reploy?index-url=" + indexURL)
	if err != nil {
		t.Fatal(err)
	}

	pack, err := LoadPack(ref)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Ref.Raw != "pypi:demo-pkg==1.2.3//demo_pkg/reploy" {
		t.Fatalf("resolved ref = %q", pack.Ref.Raw)
	}
	if pack.RequestedRef.Raw != ref.Raw {
		t.Fatalf("requested ref = %q", pack.RequestedRef.Raw)
	}
	if pack.ResolvedArtifact == nil {
		t.Fatal("missing resolved artifact")
	}
	if pack.ResolvedArtifact.Package != "demo-pkg" || pack.ResolvedArtifact.Version != "1.2.3" {
		t.Fatalf("artifact = %#v", pack.ResolvedArtifact)
	}
	if !strings.Contains(pack.ResolvedArtifact.CachePath, "demo-pkg") {
		t.Fatalf("cache path = %q", pack.ResolvedArtifact.CachePath)
	}
	if pack.App.Provider.Identifier != "arbiter-server" {
		t.Fatalf("provider identifier = %q", pack.App.Provider.Identifier)
	}
}

func TestLoadPackFromLatestPyPIWheelResolvesExactVersion(t *testing.T) {
	wheel := testPackWheel(t, "demo_pkg/reploy")
	indexURL := testPyPIIndex(t, wheel, "2.0.0")
	t.Setenv("REPLOY_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	ref, err := ParsePackRef("pypi:demo-pkg//demo_pkg/reploy?index-url=" + indexURL)
	if err != nil {
		t.Fatal(err)
	}

	pack, err := LoadPack(ref)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Ref.Raw != "pypi:demo-pkg==2.0.0//demo_pkg/reploy" {
		t.Fatalf("resolved ref = %q", pack.Ref.Raw)
	}
	if !pack.Ref.IsPinned {
		t.Fatalf("resolved ref should be pinned: %#v", pack.Ref)
	}
	if pack.RequestedRef.Raw != ref.Raw {
		t.Fatalf("requested ref = %q", pack.RequestedRef.Raw)
	}
}

func TestLoadPackFromSimplePyPIWheelWithoutPackFiles(t *testing.T) {
	subdir := "demo_pkg/reploy"
	wheel := testPackWheelWithFiles(t, map[string]string{
		subdir + "/arbiter.blueprint.yaml": simplePackTestManifest(),
	})
	indexURL := testPyPIIndex(t, wheel, "2.1.0")
	t.Setenv("REPLOY_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	ref, err := ParsePackRef("pypi:demo-pkg//" + subdir + "?index-url=" + indexURL)
	if err != nil {
		t.Fatal(err)
	}

	pack, err := LoadPack(ref)
	if err != nil {
		t.Fatal(err)
	}
	if pack.App.Provider.Identifier != "demo" {
		t.Fatalf("provider identifier = %q", pack.App.Provider.Identifier)
	}
	if len(pack.Bundle.Options) != 0 {
		t.Fatalf("bundle options = %#v", pack.Bundle.Options)
	}
	if pack.ResolvedArtifact == nil || pack.ResolvedArtifact.Version != "2.1.0" {
		t.Fatalf("resolved artifact = %#v", pack.ResolvedArtifact)
	}
}

func TestLoadPackFromPyPIWheelReportsMissingBlueprintPathWithResolvedVersion(t *testing.T) {
	wheel := testPackWheelWithFiles(t, map[string]string{
		"demo_pkg/other/arbiter.blueprint.yaml": simplePackTestManifest(),
	})
	indexURL := testPyPIIndex(t, wheel, "2.2.0")
	t.Setenv("REPLOY_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	ref, err := ParsePackRef("pypi:demo-pkg//demo_pkg/reploy?index-url=" + indexURL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = LoadPack(ref)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		"blueprint path not found in PyPI wheel demo-pkg==2.2.0",
		"demo_pkg-2.2.0-py3-none-any.whl",
		"demo_pkg/reploy",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}

func TestLoadResolvedPackUsesCachedBlueprintPath(t *testing.T) {
	wheel := testPackWheel(t, "demo_pkg/reploy")
	indexURL := testPyPIIndex(t, wheel, "3.0.0")
	t.Setenv("REPLOY_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	ref, err := ParsePackRef("pypi:demo-pkg//demo_pkg/reploy?index-url=" + indexURL)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := LoadPack(ref)
	if err != nil {
		t.Fatal(err)
	}
	oldClient := pyPIHTTPClient
	pyPIHTTPClient = &http.Client{Transport: fakeRoundTripper{}}
	t.Cleanup(func() {
		pyPIHTTPClient = oldClient
	})

	cachedPack, err := LoadResolvedPack(pack.Ref, pack.RequestedRef.Raw, pack.ResolvedArtifact)
	if err != nil {
		t.Fatal(err)
	}
	if cachedPack.App.Provider.Identifier != "arbiter-server" {
		t.Fatalf("provider identifier = %q", cachedPack.App.Provider.Identifier)
	}
}

func testPyPIIndex(t *testing.T, wheel []byte, version string) string {
	t.Helper()
	sha256 := HashBytes(wheel)
	filename := fmt.Sprintf("demo_pkg-%s-py3-none-any.whl", version)
	baseURL := "https://pypi.test"
	wheelURL := baseURL + "/files/" + filename
	metadata := fmt.Sprintf(`{
  "info": {"version": %q},
  "releases": {
    %q: [{
      "filename": %q,
      "url": %q,
      "packagetype": "bdist_wheel",
      "digests": {"sha256": %q}
    }]
  },
  "urls": []
}`, version, version, filename, wheelURL, sha256)
	oldClient := pyPIHTTPClient
	pyPIHTTPClient = &http.Client{Transport: fakeRoundTripper{
		baseURL + "/pypi/demo-pkg/json": []byte(metadata),
		wheelURL:                        wheel,
	}}
	t.Cleanup(func() {
		pyPIHTTPClient = oldClient
	})
	return baseURL
}

func testPackWheel(t *testing.T, subdir string) []byte {
	t.Helper()
	return testPackWheelWithFiles(t, map[string]string{
		subdir + "/arbiter.blueprint.yaml": packTestManifest(),
	})
}

func testPackWheelWithFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for path, content := range files {
		file, err := writer.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func simplePackTestManifest() string {
	return `blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.1.0"

app:
  id: demo
  provider:
    type: python
    identifier: demo

docker:
  deployment_dirs:
    config: conf
    bundle: .reploy/bundle
    data: data
  default_command: serve
  commands:
    serve:
      container:
        argv:
          - demo
          - serve
`
}

type fakeRoundTripper map[string][]byte

func (transport fakeRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	content, ok := transport[request.URL.String()]
	if !ok {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Body:       io.NopCloser(strings.NewReader("not found")),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(bytes.NewReader(content)),
		Header:     make(http.Header),
		Request:    request,
	}, nil
}
