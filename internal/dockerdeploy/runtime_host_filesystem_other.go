//go:build !linux && !darwin

package dockerdeploy

func protectedRuntimeHostFilesystemV1(string) (string, error) {
	return "", nil
}
