package deploy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestWorkloadLifecycleDurablePrivateEvidenceV1(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	record := WorkloadLifecycleV1{Schema: "workload-lifecycle-v1", State: "starting", Endpoint: strings.Repeat("a", 64), Runtime: strings.Repeat("b", 64), ContainerID: strings.Repeat("c", 64), StartedAt: "2026-10-22T00:00:00Z", TransferClaimed: true}
	if _, found, err := lock.ReadWorkloadLifecycleV1(); err != nil || found {
		t.Fatal(found, err)
	}
	if err := lock.WriteWorkloadLifecycleV1(record); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".reploy", workloadLifecycleFilenameV1)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("startup evidence is not private")
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := lock.WriteWorkloadLifecycleV1(record); err == nil {
		t.Fatal("write after lock release")
	}
	second, err := AcquireExistingOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Unlock()
	got, found, err := second.ReadWorkloadLifecycleV1()
	if err != nil || !found || got != record {
		t.Fatal(got, found, err)
	}
	record.State, record.HandoffComplete, record.StartupComplete = "up", true, true
	if err := second.WriteWorkloadLifecycleV1(record); err != nil {
		t.Fatal(err)
	}
}

func TestWorkloadLifecycleRejectsUntrustedEvidenceV1(t *testing.T) {
	for _, content := range []string{`{}`, `{"schema":"workload-lifecycle-v1","state":"running"}`, strings.Repeat(" ", 8193), `{} {}`} {
		dir := t.TempDir()
		lock, err := AcquireOperationLock(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, ".reploy", workloadLifecycleFilenameV1)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := lock.ReadWorkloadLifecycleV1(); err == nil {
			t.Fatal("invalid evidence accepted")
		}
		_ = lock.Unlock()
	}
	if runtime.GOOS == "windows" {
		return
	}
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	path := filepath.Join(dir, ".reploy", workloadLifecycleFilenameV1)
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lock.ReadWorkloadLifecycleV1(); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lock.ReadWorkloadLifecycleV1(); err == nil {
		t.Fatal("hard link accepted")
	}
}

func TestWorkloadLifecycleRetirementBindingV1(t *testing.T) {
	record := WorkloadLifecycleV1{Schema: "workload-lifecycle-v1", State: "stopping", Endpoint: strings.Repeat("a", 64), Runtime: strings.Repeat("b", 64), ContainerID: strings.Repeat("c", 64), StartedAt: "2026-10-22T00:00:00Z", RetirementOwner: "control-0123456789abcdef", RecoveryOwners: canonical.Digest("sha256:" + strings.Repeat("d", 64))}
	for _, name := range []string{"valid", "down", "missing lease", "missing inventory", "invalid lease", "invalid inventory", "no process", "up", "starting"} {
		t.Run(name, func(t *testing.T) {
			r := record
			switch name {
			case "down":
				r.State = "down"
			case "missing lease":
				r.RetirementOwner = ""
			case "missing inventory":
				r.RecoveryOwners = ""
			case "invalid lease":
				r.RetirementOwner = "foreign"
			case "invalid inventory":
				r.RecoveryOwners = "not-a-digest"
			case "no process":
				r.ContainerID = ""
				r.StartedAt = ""
			case "up":
				r.State = "up"
				r.StartupComplete = true
			case "starting":
				r.State = "starting"
			}
			valid := name == "valid" || name == "down"
			dir := t.TempDir()
			lock, err := AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Unlock()
			err = lock.WriteWorkloadLifecycleV1(r)
			if (err == nil) != valid {
				t.Fatal("retirement binding validity", name, err)
			}
			got, found, readErr := lock.ReadWorkloadLifecycleV1()
			if readErr != nil || found != valid || valid && got != r {
				t.Fatal("retirement binding persistence", got, found, readErr)
			}
		})
	}
}
