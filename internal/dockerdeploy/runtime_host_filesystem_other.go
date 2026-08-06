//go:build !linux && !darwin

package dockerdeploy

func protectedRuntimeHostPathV1(string) (string, error) {
	return "", nil
}

func protectedRuntimeHostFilesystemV1(string) (string, error) {
	return "", nil
}
