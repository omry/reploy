//go:build !linux

package dockerdeploy

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/identitymapping"
)

func TestMappedProcessPinRequiresLinuxV1(t *testing.T) {
	if p, err := pinMappedProcessV1(1, identitymapping.Geometry{}, nil); p != nil || err == nil || !strings.Contains(err.Error(), "Linux") {
		t.Fatalf("unsupported process observer: %v %v", p, err)
	}
}
