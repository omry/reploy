package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func currentOutputRuntimeUser() RuntimeUserPlan {
	backend := oneShotOutputOwnershipBackend()
	uid, gid := backend.currentUID(), backend.currentGID()
	if uid == 0 {
		uid, gid = 1, 1
	}
	return RuntimeUserPlan{UID: uid, GID: gid}
}

func TestOneShotOutputDirectoryIsDirectAndPersistent(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "results")
	session, err := prepareOneShotOutput(destination, "", currentOutputRuntimeUser())
	if err != nil {
		t.Fatal(err)
	}
	if session.mount == nil || session.mount.Variable != runtimeOutputDirectoryVariable || session.mount.ContainerPath != runtimeOutputRoot || session.mount.HostDirectory != destination {
		t.Fatalf("output directory session = %#v", session)
	}
	if info, err := os.Stat(destination); err != nil || !info.IsDir() {
		t.Fatalf("output directory was not created: %v", err)
	}
	if err := session.abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("direct output directory did not persist: %v", err)
	}
}

func TestOneShotOutputRejectsRootBeforePreparingHostPaths(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "not-created")
	if _, err := prepareOneShotOutput(destination, "", RuntimeUserPlan{UID: 0, GID: 0}); err == nil || !strings.Contains(err.Error(), "root-safe output contract") {
		t.Fatalf("root output error = %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("root output destination was mutated: %v", err)
	}
}

func TestOneShotOutputFilePublishesCompleteFile(t *testing.T) {
	final := filepath.Join(t.TempDir(), "report.json")
	session, err := prepareOneShotOutput("", final, currentOutputRuntimeUser())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatalf("final file exists before publication: %v", err)
	}
	if err := os.WriteFile(session.stagingFile, []byte("complete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := session.publish(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(final)
	if err != nil || string(content) != "complete\n" {
		t.Fatalf("published output = %q, %v", content, err)
	}
	if _, err := os.Stat(session.stagingDir); !os.IsNotExist(err) {
		t.Fatalf("staging directory remains after publication: %v", err)
	}
}

func TestOneShotOutputFileFailureRemovesReservation(t *testing.T) {
	final := filepath.Join(t.TempDir(), "report.json")
	session, err := prepareOneShotOutput("", final, currentOutputRuntimeUser())
	if err != nil {
		t.Fatal(err)
	}
	if err := session.abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(session.stagingDir); !os.IsNotExist(err) {
		t.Fatalf("staging directory remains after failure: %v", err)
	}
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatalf("final file exists after failure: %v", err)
	}
}

func TestOneShotOutputFileDoesNotOverwriteOrShareReservation(t *testing.T) {
	final := filepath.Join(t.TempDir(), "report.json")
	session, err := prepareOneShotOutput("", final, currentOutputRuntimeUser())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareOneShotOutput("", final, currentOutputRuntimeUser()); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("second reservation error = %v", err)
	}
	if err := os.WriteFile(session.stagingFile, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, []byte("existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := session.publish(); err == nil || !strings.Contains(err.Error(), "without overwriting") {
		t.Fatalf("publication collision error = %v", err)
	}
	if _, err := os.Stat(session.stagingFile); err != nil {
		t.Fatalf("complete staged file was not retained: %v", err)
	}
	content, err := os.ReadFile(final)
	if err != nil || string(content) != "existing\n" {
		t.Fatalf("existing destination changed: %q, %v", content, err)
	}
}

func TestOneShotOutputOptionsAreExclusiveAndFileMustBeRegular(t *testing.T) {
	root := t.TempDir()
	if _, err := prepareOneShotOutput(root, filepath.Join(root, "result"), currentOutputRuntimeUser()); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("exclusive options error = %v", err)
	}
	session, err := prepareOneShotOutput("", filepath.Join(root, "result"), currentOutputRuntimeUser())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(session.stagingFile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := session.publish(); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("non-regular output error = %v", err)
	}
}

func TestOneShotOutputFileAssignsReservationToRuntimeUser(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "result")
	wantUID, wantGID := uint32(991), uint32(992)
	var chownPath string
	var chownUID, chownGID uint32

	session, err := prepareOneShotOutputWithBackend("", final, RuntimeUserPlan{UID: wantUID, GID: wantGID}, oneShotOutputBackend{
		currentUID: func() uint32 { return 0 },
		currentGID: func() uint32 { return 0 },
		chown: func(path string, uid uint32, gid uint32) error {
			chownPath, chownUID, chownGID = path, uid, gid
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if chownPath != session.stagingDir || chownUID != wantUID || chownGID != wantGID {
		t.Fatalf("reservation ownership = %q %d:%d, want %q %d:%d", chownPath, chownUID, chownGID, session.stagingDir, wantUID, wantGID)
	}
}

func TestOneShotOutputFileRemovesReservationWhenOwnershipFails(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "result")
	_, err := prepareOneShotOutputWithBackend("", final, RuntimeUserPlan{UID: 991, GID: 992}, oneShotOutputBackend{
		currentUID: func() uint32 { return 0 },
		currentGID: func() uint32 { return 0 },
		chown:      func(string, uint32, uint32) error { return os.ErrPermission },
	})
	if err == nil || !strings.Contains(err.Error(), "runtime user 991:992") {
		t.Fatalf("ownership error = %v", err)
	}
	stagingDir := filepath.Join(root, ".result.reploy-output")
	if _, statErr := os.Lstat(stagingDir); !os.IsNotExist(statErr) {
		t.Fatalf("failed ownership left reservation: %v", statErr)
	}
}

func TestOneShotOutputFileRejectsReservationReplacedBySymlink(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "result")
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := prepareOneShotOutputWithBackend("", final, RuntimeUserPlan{UID: 991, GID: 992}, oneShotOutputBackend{
		currentUID: func() uint32 { return 0 },
		currentGID: func() uint32 { return 0 },
		chown: func(path string, _, _ uint32) error {
			if err := os.Remove(path); err != nil {
				return err
			}
			return os.Symlink(target, path)
		},
	})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("substituted reservation error = %v", err)
	}
	stagingDir := filepath.Join(root, ".result.reploy-output")
	if _, statErr := os.Lstat(stagingDir); !os.IsNotExist(statErr) {
		t.Fatalf("substituted reservation was not removed: %v", statErr)
	}
}
