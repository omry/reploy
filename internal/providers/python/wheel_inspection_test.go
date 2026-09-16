package python

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/wheelinventory"
)

func TestOrdinaryWheelInspectionPreservesMixedCaseFilename(t *testing.T) {
	filename := "PyYAML-6.0.2-py3-none-any.whl"
	content := testInspectionWheel(t, filename, "PyYAML", "6.0.2", "Metadata-Version: 2.1\nName: PyYAML\nVersion: 6.0.2\nRequires-Dist: requests\n", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n", "")
	descriptor := testInspectionDescriptor(content, filename)
	observed, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(content), int64(len(content)), filename, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Distribution != "pyyaml" || observed.Version != "6.0.2" || observed.Filename != filename || observed.Artifact != descriptor || !reflect.DeepEqual(observed.FilenameTags, []string{"py3-none-any"}) {
		t.Fatalf("inspection = %#v", observed)
	}
	// Normalizing identity must not normalize the descriptor's exact filename.
	wrongDescriptor := descriptor
	wrongDescriptor.LogicalPath = strings.ReplaceAll(descriptor.LogicalPath, "PyYAML", "pyyaml")
	if _, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(content), int64(len(content)), filename, wrongDescriptor); err == nil {
		t.Fatal("accepted a descriptor with a different filename")
	}
	if _, err := portabletool.ProjectWheelFilenameV1(filename); err == nil {
		t.Fatal("portable catalog accepted a noncanonical filename")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, filename)
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatal(err)
	}
	described, metadata, err := DescribeSourceWheelFileV1(file, descriptor.LogicalPath)
	if err != nil || described != descriptor || metadata.Distribution != "pyyaml" {
		t.Fatalf("source description = %#v, %#v, %v", described, metadata, err)
	}
	if metadata, err := InspectSourceWheelFileV1(file, descriptor); err != nil || metadata.Distribution != "pyyaml" {
		t.Fatalf("source inspection = %#v, %v", metadata, err)
	}
	if distributions, err := InspectPreparedWheelDistributionsV1(context.Background(), dir); err != nil || !reflect.DeepEqual(distributions, []string{"pyyaml"}) {
		t.Fatalf("prepared distributions = %#v, %v", distributions, err)
	}
	if wheel, err := inspectWheel(context.Background(), file); err != nil || wheel.Filename != filename || wheel.Distribution != "pyyaml" {
		t.Fatalf("prepared wheel = %#v, %v", wheel, err)
	}
	if dependencies, err := InspectWheelDeclaredDependenciesV1(file, []string{"requests"}); err != nil || !reflect.DeepEqual(dependencies, []string{"requests"}) {
		t.Fatalf("path dependencies = %#v, %v", dependencies, err)
	}
	if dependencies, err := InspectWheelDeclaredDependenciesReaderV1(bytes.NewReader(content), int64(len(content)), descriptor, []string{"requests"}); err != nil || !reflect.DeepEqual(dependencies, []string{"requests"}) {
		t.Fatalf("reader dependencies = %#v, %v", dependencies, err)
	}
}

func TestInspectWheelReaderV1ReturnsCanonicalMetadataInventoryAndScripts(t *testing.T) {
	filename := "demo-1-py2.py3-none-any.whl"
	content := testInspectionWheel(t, filename, "demo", "1", "Metadata-Version: 2.1\nName: demo\nVersion: 1\nRequires-Python: >= 3.11, < 3.13\nRequires-Dist: requests >= 2\nRequires-Dist: zlib\n", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py2-none-any\nTag: py3-none-any\n", "[console_scripts]\nz = demo.cli:main\na = demo.cli:run\n[ignored]\nnope = bad\n")
	got, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(content), int64(len(content)), filename, testInspectionDescriptor(content, filename))
	if err != nil {
		t.Fatal(err)
	}
	if got.Distribution != "demo" || got.Version != "1" || got.RequiresPython != ">=3.11,<3.13" || got.Filename != filename || !got.RootIsPurelib {
		t.Fatalf("metadata = %#v", got)
	}
	if !reflect.DeepEqual(got.FilenameTags, []string{"py2-none-any", "py3-none-any"}) || !reflect.DeepEqual(got.InternalTags, []string{"py2-none-any", "py3-none-any"}) {
		t.Fatalf("tags = %#v / %#v", got.FilenameTags, got.InternalTags)
	}
	if !reflect.DeepEqual(got.ConsoleScripts, []WheelConsoleScriptV1{{Name: "a", Target: "demo.cli:run"}, {Name: "z", Target: "demo.cli:main"}}) {
		t.Fatalf("scripts = %#v", got.ConsoleScripts)
	}
	if len(got.Inventory) != 3 || got.Inventory[0].Kind != wheelinventory.Regular {
		t.Fatalf("inventory = %#v", got.Inventory)
	}
	if !reflect.DeepEqual(got.DeclaredDependencies, []string{"requests", "zlib"}) {
		t.Fatalf("dependencies = %#v", got.DeclaredDependencies)
	}
}

func TestInspectWheelReaderV1AcceptsFalsePurelibAndBoundsWheelMembers(t *testing.T) {
	filename := "demo-1-py3-none-any.whl"
	baseMetadata := "Metadata-Version: 2.1\nName: demo\nVersion: 1\n"
	for _, tc := range []struct {
		name, wheel, entries string
		wantErr              bool
	}{
		{"false purelib", "Wheel-Version: 1.0\nRoot-Is-Purelib: false\nTag: py3-none-any\n", "", false},
		{"wheel exact bound", padInspectionMember("Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\nX-Padding: ", 1<<20), "", false},
		{"wheel oversize", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n" + strings.Repeat("x", 1<<20), "", true},
		{"entry points oversize", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n", "[console_scripts]\na = demo:main\n" + strings.Repeat("x", 1<<20), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := testInspectionWheel(t, filename, "demo", "1", baseMetadata, tc.wheel, tc.entries)
			_, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(content), int64(len(content)), filename, testInspectionDescriptor(content, filename))
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestInspectWheelReaderV1DescriptorMismatchPrecedesMalformedMetadata(t *testing.T) {
	filename := "demo-1-py3-none-any.whl"
	content := testInspectionWheel(t, filename, "demo", "1", "Name: demo\n", "Wheel-Version: 1.0\n", "")
	descriptor := testInspectionDescriptor(content, filename)
	descriptor.SHA256 = canonical.Digest("sha256:" + strings.Repeat("0", 64))
	if _, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(content), int64(len(content)), filename, descriptor); err == nil || !strings.Contains(strings.ToLower(err.Error()), "digest") {
		t.Fatal("expected descriptor mismatch")
	}
}

func TestInspectWheelReaderV1SelectedMemberBound(t *testing.T) {
	filename := "demo-1-py3-none-any.whl"
	for _, tc := range []struct {
		name    string
		n       int
		wantErr bool
	}{{"exact", 1 << 20, false}, {"oversize", 1<<20 + 1, true}} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := "Metadata-Version: 2.1\nName: demo\nVersion: 1\nX-Padding: "
			metadata := prefix + strings.Repeat("x", tc.n-len(prefix)-1) + "\n"
			content := testInspectionWheel(t, filename, "demo", "1", metadata, "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n", "")
			_, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(content), int64(len(content)), filename, testInspectionDescriptor(content, filename))
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestInspectWheelReaderV1RejectsDuplicateSingletonFieldsAndInvalidIdentity(t *testing.T) {
	filename := "demo-1-py3-none-any.whl"
	for _, tc := range []struct{ name, metadata, wheel string }{
		{"duplicate name", "Metadata-Version: 2.1\nName: demo\nName: demo\nVersion: 1\n", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n"},
		{"duplicate version", "Metadata-Version: 2.1\nName: demo\nVersion: 1\nVersion: 1\n", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n"},
		{"duplicate requires", "Metadata-Version: 2.1\nName: demo\nVersion: 1\nRequires-Python: >=3.11\nRequires-Python: >=3.12\n", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n"},
		{"duplicate wheel version", "Metadata-Version: 2.1\nName: demo\nVersion: 1\n", "Wheel-Version: 1.0\nWheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n"},
		{"duplicate purelib", "Metadata-Version: 2.1\nName: demo\nVersion: 1\n", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nRoot-Is-Purelib: false\nTag: py3-none-any\n"},
		{"missing root identity", "Metadata-Version: 2.1\nName: demo\nVersion: 1\n", "Wheel-Version: 1.0\nTag: py3-none-any\n"},
		{"ambiguous roots", "Metadata-Version: 2.1\nName: demo\nVersion: 1\n", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := filename
			if tc.name == "ambiguous roots" {
				name = "demo-1-py3-none-any.whl"
			}
			content := testInspectionWheel(t, name, "demo", "1", tc.metadata, tc.wheel, "", tc.name == "ambiguous roots")
			if _, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(content), int64(len(content)), name, testInspectionDescriptor(content, name)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestInspectWheelReaderV1RejectsFilenameAndFormatViolations(t *testing.T) {
	for _, tc := range []struct{ name, filename, dist, version, wheel string }{
		{"filename core mismatch", "other-1-py3-none-any.whl", "demo", "1", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n"},
		{"unsupported wheel version", "demo-1-py3-none-any.whl", "demo", "1", "Wheel-Version: 1.1\nRoot-Is-Purelib: true\nTag: py3-none-any\n"},
		{"invalid purelib", "demo-1-py3-none-any.whl", "demo", "1", "Wheel-Version: 1.0\nRoot-Is-Purelib: yes\nTag: py3-none-any\n"},
		{"invalid internal tag", "demo-1-py3-none-any.whl", "demo", "1", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-!bad-any\n"},
		{"duplicate internal tag", "demo-1-py3-none-any.whl", "demo", "1", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\nTag: py3-none-any\n"},
		{"malformed requires python", "demo-1-py3-none-any.whl", "demo", "1", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := "Metadata-Version: 2.1\nName: " + tc.dist + "\nVersion: " + tc.version + "\n"
			if tc.name == "malformed requires python" {
				metadata += "Requires-Python: definitely not a specifier\n"
			}
			content := testInspectionWheel(t, tc.filename, tc.dist, tc.version, metadata, tc.wheel, "")
			if _, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(content), int64(len(content)), tc.filename, testInspectionDescriptor(content, tc.filename)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestInspectWheelReaderV1RequiresFieldsAndEntryPoints(t *testing.T) {
	filename := "demo-1-py3-none-any.whl"
	for _, tc := range []struct{ name, metadata, entries string }{
		{"missing metadata", "", ""}, {"missing wheel", "Metadata-Version: 2.1\nName: demo\nVersion: 1\n", ""},
		{"empty requires python", "Metadata-Version: 2.1\nName: demo\nVersion: 1\nRequires-Python: \n", ""},
		{"bad script target", "Metadata-Version: 2.1\nName: demo\nVersion: 1\n", "[console_scripts]\na = demo\n"},
		{"duplicate script", "Metadata-Version: 2.1\nName: demo\nVersion: 1\n", "[console_scripts]\na = demo:main\na = demo:other\n"},
		{"duplicate section", "Metadata-Version: 2.1\nName: demo\nVersion: 1\n", "[console_scripts]\na = demo:main\n[console_scripts]\nb = demo:other\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wheel := "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n"
			content := testInspectionWheel(t, filename, "demo", "1", tc.metadata, wheel, tc.entries, tc.name == "missing metadata", tc.name == "missing wheel")
			if _, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(content), int64(len(content)), filename, testInspectionDescriptor(content, filename)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func padInspectionMember(prefix string, size int) string {
	return prefix + strings.Repeat("x", size-len(prefix)-1) + "\n"
}

func testInspectionDescriptor(content []byte, filename string) providerstore.ArtifactDescriptor {
	digest := sha256.Sum256(content)
	return providerstore.ArtifactDescriptor{LogicalPath: "wheels/" + filename, Kind: "wheel", Size: fmt.Sprint(len(content)), SHA256: canonical.Digest(fmt.Sprintf("sha256:%x", digest))}
}

func testInspectionWheel(t *testing.T, filename, dist, version, metadata, wheel, entries string, flags ...bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	root := strings.ReplaceAll(dist, "-", "_") + "-" + version + ".dist-info/"
	omitMetadata, omitWheel := false, false
	ambiguous := len(flags) == 1 && flags[0]
	if len(flags) == 2 {
		omitMetadata, omitWheel = flags[0], flags[1]
	}
	if ambiguous {
		root = "demo-1.dist-info/"
		writeInspectionZip(t, zw, "other-1.dist-info/METADATA", "Name: demo\nVersion: 1\n")
	}
	if metadata != "" && !omitMetadata {
		writeInspectionZip(t, zw, root+"METADATA", metadata)
	}
	if wheel != "" && !omitWheel {
		writeInspectionZip(t, zw, root+"WHEEL", wheel)
	}
	if entries != "" {
		writeInspectionZip(t, zw, root+"entry_points.txt", entries)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeInspectionZip(t *testing.T, zw *zip.Writer, name, value string) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, value); err != nil {
		t.Fatal(err)
	}
}
