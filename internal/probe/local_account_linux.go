//go:build linux

package probe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	applicationPasswdPath = "/etc/passwd"
	applicationGroupPath  = "/etc/group"
)

func installApplicationLocalAccount(name string, uid string, gid string, home string) error {
	return installLocalAccountFiles(name, uid, gid, home, applicationPasswdPath, applicationGroupPath)
}

func installLocalAccountFiles(name string, uid string, gid string, home string, passwdPath string, groupPath string) error {
	if err := validateLocalAccountInput(name, uid, gid, home); err != nil {
		return err
	}
	passwd, err := rewriteLocalAccountFile(passwdPath, name, uid, 2, name+":x:"+uid+":"+gid+"::"+home+":/sbin/nologin")
	if err != nil {
		return fmt.Errorf("prepare passwd entry: %w", err)
	}
	groupName := name
	if uid == "0" && gid != "0" {
		// Keep the conventional root group bound to numeric GID 0 while making
		// the invoking root process's actual primary GID resolvable.
		groupName = "_reploy_gid_" + gid
	}
	group, err := rewriteLocalAccountFile(groupPath, groupName, gid, 2, groupName+":x:"+gid+":")
	if err != nil {
		return fmt.Errorf("prepare group entry: %w", err)
	}
	if err := writeLocalAccountFile(passwdPath, passwd); err != nil {
		return fmt.Errorf("write passwd entry: %w", err)
	}
	if err := writeLocalAccountFile(groupPath, group); err != nil {
		return fmt.Errorf("write group entry: %w", err)
	}
	return nil
}

func validateLocalAccountInput(name string, uid string, gid string, home string) error {
	if name == "" || len(name) > 32 {
		return fmt.Errorf("local account name is invalid")
	}
	for index, character := range name {
		if character >= 'a' && character <= 'z' || character == '_' && index == 0 ||
			index > 0 && (character >= '0' && character <= '9' || character == '_' || character == '-') {
			continue
		}
		return fmt.Errorf("local account name is invalid")
	}
	parsedUID, err := strconv.ParseUint(uid, 10, 32)
	if err != nil || strconv.FormatUint(parsedUID, 10) != uid {
		return fmt.Errorf("local account UID is invalid")
	}
	if parsedUID == 0 && name != "root" || parsedUID != 0 && name == "root" {
		return fmt.Errorf("local account root name and UID disagree")
	}
	parsedGID, err := strconv.ParseUint(gid, 10, 32)
	if err != nil || strconv.FormatUint(parsedGID, 10) != gid {
		return fmt.Errorf("local account GID is invalid")
	}
	if parsedUID != 0 && parsedGID == 0 {
		return fmt.Errorf("non-root local account must not use GID 0")
	}
	if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home || strings.ContainsAny(home, ":\n\r") {
		return fmt.Errorf("local account home is invalid")
	}
	return nil
}

func rewriteLocalAccountFile(path string, name string, id string, idField int, replacement string) ([]byte, error) {
	content, err := readLocalAccountFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	kept := make([]string, 0, len(lines)+1)
	kept = append(kept, replacement)
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) <= idField {
			return nil, fmt.Errorf("%s contains a malformed entry", path)
		}
		if fields[0] != name {
			kept = append(kept, line)
			continue
		}
		if fields[idField] != id {
			return nil, fmt.Errorf("%s already defines local account name %q with ID %s", path, name, fields[idField])
		}
	}
	return []byte(strings.Join(kept, "\n") + "\n"), nil
}

func readLocalAccountFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", path)
	}
	return os.ReadFile(path)
}

func writeLocalAccountFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".reploy-account-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	remove := true
	defer func() {
		_ = temporary.Close()
		if remove {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	remove = false
	return nil
}
