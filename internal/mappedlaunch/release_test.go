package mappedlaunch

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func bindingFixture() Binding {
	d := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	return Binding{"mapped-release-v1", "installation", "operation", d, "12", d, "app", d}
}

func rawFrame(data []byte) []byte {
	frame := make([]byte, 4)
	binary.BigEndian.PutUint32(frame, uint32(len(data)))
	return append(frame, data...)
}

type fragmentedReader struct{ io.Reader }

func (r fragmentedReader) Read(b []byte) (int, error) {
	return r.Reader.Read(b[:min(len(b), 1)])
}

func TestReleaseFramePreservesEveryApplicationByte(t *testing.T) {
	b := bindingFixture()
	r := Request{b, strings.Repeat("b", 64)}
	frame, err := Frame(r)
	if err != nil {
		t.Fatal(err)
	}
	// A duplicate frame after the accepted frame is application input, with no
	// remaining parser. Include NULs and non-UTF8 bytes in the app canary.
	canary := append([]byte{0, 255, '\n'}, frame...)
	for _, fragmented := range []bool{false, true} {
		input := bytes.NewReader(append(slicesClone(frame), canary...))
		var reader io.Reader = input
		if fragmented {
			reader = fragmentedReader{input}
		}
		got, err := Read(reader, b)
		if err != nil || got != r {
			t.Fatalf("read: %+v %v", got, err)
		}
		remaining, err := io.ReadAll(reader)
		if err != nil || !bytes.Equal(remaining, canary) {
			t.Fatalf("stdin consumed: %x %v", remaining, err)
		}
	}
	for n := range len(frame) {
		if _, err := Read(bytes.NewReader(frame[:n]), b); err == nil {
			t.Fatalf("partial frame length %d accepted", n)
		}
	}
}

func slicesClone(b []byte) []byte { return append([]byte(nil), b...) }

func TestReleaseRejectsClosedShapeAndBindingAttacks(t *testing.T) {
	b := bindingFixture()
	valid, _ := canonical.Marshal(Request{b, strings.Repeat("b", 64)})
	for name, data := range map[string][]byte{
		"duplicate outer":  bytes.Replace(valid, []byte(`"container":`), []byte(`"container":"`+strings.Repeat("b", 64)+`","container":`), 1),
		"duplicate nested": bytes.Replace(valid, []byte(`"role":"app"`), []byte(`"role":"app","role":"app"`), 1),
		"unknown":          append([]byte(`{"unknown":true,`), valid[1:]...),
		"unknown nested":   bytes.Replace(valid, []byte(`"binding":{`), []byte(`"binding":{"unknown":"x",`), 1),
		"trailer":          append(slicesClone(valid), []byte(`{}`)...),
		"space":            append(slicesClone(valid), ' '),
		"null":             []byte(`null`),
		"array":            []byte(`[]`),
		"empty":            nil,
		"invalid JSON":     []byte(`{`),
		"invalid UTF8":     {255},
		"bad container":    bytes.Replace(valid, []byte(strings.Repeat("b", 64)), []byte(strings.Repeat("B", 64)), 1),
		"escaped alias":    bytes.Replace(valid, []byte(`"app"`), []byte(`"\u0061pp"`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Read(bytes.NewReader(rawFrame(data)), b); err == nil {
				t.Fatal("malformed request accepted")
			}
		})
	}
	for _, n := range []uint32{0, MaxRequest + 1, ^uint32(0)} {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], n)
		if _, err := Read(bytes.NewReader(header[:]), b); err == nil {
			t.Fatalf("length %d accepted", n)
		}
	}
	for _, field := range []string{"schema", "installation", "operation", "generation", "revision", "image", "role", "plan"} {
		t.Run("mismatch "+field, func(t *testing.T) {
			other := b
			switch field {
			case "schema":
				other.Schema = "v2"
			case "installation":
				other.Installation = "other"
			case "operation":
				other.Operation = "other"
			case "generation":
				other.Generation = canonical.Digest("sha256:" + strings.Repeat("c", 64))
			case "revision":
				other.Revision = "13"
			case "image":
				other.Image = canonical.Digest("sha256:" + strings.Repeat("c", 64))
			case "role":
				other.Role = "other"
			case "plan":
				other.Plan = canonical.Digest("sha256:" + strings.Repeat("c", 64))
			}
			if _, err := Read(bytes.NewReader(rawFrame(valid)), other); err == nil {
				t.Fatal("binding mismatch accepted")
			}
		})
	}
}

func TestBindingValidationAndLaunchArguments(t *testing.T) {
	valid := bindingFixture()
	encoded, _ := canonical.Marshal(valid)
	if got, err := DecodeBinding(encoded); err != nil || got != valid {
		t.Fatalf("binding roundtrip: %+v %v", got, err)
	}
	for _, data := range [][]byte{nil, []byte(`{}`), []byte(`null`), []byte(strings.Repeat("x", MaxRequest+1)), append(slicesClone(encoded), ' ')} {
		if _, err := DecodeBinding(data); err == nil {
			t.Fatal("invalid binding accepted")
		}
	}
	for _, revision := range []string{"", "0", "01", "-1", "18446744073709551616"} {
		b := valid
		b.Revision = revision
		if b.Validate() == nil {
			t.Fatalf("revision %q accepted", revision)
		}
	}
	for _, value := range []string{"", strings.Repeat("x", 1025), "a\x00b", "a\nb", "a\rb", string([]byte{255})} {
		for field := range 3 {
			b := valid
			switch field {
			case 0:
				b.Installation = value
			case 1:
				b.Operation = value
			case 2:
				b.Role = value
			}
			if b.Validate() == nil {
				t.Fatalf("identity %q accepted", value)
			}
		}
	}
	for field := range 3 {
		b := valid
		switch field {
		case 0:
			b.Generation = "bad"
		case 1:
			b.Image = "bad"
		case 2:
			b.Plan = "bad"
		}
		if _, err := Frame(Request{b, strings.Repeat("b", 64)}); err == nil {
			t.Fatal("bad digest framed")
		}
	}
	if _, err := Frame(Request{valid, "short"}); err == nil {
		t.Fatal("short ID framed")
	}
}
