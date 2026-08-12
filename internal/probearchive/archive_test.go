package probearchive

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

const appendedExecutableChild = "REPLOY_TEST_APPENDED_PROBE_EXECUTABLE"

func TestAppendAndVerifyExactProbeMatrix(t *testing.T) {
	dir := t.TempDir()
	executable := writeTestFile(t, dir, "reploy", []byte("executable-prefix\n"), 0o755)
	inputs := testHelpers(t, dir)
	inputs[0], inputs[2] = inputs[2], inputs[0]
	if err := Append(executable, testRelease(), inputs, testSessionClients(t, dir)); err != nil {
		t.Fatal(err)
	}
	manifest, err := Verify(executable)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != ManifestSchemaV1 || manifest.Release != testRelease() || len(manifest.Entries) != 3 || len(manifest.SessionClients) != 2 {
		t.Fatalf("manifest = %#v", manifest)
	}
	platforms := []string{manifest.Entries[0].Platform, manifest.Entries[1].Platform, manifest.Entries[2].Platform}
	if !reflect.DeepEqual(platforms, supportedPlatforms[:]) {
		t.Fatalf("platforms = %q, want %q", platforms, supportedPlatforms)
	}
	info, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Fatalf("executable mode = %04o", info.Mode().Perm())
	}
	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if string(content[:len("executable-prefix\n")]) != "executable-prefix\n" {
		t.Fatal("archive changed the executable prefix")
	}
}

func TestArchiveAppendedExecutableStillRuns(t *testing.T) {
	if os.Getenv(appendedExecutableChild) == "1" {
		return
	}
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	name := "reploy-archive-test"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dir := t.TempDir()
	executable := writeTestFile(t, dir, name, content, 0o755)
	if err := Append(executable, testRelease(), testHelpers(t, dir), testSessionClients(t, dir)); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestArchiveAppendedExecutableStillRuns$")
	command.Env = append(os.Environ(), appendedExecutableChild+"=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run archive-appended executable: %v\n%s", err, output)
	}
}

func TestAppendIsDeterministicAndRejectsSecondArchive(t *testing.T) {
	dir := t.TempDir()
	left := writeTestFile(t, dir, "left", []byte("same-prefix"), 0o755)
	right := writeTestFile(t, dir, "right", []byte("same-prefix"), 0o755)
	inputs := testHelpers(t, dir)
	if err := Append(left, testRelease(), inputs, testSessionClients(t, dir)); err != nil {
		t.Fatal(err)
	}
	if err := Append(right, testRelease(), inputs, testSessionClients(t, dir)); err != nil {
		t.Fatal(err)
	}
	leftContent, _ := os.ReadFile(left)
	rightContent, _ := os.ReadFile(right)
	if !reflect.DeepEqual(leftContent, rightContent) {
		t.Fatal("identical inputs produced different probe archives")
	}
	before := append([]byte{}, leftContent...)
	if err := Append(left, testRelease(), inputs, testSessionClients(t, dir)); err == nil {
		t.Fatal("second probe archive was accepted")
	}
	after, _ := os.ReadFile(left)
	if !reflect.DeepEqual(after, before) {
		t.Fatal("rejected append changed the executable")
	}
}

func TestAppendRollsBackFailedPostWriteVerification(t *testing.T) {
	dir := t.TempDir()
	executable := writeTestFile(t, dir, "reploy", []byte("original"), 0o755)
	if err := appendWithVerifier(executable, testRelease(), testHelpers(t, dir), testSessionClients(t, dir), func(string) error {
		return errors.New("injected verification failure")
	}); err == nil {
		t.Fatal("verification failure was ignored")
	}
	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "original" {
		t.Fatalf("failed append left %d bytes, want original content", len(content))
	}
}

func TestValidateReleaseAcceptsDisplayVersionDirtyMarkers(t *testing.T) {
	for _, marker := range []string{"", "0", "1", "true", "TRUE", "false", "FALSE"} {
		release := testRelease()
		release.BuildDirty = marker
		if err := ValidateReleaseV1(release); err != nil {
			t.Errorf("BuildDirty %q: %v", marker, err)
		}
	}
	release := testRelease()
	release.BuildDirty = "yes"
	if err := ValidateReleaseV1(release); err == nil {
		t.Fatal("invalid build dirty marker was accepted")
	}
}

func TestAppendRejectsIncompleteOrUnsupportedMatrixWithoutChangingExecutable(t *testing.T) {
	dir := t.TempDir()
	executable := writeTestFile(t, dir, "reploy", []byte("unchanged"), 0o755)
	inputs := testHelpers(t, dir)
	empty := writeTestFile(t, dir, "empty-probe", []byte{}, 0o755)
	for _, invalid := range [][]HelperInput{
		inputs[:2],
		{inputs[0], inputs[1], {Platform: "linux/riscv64", Path: inputs[2].Path}},
		{inputs[0], inputs[0], inputs[2]},
		{inputs[0], inputs[1], {Platform: "linux/arm64", Path: empty}},
	} {
		if err := Append(executable, testRelease(), invalid, testSessionClients(t, dir)); err == nil {
			t.Fatalf("invalid inputs accepted: %#v", invalid)
		}
		content, err := os.ReadFile(executable)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != "unchanged" {
			t.Fatal("rejected inputs changed the executable")
		}
	}
}

func TestAppendRejectsIncompleteOrUnsupportedSessionClientsWithoutChangingExecutable(t *testing.T) {
	dir := t.TempDir()
	executable := writeTestFile(t, dir, "reploy", []byte("unchanged"), 0o755)
	clients := testSessionClients(t, dir)
	empty := writeTestFile(t, dir, "empty-session-client", []byte{}, 0o755)
	for _, invalid := range [][]SessionClientInput{
		clients[:1],
		{clients[0], {Platform: "linux/riscv64", Path: clients[1].Path}},
		{clients[0], clients[0]},
		{clients[0], {Platform: "linux/arm64", Path: empty}},
	} {
		if err := Append(executable, testRelease(), testHelpers(t, dir), invalid); err == nil {
			t.Fatalf("invalid session clients accepted: %#v", invalid)
		}
		content, err := os.ReadFile(executable)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != "unchanged" {
			t.Fatal("rejected session clients changed the executable")
		}
	}
}

func TestVerifyRejectsMissingAndCorruptedArchive(t *testing.T) {
	dir := t.TempDir()
	plain := writeTestFile(t, dir, "plain", []byte("not-an-archive"), 0o755)
	if _, err := Verify(plain); !errors.Is(err, ErrNotEmbedded) {
		t.Fatalf("missing archive error = %v", err)
	}
	executable := writeTestFile(t, dir, "reploy", []byte("prefix"), 0o755)
	if err := Append(executable, testRelease(), testHelpers(t, dir), testSessionClients(t, dir)); err != nil {
		t.Fatal(err)
	}
	archive, err := open(executable)
	if err != nil {
		t.Fatal(err)
	}
	offset, err := archive.entries[helperArchivePath("linux/amd64")].DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(executable, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{0xff}, offset); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(executable); err == nil {
		t.Fatal("corrupted helper archive was accepted")
	}
}

func testHelpers(t *testing.T, dir string) []HelperInput {
	t.Helper()
	return []HelperInput{
		{Platform: "linux/amd64", Path: writeTestFile(t, dir, "probe-amd64", []byte("amd64 helper bytes"), 0o755)},
		{Platform: "linux/arm/v7", Path: writeTestFile(t, dir, "probe-arm-v7", []byte("arm v7 helper bytes"), 0o755)},
		{Platform: "linux/arm64", Path: writeTestFile(t, dir, "probe-arm64", []byte("arm64 helper bytes"), 0o755)},
	}
}

func testSessionClients(t *testing.T, dir string) []SessionClientInput {
	t.Helper()
	return []SessionClientInput{
		{Platform: "linux/amd64", Path: writeTestFile(t, dir, "session-client-amd64", []byte("amd64 session client bytes"), 0o755)},
		{Platform: "linux/arm64", Path: writeTestFile(t, dir, "session-client-arm64", []byte("arm64 session client bytes"), 0o755)},
	}
}

func testRelease() ReleaseV1 {
	return ReleaseV1{Version: "1.2.3", BuildCommit: "abcdef0123", BuildDirty: "false", BuildTimestamp: "2026-08-13_00:00:00_UTC"}
}

func writeTestFile(t *testing.T, dir string, name string, content []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
	return path
}
