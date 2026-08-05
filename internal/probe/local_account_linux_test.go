//go:build linux

package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		{name: "root", uid: "0", gid: "1000"},
	} {
		err := installLocalAccountFiles(
			account.name, account.uid, account.gid, "/mnt/reploy-home",
			filepath.Join(root, account.name+"-passwd"), filepath.Join(root, account.name+"-group"),
		)
		if err == nil || !strings.Contains(err.Error(), "GID disagree") {
			t.Fatalf("account %#v error = %v", account, err)
		}
	}
}
