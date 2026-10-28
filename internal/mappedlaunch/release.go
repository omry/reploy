// Package mappedlaunch defines the single stdin handoff between the trusted
// host admission owner and a fresh, inhibited startup process.
package mappedlaunch

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/canonical"
)

const MaxRequest = 64 << 10
const Command = "mapped-wait-v1"

// Binding is fixed in trusted create arguments before the engine assigns an
// ID. The host binds that dynamic ID through its exact owned observation; the
// waiter does not discover identity from application output or engine access.
type Binding struct {
	Schema       string           `json:"schema"`
	Installation string           `json:"installation"`
	Operation    string           `json:"operation"`
	Generation   canonical.Digest `json:"generation"`
	Revision     string           `json:"revision"`
	Image        canonical.Digest `json:"image"`
	Role         string           `json:"role"`
	Plan         canonical.Digest `json:"plan"`
}

type Request struct {
	Binding   Binding `json:"binding"`
	Container string  `json:"container"`
}

func (b Binding) Validate() error {
	n, err := strconv.ParseUint(b.Revision, 10, 64)
	if b.Schema != "mapped-release-v1" || err != nil || n == 0 || strconv.FormatUint(n, 10) != b.Revision {
		return fmt.Errorf("invalid mapped release schema or revision")
	}
	for _, s := range []string{b.Installation, b.Operation, b.Role} {
		if s == "" || len(s) > 1024 || !utf8.ValidString(s) || strings.ContainsAny(s, "\x00\r\n") {
			return fmt.Errorf("invalid mapped release identity")
		}
	}
	for _, d := range []canonical.Digest{b.Generation, b.Image, b.Plan} {
		if err := d.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// DecodeBinding accepts only the canonical closed shape, including unique
// keys. Re-encoding also rejects duplicate keys that encoding/json accepts.
func DecodeBinding(data []byte) (Binding, error) {
	var b Binding
	if err := decode(data, &b); err != nil {
		return b, err
	}
	return b, b.Validate()
}

func decode(data []byte, value any) error {
	if len(data) == 0 || len(data) > MaxRequest || !utf8.Valid(data) {
		return fmt.Errorf("invalid mapped release JSON length or UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return fmt.Errorf("invalid mapped release JSON")
	}
	encoded, err := canonical.Marshal(value)
	if err != nil || !bytes.Equal(data, encoded) {
		return fmt.Errorf("mapped release JSON is not canonical and closed")
	}
	return nil
}

func (r Request) validate() error {
	if err := r.Binding.Validate(); err != nil {
		return err
	}
	// Container IDs use the engine's full lowercase 256-bit identity.
	_, err := canonical.ParseDigest("sha256:" + r.Container)
	return err
}

func Frame(r Request) ([]byte, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	data, err := canonical.Marshal(r)
	if err != nil || len(data) > MaxRequest {
		return nil, fmt.Errorf("mapped release exceeds the bounded request")
	}
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame, uint32(len(data)))
	copy(frame[4:], data)
	return frame, nil
}

// Read consumes exactly one frame. Its reader must be the unbuffered stdin
// descriptor: bytes beyond the declared body belong exclusively to the app.
func Read(input io.Reader, expected Binding) (Request, error) {
	var r Request
	if err := expected.Validate(); err != nil {
		return r, err
	}
	var header [4]byte
	if _, err := io.ReadFull(input, header[:]); err != nil {
		return r, fmt.Errorf("read mapped release header: %w", err)
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > MaxRequest {
		return r, fmt.Errorf("invalid mapped release length")
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(input, data); err != nil {
		return r, fmt.Errorf("read mapped release body: %w", err)
	}
	if err := decode(data, &r); err != nil {
		return r, err
	}
	if err := r.validate(); err != nil {
		return r, err
	}
	if r.Binding != expected {
		return r, fmt.Errorf("mapped release differs from trusted launch arguments")
	}
	return r, nil
}
