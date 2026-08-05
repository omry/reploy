//go:build darwin

package dockerdeploy

import "golang.org/x/sys/unix"

func protectedRuntimeHostFilesystemV1(path string) (string, error) {
	var filesystem unix.Statfs_t
	if err := unix.Statfs(path, &filesystem); err != nil {
		return "", err
	}
	kind := darwinFilesystemNameV1(filesystem.Fstypename[:])
	switch kind {
	case "devfs", "procfs":
		return kind, nil
	default:
		return "", nil
	}
}

func darwinFilesystemNameV1(value []byte) string {
	for index, item := range value {
		if item == 0 {
			return string(value[:index])
		}
	}
	return string(value)
}
