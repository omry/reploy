//go:build linux

package probe

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
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
