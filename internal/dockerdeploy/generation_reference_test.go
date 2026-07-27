package dockerdeploy

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestEnvironmentImageReferencesAreDirectoryOwnedAndUnique(t *testing.T) {
	dir := t.TempDir()
	random := bytes.NewReader(append(bytes.Repeat([]byte{0x11}, environmentReferenceRandomBytes), bytes.Repeat([]byte{0x22}, environmentReferenceRandomBytes)...))
	references, err := newEnvironmentImageReferences("Demo App", dir, random)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := pathIdentityHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "reploy/env/demo-app-" + hash + ":"
	if references.Temporary != prefix+"tmp-"+strings.Repeat("11", environmentReferenceRandomBytes) {
		t.Fatalf("temporary reference = %q", references.Temporary)
	}
	if references.Generation != prefix+"g-"+strings.Repeat("22", environmentReferenceRandomBytes) {
		t.Fatalf("generation reference = %q", references.Generation)
	}
	if err := ValidateEnvironmentImageReferences(references, "Demo App", dir); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if err := ValidateEnvironmentImageReferences(references, "Demo App", other); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("error = %v", err)
	}
}

func TestEnvironmentImageReferencesRejectMalformedInputs(t *testing.T) {
	dir := t.TempDir()
	if _, err := newEnvironmentImageReferences("", dir, strings.NewReader("unused")); err == nil || !strings.Contains(err.Error(), "environment name") {
		t.Fatalf("error = %v", err)
	}
	if _, err := newEnvironmentImageReferences("demo", dir, nil); err == nil || !strings.Contains(err.Error(), "randomness") {
		t.Fatalf("error = %v", err)
	}
	if _, err := newEnvironmentImageReferences("demo", dir, errorReader{}); err == nil || !strings.Contains(err.Error(), "random") {
		t.Fatalf("error = %v", err)
	}

	validRandom := bytes.NewReader(bytes.Repeat([]byte{0x33}, environmentReferenceRandomBytes*2))
	references, err := newEnvironmentImageReferences("demo", dir, validRandom)
	if err != nil {
		t.Fatal(err)
	}
	references.Generation += "x"
	if err := ValidateEnvironmentImageReferences(references, "demo", dir); err == nil || !strings.Contains(err.Error(), "invalid random suffix") {
		t.Fatalf("error = %v", err)
	}
	references, err = newEnvironmentImageReferences("demo", dir, bytes.NewReader(bytes.Repeat([]byte{0xab}, environmentReferenceRandomBytes*2)))
	if err != nil {
		t.Fatal(err)
	}
	suffixStart := len(references.Temporary) - environmentReferenceRandomBytes*2
	references.Temporary = references.Temporary[:suffixStart] + strings.ToUpper(references.Temporary[suffixStart:])
	if err := ValidateEnvironmentImageReferences(references, "demo", dir); err == nil || !strings.Contains(err.Error(), "invalid random suffix") {
		t.Fatalf("error = %v", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("injected random failure") }
