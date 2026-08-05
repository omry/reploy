//go:build linux

package dockerdeploy

import (
	"os"
	"syscall"
	"testing"
)

func TestProtectedRuntimeHostFilesystemV1RecognizesKernelFilesystems(t *testing.T) {
	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/proc", want: "procfs"},
		{path: "/sys", want: "sysfs"},
		{path: "/dev/pts", want: "devpts"},
	} {
		t.Run(test.want, func(t *testing.T) {
			if _, err := os.Stat(test.path); os.IsNotExist(err) {
				t.Skipf("host path %q is absent", test.path)
			} else if err != nil {
				t.Fatal(err)
			}
			got, err := protectedRuntimeHostFilesystemV1(test.path)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("filesystem for %q = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestProtectedRuntimeHostFilesystemV1DoesNotRejectOrdinaryTmpfs(t *testing.T) {
	for _, candidate := range []string{"/run", "/tmp"} {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		kind, err := runtimeHostMountFilesystemV1(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if kind != "tmpfs" {
			continue
		}
		protected, err := protectedRuntimeHostFilesystemV1(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if protected != "" {
			t.Fatalf("ordinary tmpfs %q classified as %q", candidate, protected)
		}
		return
	}
	t.Skip("host has no ordinary tmpfs candidate")
}

func TestRuntimeHostSharesDedicatedDevFilesystemV1(t *testing.T) {
	dev, err := runtimeHostSharesDedicatedDevFilesystemV1("/dev")
	if err != nil {
		t.Fatal(err)
	}
	var devInfo, rootInfo syscall.Stat_t
	if err := syscall.Stat("/dev", &devInfo); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Stat("/", &rootInfo); err != nil {
		t.Fatal(err)
	}
	if devInfo.Dev == rootInfo.Dev {
		if dev {
			t.Fatal("non-dedicated /dev filesystem classified as dedicated")
		}
		return
	}
	if !dev {
		t.Fatal("dedicated /dev filesystem was not recognized")
	}
}

func TestRuntimeHostMountFilesystemV1ParsesIdentity(t *testing.T) {
	data := []byte("41 1 0:1 / / rw - ext4 /dev/root rw\n" +
		"42 41 0:2 / /safe\\040tree rw - devtmpfs udev rw\n" +
		"43 42 0:3 / /safe\\040tree/nested rw - proc proc rw\n")

	kind, found, err := runtimeHostMountFilesystemByIDV1(data, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !found || kind != "devtmpfs" {
		t.Fatalf("mount ID lookup = %q, %t", kind, found)
	}
}
