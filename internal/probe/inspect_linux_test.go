//go:build linux

package probe

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestInspectObservesLinksTerminalDigestOwnershipAndAccess(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	terminalPath := filepath.Join(binDir, "tool-real")
	content := []byte("probe executable\n")
	if err := os.WriteFile(terminalPath, content, 0o755); err != nil {
		t.Fatal(err)
	}
	secondLink := filepath.Join(binDir, "tool-link")
	if err := os.Symlink("tool-real", secondLink); err != nil {
		t.Fatal(err)
	}
	invocationPath := filepath.Join(dir, "tool")
	if err := os.Symlink("bin/tool-link", invocationPath); err != nil {
		t.Fatal(err)
	}
	request := RequestV1{
		Schema:      RequestSchemaV1,
		Inspections: []ExecutableInspectionV1{{ID: "tool", InvocationPath: filepath.ToSlash(invocationPath)}},
	}
	response, err := Inspect(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateResponseV1(request, response); err != nil {
		t.Fatal(err)
	}
	observation := response.Observations[0]
	if len(observation.Links) != 2 || observation.Links[0].Path != filepath.ToSlash(invocationPath) || observation.Links[1].Path != filepath.ToSlash(secondLink) {
		t.Fatalf("links = %#v", observation.Links)
	}
	wantDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	if observation.Terminal.Path != filepath.ToSlash(terminalPath) || string(observation.Terminal.SHA256) != wantDigest || observation.Terminal.Mode != "0755" || observation.Terminal.UID == "" || observation.Terminal.GID == "" {
		t.Fatalf("terminal = %#v", observation.Terminal)
	}
	if !sort.SliceIsSorted(observation.Access, func(left int, right int) bool { return observation.Access[left].Path < observation.Access[right].Path }) {
		t.Fatalf("access is not sorted: %#v", observation.Access)
	}
	foundTerminal := false
	for _, access := range observation.Access {
		if access.Path == filepath.ToSlash(terminalPath) && access.Kind == "regular" {
			foundTerminal = true
		}
	}
	if !foundTerminal {
		t.Fatalf("terminal access missing: %#v", observation.Access)
	}
	wantAccess := requiredAccessPaths(request.Inspections[0].InvocationPath, observation.Links, observation.Terminal.Path)
	if len(observation.Access) != len(wantAccess) {
		t.Fatalf("access count = %d, want %d: %#v", len(observation.Access), len(wantAccess), observation.Access)
	}
	for _, access := range observation.Access {
		if wantAccess[access.Path] != access.Kind {
			t.Fatalf("unexpected access observation: %#v", access)
		}
	}
}

func TestInspectRejectsSymbolicLinkCycles(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left")
	right := filepath.Join(dir, "right")
	if err := os.Symlink("right", left); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("left", right); err != nil {
		t.Fatal(err)
	}
	_, err := Inspect(RequestV1{
		Schema:      RequestSchemaV1,
		Inspections: []ExecutableInspectionV1{{ID: "cycle", InvocationPath: filepath.ToSlash(left)}},
	})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestSameFileProbeRequiresActualFileIdentity(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "usr", "bin")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatal(err)
	}
	terminal := filepath.Join(realDir, "bash")
	content := []byte("identical executable bytes\n")
	if err := os.WriteFile(terminal, content, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("usr/bin", filepath.Join(dir, "bin")); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(dir, "copy")
	if err := os.WriteFile(copyPath, content, 0755); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(dir, "leaf")
	if err := os.Symlink("usr/bin/bash", leaf); err != nil {
		t.Fatal(err)
	}
	hard := filepath.Join(dir, "hard")
	if err := os.Link(terminal, hard); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, consumer string
		pass           bool
	}{
		{"directory alias", filepath.Join(dir, "bin", "bash"), true},
		{"leaf alias", leaf, true},
		{"hard link", hard, true},
		{"same path", terminal, true},
		{"separate same-byte copy", copyPath, false},
		{"missing", filepath.Join(dir, "missing"), false},
		{"nonregular", realDir, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := RequestV1{Schema: RequestSchemaV1, Inspections: []ExecutableInspectionV1{
				{ID: "consumer", InvocationPath: tc.consumer}, {ID: "export", InvocationPath: terminal},
			}}
			encoded, err := canonical.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			code := Main([]string{"inspect-same-file"}, bytes.NewReader(encoded), &stdout, &stderr)
			if !tc.pass {
				if code == 0 || stdout.Len() != 0 {
					t.Fatalf("accepted distinct/invalid file: code=%d output=%s stderr=%s", code, &stdout, &stderr)
				}
				if tc.name == "separate same-byte copy" && !strings.Contains(stderr.String(), "same regular file") {
					t.Fatalf("wrong rejection: %s", &stderr)
				}
				return
			}
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("same-file failure: code=%d stderr=%s", code, &stderr)
			}
			response, err := DecodeResponseV1(request, stdout.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			for _, observation := range response.Observations {
				if string(observation.Terminal.SHA256) != fmt.Sprintf("sha256:%x", sha256.Sum256(content)) {
					t.Fatalf("wrong opened-file digest: %#v", observation.Terminal)
				}
			}
		})
	}
}

func TestSameFileProbeRejectsIncompleteAndMalformedRequests(t *testing.T) {
	for _, inspections := range [][]ExecutableInspectionV1{
		nil, {}, {{ID: "one", InvocationPath: "/one"}},
		{{ID: "a", InvocationPath: "/a"}, {ID: "b", InvocationPath: "/b"}, {ID: "c", InvocationPath: "/c"}},
		{{ID: "a", InvocationPath: "relative"}, {ID: "b", InvocationPath: "/b"}},
	} {
		request := RequestV1{Schema: RequestSchemaV1, Inspections: inspections}
		encoded, err := canonical.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := Main([]string{"inspect-same-file"}, bytes.NewReader(encoded), &stdout, &stderr); code == 0 || stdout.Len() != 0 {
			t.Fatalf("accepted incomplete request: %#v", request)
		}
	}
	for _, args := range [][]string{{"inspect-same-file", "extra"}, {"inspect-same-file", "--", "/bin/bash"}} {
		if code := Main(args, strings.NewReader("{}"), &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
			t.Fatalf("accepted extra mode arguments: %#v", args)
		}
	}
}
