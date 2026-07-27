//go:build !linux

package probe

import "fmt"

func Inspect(RequestV1) (ResponseV1, error) {
	return ResponseV1{}, fmt.Errorf("reploy-probe supports Linux containers only")
}
