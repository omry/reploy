package python

import (
	"crypto/sha256"
	"fmt"
	"path"
	"strings"
	"testing"
)

func TestRuntimeRootV1UsesBoundedDigestForLongApplication(t *testing.T) {
	short, err := RuntimeRootV1("application")
	if err != nil {
		t.Fatal(err)
	}
	if short != path.Join(InstallRoot, "application") {
		t.Fatalf("short runtime root = %q", short)
	}

	application := strings.Repeat("long-application-name-", 20) + "4"
	component := "application/" + application + "/python"
	root, err := RuntimeRootV1(component)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("application/" + application))
	digestText := fmt.Sprintf("%x", digest)
	if digestText[0] < 'a' || digestText[0] > 'f' {
		t.Fatalf("test digest must begin with a valid application identifier character: %q", digestText)
	}
	want := path.Join(InstallRoot, "application", "_"+digestText)
	if root != want {
		t.Fatalf("long runtime root = %q, want %q", root, want)
	}
	interpreter := path.Join(root, "bin", "python")
	if len(interpreter)+3 > pythonDirectShebangLimitBytes {
		t.Fatalf("bounded interpreter path is too long: %d bytes", len(interpreter)+3)
	}
	if strings.Contains(root, "long-application-name") {
		t.Fatalf("long application name leaked into runtime root: %q", root)
	}

	other, err := RuntimeRootV1("application/" + strings.Repeat("other-application-name-", 20) + "/python")
	if err != nil {
		t.Fatal(err)
	}
	if root == other {
		t.Fatal("distinct long applications share a runtime root")
	}

	// The digest namespace must not overlap a valid short application name
	// consisting of the same hexadecimal characters.
	literal := "application/" + digestText + "/python"
	literalRoot, err := RuntimeRootV1(literal)
	if err != nil {
		t.Fatal(err)
	}
	if literalRoot == root {
		t.Fatalf("literal application root collides with digest root: %q", literalRoot)
	}
}
