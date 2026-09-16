package python

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
)

func TestWheelConsoleScriptTargetSyntaxV1(t *testing.T) {
	for _, value := range []string{"demo.cli:main", "demo.cli  :  main [ one, two ]", "demo:main []", "δοκιμή:κύριο", "demo:méthod", "demo:Class.method"} {
		if err := ValidateWheelConsoleScriptTargetV1(value); err != nil {
			t.Errorf("valid target %q: %v", value, err)
		}
	}
	for _, value := range []string{"demo", "demo:", ":main", "demo:bad()", "demo:1main", "demo:main:extra", "demo:main [", "demo:main [bad,,extra]", "demo:main [extra] suffix", "demo:ⓐ", "demo:bad name", "demo.:main"} {
		if err := ValidateWheelConsoleScriptTargetV1(value); err == nil {
			t.Errorf("accepted invalid target %q", value)
		}
	}
}

func TestInspectWheelReaderV1ChecksDescriptorBeforeReading(t *testing.T) {
	filename := "demo-1-py3-none-any.whl"
	data := testInspectionWheel(t, filename, "demo", "1", "malformed metadata", "", "")
	descriptor := testInspectionDescriptor(data, filename)
	for _, tc := range []struct {
		name   string
		mutate func(*providerstore.ArtifactDescriptor)
	}{
		{"kind", func(d *providerstore.ArtifactDescriptor) { d.Kind = "sdist" }},
		{"filename", func(d *providerstore.ArtifactDescriptor) { d.LogicalPath = "wheels/other-1-py3-none-any.whl" }},
		{"size", func(d *providerstore.ArtifactDescriptor) { d.Size = "1" }},
		{"noncanonical size", func(d *providerstore.ArtifactDescriptor) { d.Size = "01" }},
		{"invalid digest", func(d *providerstore.ArtifactDescriptor) { d.SHA256 = canonical.Digest("bad") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := descriptor
			tc.mutate(&d)
			reader := &inspectionReadCounter{Reader: bytes.NewReader(data)}
			if _, err := InspectWheelReaderV1(context.Background(), reader, int64(len(data)), filename, d); err == nil {
				t.Fatal("accepted invalid descriptor")
			}
			if reader.reads != 0 {
				t.Fatal("read archive before rejecting descriptor")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := &inspectionReadCounter{Reader: bytes.NewReader(data)}
	if _, err := InspectWheelReaderV1(ctx, reader, int64(len(data)), filename, descriptor); err == nil || reader.reads != 0 {
		t.Fatalf("canceled inspection read archive: %v, reads %d", err, reader.reads)
	}
}

type inspectionReadCounter struct {
	*bytes.Reader
	reads int
}

func (r *inspectionReadCounter) ReadAt(p []byte, offset int64) (int, error) {
	r.reads++
	return r.Reader.ReadAt(p, offset)
}

func TestInspectWheelReaderV1DrainsMetadataAndChecksCRC(t *testing.T) {
	filename := "demo-1-py3-none-any.whl"
	data := testInspectionWheel(t, filename, "demo", "1", "Name: demo\nVersion: 1\n\nignored body", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n", "")
	offset := bytes.Index(data, []byte{'P', 'K', 1, 2})
	if offset < 0 {
		t.Fatal("missing central header")
	}
	binary.LittleEndian.PutUint32(data[offset+16:], 0)
	_, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(data), int64(len(data)), filename, testInspectionDescriptor(data, filename))
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("metadata checksum: %v", err)
	}
}

func TestInspectWheelReaderV1BoundsUnknownEntryPointSections(t *testing.T) {
	filename := "demo-1-py3-none-any.whl"
	entries := padInspectionMember("[console_scripts]\ncli = demo : main [ extra ]\n[unknown]\nignored = ", 1<<20)
	data := testInspectionWheel(t, filename, "demo", "1", "Name: demo\nVersion: 1\n", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n", entries)
	result, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(data), int64(len(data)), filename, testInspectionDescriptor(data, filename))
	if err != nil || len(result.ConsoleScripts) != 1 || result.ConsoleScripts[0].Target != "demo : main [ extra ]" {
		t.Fatalf("entry points: %#v %v", result.ConsoleScripts, err)
	}
}

func TestInspectWheelReaderV1RequiresCanonicalCoreIdentityV1(t *testing.T) {
	filename := "demo-1-py3-none-any.whl"
	wheel := "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n"
	for _, metadata := range []string{"Name: other\nVersion: 1\n", "Name: demo\nVersion: 2\n", "Name: bad/name\nVersion: 1\n", "Name: demo\nVersion: nonsense\n"} {
		data := testInspectionWheel(t, filename, "demo", "1", metadata, wheel, "")
		if _, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(data), int64(len(data)), filename, testInspectionDescriptor(data, filename)); err == nil {
			t.Fatalf("accepted mismatched core metadata %q", metadata)
		}
	}
	data := testInspectionWheel(t, filename, "Demo", "1", "Name: Demo\nVersion: 01\n", wheel, "")
	got, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(data), int64(len(data)), filename, testInspectionDescriptor(data, filename))
	if err != nil || got.Distribution != "demo" || got.Version != "1" {
		t.Fatalf("normalized identity = %#v, %v", got, err)
	}
}

func TestInspectWheelReaderV1RejectsCompressedInternalTagsV1(t *testing.T) {
	filename := "demo-1-py2.py3-none-any.whl"
	data := testInspectionWheel(t, filename, "demo", "1", "Name: demo\nVersion: 1\n", "Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py2.py3-none-any\n", "")
	if _, err := InspectWheelReaderV1(context.Background(), bytes.NewReader(data), int64(len(data)), filename, testInspectionDescriptor(data, filename)); err == nil || !strings.Contains(err.Error(), "tag") {
		t.Fatalf("compressed internal tag: %v", err)
	}
}
