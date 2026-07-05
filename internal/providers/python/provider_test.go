package python

import "testing"

func TestClassifyRootAcceptsContainerAbsoluteWheel(t *testing.T) {
	root, err := ClassifyRoot("/bundle/demo-1.0.0-py3-none-any.whl")
	if err != nil {
		t.Fatal(err)
	}
	if root.Provider != ProviderName || root.Kind != "wheel" || root.Source != "/bundle/demo-1.0.0-py3-none-any.whl" {
		t.Fatalf("root = %#v", root)
	}
}
