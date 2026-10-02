package deploy

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

func pendingValidatedFixtureV1(t *testing.T) PendingValidatedBuildV1 {
	t.Helper()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	record := ValidatedBuildV1{Schema: ValidatedBuildSchemaV1, BlueprintDigest: digest, OverlayDigest: digest, PackageOverridesDigest: digest, Platform: platform, BuildLockDigest: digest, Image: validatedBuildTestImage(digest), ImageReference: "reploy/env/demo:candidate", PendingStorageCleanup: true}
	owner := EnvironmentGenerationState{Reference: record.ImageReference, ImageDigest: digest, RootFSSubject: digest, BuildLockDigest: digest, Platform: platform, RuntimePolicyDigest: digest}
	record.Owner = &owner
	record.Companion = &OwnedImageReferenceV1{Image: record.Image, Reference: "reploy/portable/demo:candidate"}
	return PendingValidatedBuildV1{Schema: PendingValidatedBuildSchemaV1, Candidate: record}
}

func TestPendingValidatedBuildStrictCodecV1(t *testing.T) {
	intent := pendingValidatedFixtureV1(t)
	content, err := EncodePendingValidatedBuildV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePendingValidatedBuildV1(content)
	if err != nil || !reflect.DeepEqual(decoded, intent) {
		t.Fatalf("decoded=%#v error=%v", decoded, err)
	}
	for name, invalid := range map[string][]byte{
		"unknown schema":   bytes.Replace(content, []byte(PendingValidatedBuildSchemaV1), []byte("pending-validated-build-v2"), 1),
		"unknown field":    append([]byte(`{"extra":1,`), content[1:]...),
		"missing previous": bytes.Replace(content, []byte(`,"previous":null`), nil, 1),
		"trailer":          append(append([]byte{}, content...), []byte(`{}`)...),
		"whitespace":       append(append([]byte{}, content...), '\n'),
		"null":             []byte(`null`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePendingValidatedBuildV1(invalid); err == nil {
				t.Fatalf("accepted %s", invalid)
			}
		})
	}
	for name, change := range map[string]func(*PendingValidatedBuildV1){
		"missing companion": func(p *PendingValidatedBuildV1) { p.Candidate.Companion = nil },
		"missing owner":     func(p *PendingValidatedBuildV1) { p.Candidate.Owner = nil },
		"owner mismatch": func(p *PendingValidatedBuildV1) {
			owner := *p.Candidate.Owner
			owner.Reference = "reploy/env/demo:other"
			p.Candidate.Owner = &owner
		},
		"discarded candidate":    func(p *PendingValidatedBuildV1) { p.Candidate.Discarded = true },
		"no cleanup":             func(p *PendingValidatedBuildV1) { p.Candidate.PendingStorageCleanup = false },
		"previous primary alias": func(p *PendingValidatedBuildV1) { old := p.Candidate; p.Previous = &old },
		"previous cleanup alias": func(p *PendingValidatedBuildV1) {
			old := p.Candidate
			old.Owner = nil
			old.Companion = nil
			old.ImageReference = "reploy/env/demo:previous"
			old.PendingCleanup = []ValidatedBuildReferenceV1{{Image: p.Candidate.Image, ImageReference: p.Candidate.Companion.Reference}}
			p.Previous = &old
		},
		"companion cleanup primary alias": func(p *PendingValidatedBuildV1) {
			p.Candidate.PendingCleanup = []ValidatedBuildReferenceV1{{Image: p.Candidate.Image, ImageReference: p.Candidate.ImageReference, CompanionOwner: p.Candidate.Owner}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := intent
			change(&p)
			if _, err := EncodePendingValidatedBuildV1(p); err == nil {
				t.Fatal("invalid intent accepted")
			}
		})
	}
}

func TestPendingValidatedBuildFileLifecycleV1(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	intent := pendingValidatedFixtureV1(t)
	if _, found, err := lock.ReadPendingValidatedBuildV1(); err != nil || found {
		t.Fatalf("found=%v error=%v", found, err)
	}
	if err := lock.WritePendingValidatedBuildV1(intent); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".reploy", pendingValidatedBuildFilenameV1)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || hasPOSIXPermissionBits() && info.Mode().Perm() != 0o600 {
		t.Fatalf("private file: %v %v", info, err)
	}
	if err := lock.WritePendingValidatedBuildV1(intent); err == nil {
		t.Fatal("existing intent overwritten")
	}
	loaded, found, err := lock.ReadPendingValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(loaded, intent) {
		t.Fatalf("intent=%#v found=%v error=%v", loaded, found, err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lock.ReadPendingValidatedBuildV1(); err == nil {
		t.Fatal("read through released lock")
	}
	if err := lock.WritePendingValidatedBuildV1(intent); err == nil {
		t.Fatal("write through released lock")
	}
	if err := lock.RemovePendingValidatedBuildV1(); err == nil {
		t.Fatal("remove through released lock")
	}
	lock, err = AcquireExistingOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	loaded, found, err = lock.ReadPendingValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(loaded, intent) {
		t.Fatalf("restart lost intent: %v %v", found, err)
	}
	if err := lock.RemovePendingValidatedBuildV1(); err != nil {
		t.Fatal(err)
	}
	if err := lock.RemovePendingValidatedBuildV1(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("intent remains: %v", err)
	}
}

func TestPendingValidatedBuildAtomicFailuresV1(t *testing.T) {
	for _, phase := range []string{"replace", "sync-write", "sync-remove"} {
		t.Run(phase, func(t *testing.T) {
			lock, err := AcquireOperationLock(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Unlock()
			intent := pendingValidatedFixtureV1(t)
			failure := errors.New("injected durable boundary failure")
			originalReplace, originalSync := replaceAtomicStateFile, syncAtomicStateFileDirectory
			defer func() { replaceAtomicStateFile, syncAtomicStateFileDirectory = originalReplace, originalSync }()
			if phase == "sync-remove" {
				if err := lock.WritePendingValidatedBuildV1(intent); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "replace" {
				replaceAtomicStateFile = func(string, string) error { return failure }
			} else {
				syncAtomicStateFileDirectory = func(string) error { return failure }
			}
			if phase == "sync-remove" {
				err = lock.RemovePendingValidatedBuildV1()
			} else {
				err = lock.WritePendingValidatedBuildV1(intent)
			}
			if !errors.Is(err, failure) {
				t.Fatalf("error=%v", err)
			}
			loaded, found, err := lock.ReadPendingValidatedBuildV1()
			if err != nil || found != (phase == "sync-write") {
				t.Fatalf("found=%v error=%v", found, err)
			}
			if found && !reflect.DeepEqual(loaded, intent) {
				t.Fatal("post-rename intent identity changed")
			}
		})
	}
}

func TestValidatedBuildCommitDirectorySyncFailureAfterRenameV1(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()

	record := pendingValidatedFixtureV1(t).Candidate
	failure := errors.New("injected validated record directory sync failure")
	originalSync := syncAtomicStateFileDirectory
	syncAtomicStateFileDirectory = func(string) error { return failure }
	defer func() { syncAtomicStateFileDirectory = originalSync }()

	if err := lock.CommitValidatedBuildV1(record); !errors.Is(err, failure) {
		t.Fatalf("commit error=%v", err)
	}
	loaded, found, err := lock.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(loaded, record) {
		t.Fatalf("post-rename record=%#v found=%v error=%v", loaded, found, err)
	}
}

func TestPendingValidatedBuildRejectsNonregularFileV1(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			lock, err := AcquireOperationLock(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Unlock()
			path := filepath.Join(dir, ".reploy", pendingValidatedBuildFilenameV1)
			switch kind {
			case "directory":
				err = os.Mkdir(path, 0o700)
			case "symlink":
				err = os.Symlink("missing", path)
				if err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "malformed":
				err = os.WriteFile(path, []byte(`{"schema":"unknown"}`), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := lock.ReadPendingValidatedBuildV1(); err == nil {
				t.Fatal("untrusted intent accepted")
			}
			if err := lock.WritePendingValidatedBuildV1(pendingValidatedFixtureV1(t)); err == nil {
				t.Fatal("untrusted intent replaced")
			}
			if kind != "malformed" {
				if err := lock.RemovePendingValidatedBuildV1(); err == nil {
					t.Fatal("nonregular intent removed")
				}
			}
		})
	}
}
