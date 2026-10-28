//go:build !linux

package probe

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/mappedlaunch"
)

func TestMappedWaitRequiresLinuxV1(t *testing.T) {
	var output bytes.Buffer
	if status := Main([]string{mappedlaunch.Command}, nil, &output, &output); status != 1 || !strings.Contains(output.String(), "Linux") {
		t.Fatalf("unsupported waiter: %d %s", status, output.String())
	}
}
