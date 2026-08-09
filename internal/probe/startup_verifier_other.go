//go:build !linux

package probe

import "fmt"

func applicationKernelStatusFallbackPathV1() (string, error) {
	return "", fmt.Errorf("thread-local kernel status compatibility path is unsupported")
}
