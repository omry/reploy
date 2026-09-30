package deploy

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestOperationLockRoundTripsPendingValidatedBuild(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	pending := PendingValidatedBuildV1{
		Schema: PendingValidatedBuildSchemaV1, BuildLockDigest: digest,
		Final: ValidatedBuildReferenceV1{
			Image: validatedBuildTestImage(digest), ImageReference: "reploy/env/demo:g-0123456789abcdef0123456789abcdef",
		},
	}
	if err := lock.WritePendingValidatedBuildV1(pending); err != nil {
		t.Fatal(err)
	}
	if err := lock.WritePendingValidatedBuildV1(pending); err == nil {
		t.Fatal("duplicate pending validated build was accepted")
	}
	loaded, found, err := lock.ReadPendingValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(loaded, pending) {
		t.Fatalf("loaded=%#v found=%v err=%v", loaded, found, err)
	}
	path := filepath.Join(dir, ".reploy", pendingValidatedBuildFilenameV1)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || hasPOSIXPermissionBits() && info.Mode().Perm() != 0o600 {
		t.Fatalf("pending journal mode=%v err=%v", info.Mode(), err)
	}
	encoded, err := EncodePendingValidatedBuildV1(pending)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePendingValidatedBuildV1(append(bytes.Clone(encoded), '\n')); err == nil {
		t.Fatal("noncanonical pending journal was accepted")
	}
	if err := lock.RemovePendingValidatedBuildV1(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := lock.ReadPendingValidatedBuildV1(); err != nil || found {
		t.Fatalf("pending journal remains: found=%v err=%v", found, err)
	}
}
