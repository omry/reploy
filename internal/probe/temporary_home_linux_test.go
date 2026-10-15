//go:build linux

package probe

import (
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestApplicationHomeMountValidationRejectsSubstitutes(t *testing.T) {
	stat := unix.Stat_t{Mode: unix.S_IFDIR | 0o700}
	fs := unix.Statfs_t{Type: unix.TMPFS_MAGIC, Bsize: 4096, Blocks: 16384, Flags: unix.ST_NOEXEC | unix.ST_NOSUID | unix.ST_NODEV}
	data := "78 12 0:91 / /mnt/reploy-home rw,nosuid,nodev,noexec - tmpfs tmpfs rw,size=65536k,mode=700\n"
	if err := validateApplicationHomeMountV1(data, 78, stat, fs); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		data string
		stat unix.Stat_t
		fs   unix.Statfs_t
	}{
		{"not a mount", "", stat, fs},
		{"wrong mount ID", strings.Replace(data, "78 ", "79 ", 1), stat, fs},
		{"other directory", strings.Replace(data, "/mnt/reploy-home", "/data", 1), stat, fs},
		{"bind of subtree", strings.Replace(data, " / /mnt", " /subdir /mnt", 1), stat, fs},
		{"other filesystem", strings.Replace(data, "- tmpfs", "- ext4", 1), stat, fs},
		{"symlink", data, unix.Stat_t{Mode: unix.S_IFLNK | 0o700}, fs},
		{"public mode", data, unix.Stat_t{Mode: unix.S_IFDIR | 0o777}, fs},
		{"special mode", data, unix.Stat_t{Mode: unix.S_IFDIR | 0o4700}, fs},
		{"disk filesystem", data, stat, unix.Statfs_t{Type: unix.EXT4_SUPER_MAGIC, Bsize: 4096, Blocks: 16384, Flags: fs.Flags}},
		{"executable", data, stat, unix.Statfs_t{Type: fs.Type, Bsize: fs.Bsize, Blocks: fs.Blocks, Flags: fs.Flags &^ unix.ST_NOEXEC}},
		{"setuid", data, stat, unix.Statfs_t{Type: fs.Type, Bsize: fs.Bsize, Blocks: fs.Blocks, Flags: fs.Flags &^ unix.ST_NOSUID}},
		{"devices", data, stat, unix.Statfs_t{Type: fs.Type, Bsize: fs.Bsize, Blocks: fs.Blocks, Flags: fs.Flags &^ unix.ST_NODEV}},
		{"read only", data, stat, unix.Statfs_t{Type: fs.Type, Bsize: fs.Bsize, Blocks: fs.Blocks, Flags: fs.Flags | unix.ST_RDONLY}},
		{"wrong size", data, stat, unix.Statfs_t{Type: fs.Type, Bsize: fs.Bsize, Blocks: 32768, Flags: fs.Flags}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateApplicationHomeMountV1(test.data, 78, test.stat, test.fs); err == nil {
				t.Fatal("accepted unsafe private home")
			}
		})
	}
}

func TestTemporaryHomePreparationIsExplicitAndBeforeApplication(t *testing.T) {
	plan, err := parseSandboxExecPlanV1([]string{"--uid", "1000", "--gid", "1000", "--prepare-temporary-home", "--", "/bin/echo", "--prepare-temporary-home"}, false)
	if err != nil || !plan.PrepareTemporaryHome || len(plan.Argv) != 2 {
		t.Fatalf("explicit helper preparation: %#v, %v", plan, err)
	}
	plan, err = parseSandboxExecPlanV1([]string{"--uid", "1000", "--gid", "1000", "--", "/bin/echo", "--prepare-temporary-home"}, false)
	if err != nil || plan.PrepareTemporaryHome {
		t.Fatalf("application argument enabled trusted preparation: %#v, %v", plan, err)
	}
}
