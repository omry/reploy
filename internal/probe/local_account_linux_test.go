//go:build linux

package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallLocalAccountFilesPrependsResolvableAccountAndPreservesAliases(t *testing.T) {
	root := t.TempDir()
	passwd := filepath.Join(root, "passwd")
	group := filepath.Join(root, "group")
	if err := os.WriteFile(passwd, []byte("root:x:0:0:root:/root:/bin/sh\nnode:x:1000:1000::/home/node:/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(group, []byte("root:x:0:\nnode:x:1000:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installLocalAccountFiles("reploy", "1000", "1000", "/mnt/reploy-home", passwd, group); err != nil {
		t.Fatal(err)
	}
	passwdContent, err := os.ReadFile(passwd)
	if err != nil {
		t.Fatal(err)
	}
	groupContent, err := os.ReadFile(group)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(passwdContent), "reploy:x:1000:1000::/mnt/reploy-home:/sbin/nologin\n") ||
		!strings.Contains(string(passwdContent), "node:x:1000:1000:") {
		t.Fatalf("passwd = %q", passwdContent)
	}
	if !strings.HasPrefix(string(groupContent), "reploy:x:1000:\n") || !strings.Contains(string(groupContent), "node:x:1000:") {
		t.Fatalf("group = %q", groupContent)
	}
}

func TestInstallLocalAccountFilesCreatesMissingDatabaseFiles(t *testing.T) {
	root := t.TempDir()
	passwd := filepath.Join(root, "etc", "passwd")
	group := filepath.Join(root, "etc", "group")
	if err := installLocalAccountFiles("reploy", "12345", "12345", "/mnt/reploy-home", passwd, group); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{passwd, group} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o644 {
			t.Fatalf("local account file %s = %#v, %v", path, info, err)
		}
	}
}

func TestInstallLocalAccountFilesRealizesRootRuntimeAccount(t *testing.T) {
	root := t.TempDir()
	passwd := filepath.Join(root, "passwd")
	group := filepath.Join(root, "group")
	if err := os.WriteFile(passwd, []byte("root:x:0:0:root:/root:/bin/sh\nnobody:x:65534:65534::/:/sbin/nologin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(group, []byte("root:x:0:\nnogroup:x:65534:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installLocalAccountFiles("root", "0", "0", "/mnt/reploy-home", passwd, group); err != nil {
		t.Fatal(err)
	}
	passwdContent, err := os.ReadFile(passwd)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(passwdContent); !strings.HasPrefix(got, "root:x:0:0::/mnt/reploy-home:/sbin/nologin\n") || strings.Count(got, "root:x:0:0:") != 1 {
		t.Fatalf("root passwd = %q", got)
	}
}

func TestInstallLocalAccountFilesPreservesRootGroupForNonzeroPrimaryGID(t *testing.T) {
	root := t.TempDir()
	passwd := filepath.Join(root, "passwd")
	group := filepath.Join(root, "group")
	if err := os.WriteFile(passwd, []byte("root:x:0:0:root:/root:/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(group, []byte("root:x:0:\nusers:x:1000:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installLocalAccountFiles("root", "0", "1000", "/mnt/reploy-home", passwd, group); err != nil {
		t.Fatal(err)
	}
	passwdContent, err := os.ReadFile(passwd)
	if err != nil {
		t.Fatal(err)
	}
	groupContent, err := os.ReadFile(group)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(passwdContent); !strings.HasPrefix(got, "root:x:0:1000::/mnt/reploy-home:/sbin/nologin\n") {
		t.Fatalf("root passwd = %q", got)
	}
	if got := string(groupContent); !strings.HasPrefix(got, "_reploy_gid_1000:x:1000:\n") ||
		!strings.Contains(got, "root:x:0:\n") || !strings.Contains(got, "users:x:1000:\n") {
		t.Fatalf("root group database = %q", got)
	}
}

func TestInstallLocalAccountFilesRejectsNameCollisionAndSpecialDatabase(t *testing.T) {
	root := t.TempDir()
	passwd := filepath.Join(root, "passwd")
	group := filepath.Join(root, "group")
	if err := os.WriteFile(passwd, []byte("reploy:x:44:44::/home/reploy:/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(group, []byte("root:x:0:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := installLocalAccountFiles("reploy", "1000", "1000", "/mnt/reploy-home", passwd, group)
	if err == nil || !strings.Contains(err.Error(), "already defines") {
		t.Fatalf("name collision error = %v", err)
	}
	if err := os.Remove(passwd); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", passwd); err != nil {
		t.Fatal(err)
	}
	err = installLocalAccountFiles("reploy", "1000", "1000", "/mnt/reploy-home", passwd, group)
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("special passwd error = %v", err)
	}
}

func TestInstallLocalAccountFilesRejectsPrivilegedGroupMismatch(t *testing.T) {
	root := t.TempDir()
	for _, account := range []struct {
		name string
		uid  string
		gid  string
	}{
		{name: "reploy", uid: "1000", gid: "0"},
	} {
		err := installLocalAccountFiles(
			account.name, account.uid, account.gid, "/mnt/reploy-home",
			filepath.Join(root, account.name+"-passwd"), filepath.Join(root, account.name+"-group"),
		)
		if err == nil || !strings.Contains(err.Error(), "must not use GID 0") {
			t.Fatalf("account %#v error = %v", account, err)
		}
	}
}

func TestInstallLocalAccountFilesUsesStableGeneratedTimestamps(t *testing.T) {
	for _, timestamp := range []time.Time{time.Unix(123, 0), time.Unix(456, 0)} {
		root := t.TempDir()
		passwd, group := filepath.Join(root, "passwd"), filepath.Join(root, "group")
		other := filepath.Join(root, "unrelated")
		if err := os.WriteFile(other, []byte("preserved"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(other, timestamp, timestamp); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(root, timestamp, timestamp); err != nil {
			t.Fatal(err)
		}
		before, err := os.Stat(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := installLocalAccountFiles("reploy", "12345", "12345", "/mnt/reploy-home", passwd, group); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{passwd, group, root} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !info.ModTime().Equal(time.Unix(0, 0)) {
				t.Fatalf("%s mtime = %v", path, info.ModTime())
			}
		}
		after, err := os.Stat(root)
		if err != nil {
			t.Fatal(err)
		}
		if after.Mode() != before.Mode() {
			t.Fatalf("directory mode changed: %v to %v", before.Mode(), after.Mode())
		}
		info, err := os.Stat(other)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(timestamp) || info.Mode().Perm() != 0o600 {
			t.Fatalf("unrelated file changed: %v", info)
		}
	}
}

func TestWriteLocalAccountFileCleansTemporaryOnCommitFailure(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "passwd")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeLocalAccountFile(destination, []byte("account")); err == nil {
		t.Fatal("expected replacement of directory to fail")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "passwd" || !entries[0].IsDir() {
		t.Fatalf("unexpected remaining entries: %v", entries)
	}
}
