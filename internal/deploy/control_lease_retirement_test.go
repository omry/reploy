package deploy

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRetirementLeaseExactOwnershipAcrossOperationUnlockV1(t *testing.T) {
	dir := t.TempDir()
	operation, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := operation.AcquireControlLeaseV1("control-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := lease.RequireHeld(); err != nil {
		t.Fatal(err)
	}
	next, err := AcquireExistingOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Unlock()
	if second, err := next.AcquireControlLeaseV1("control-0123456789abcdef"); err == nil {
		second.Close()
		t.Fatal("retirement lost its exclusive lease across unlock")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lease.RequireHeld(); err == nil {
		t.Fatal("closed lease still authorizes retirement")
	}
	if _, err := os.Stat(lease.path); err != nil {
		t.Fatal("ambiguous retirement file was removed", err)
	}
	second, err := next.AcquireControlLeaseV1("control-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.RequireHeld(); err == nil {
		t.Fatal("another holder substituted for original lease")
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lease.path); !os.IsNotExist(err) {
		t.Fatal("completed lease not removed", err)
	}
	var missing *QueueEntryLeaseV1
	if err := missing.RequireHeld(); err == nil {
		t.Fatal("nil lease authorizes retirement")
	}
}

func TestRetirementLeasePathReplacementIsNotCurrentOwnershipV1(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows sharing rejects replacement of a locked open file")
	}
	dir := t.TempDir()
	operation, err := AcquireOperationLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	lease, err := operation.AcquireControlLeaseV1("control-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	path := filepath.Join(dir, ".reploy", "control-0123456789abcdef.lease")
	if err := os.Rename(path, path+".retained"); err != nil {
		t.Fatal(err)
	}
	replacement, err := operation.AcquireControlLeaseV1("control-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Release()
	if err := operation.RequireQueueEntryLeaseHeldV1("control-0123456789abcdef"); err != nil {
		t.Fatal("fixture does not have a different holder", err)
	}
	if err := lease.RequireHeld(); err == nil {
		t.Fatal("foreign held lease substituted for exact original instance")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := replacement.RequireHeld(); err != nil {
		t.Fatal("original owner cleanup removed foreign lease", err)
	}
}
