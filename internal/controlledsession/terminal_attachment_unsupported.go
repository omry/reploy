//go:build !linux

package controlledsession

import (
	"context"
	"fmt"
)

func RunTerminalAttachmentV1(context.Context, TerminalAttachmentOptionsV1) error {
	return fmt.Errorf("controlled-session terminal attachment requires Linux")
}
