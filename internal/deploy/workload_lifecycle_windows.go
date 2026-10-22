//go:build windows

package deploy

import (
	"fmt"
	"io"
	"os"
)

func readWorkloadLifecycleFileV1(path string) ([]byte, bool, error) {
	before, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !before.Mode().IsRegular() {
		return nil, false, fmt.Errorf("workload lifecycle must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	if !os.SameFile(before, after) || !after.Mode().IsRegular() {
		return nil, false, fmt.Errorf("workload lifecycle changed while opening")
	}
	content, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil {
		return nil, false, err
	}
	if len(content) > 8192 {
		return nil, false, fmt.Errorf("workload lifecycle exceeds its size limit")
	}
	return content, true, nil
}
