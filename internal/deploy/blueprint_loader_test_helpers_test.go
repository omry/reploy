package deploy

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

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
	t.Cleanup(func() { pyPIHTTPClient = oldClient })
	return baseURL
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

type fakeRoundTripper map[string][]byte

func (transport fakeRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	content, ok := transport[request.URL.String()]
	if !ok {
		return &http.Response{
			StatusCode: http.StatusNotFound, Status: "404 Not Found",
			Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header), Request: request,
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK, Status: "200 OK",
		Body: io.NopCloser(bytes.NewReader(content)), Header: make(http.Header), Request: request,
	}, nil
}
