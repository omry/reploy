//go:build linux

package probe

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"syscall"
	"unicode/utf8"

	"github.com/omry/reploy/internal/canonical"
)

func Inspect(request RequestV1) (ResponseV1, error) {
	if err := ValidateRequestV1(request); err != nil {
		return ResponseV1{}, err
	}
	response := ResponseV1{Schema: ResponseSchemaV1, Observations: make([]ExecutableObservationV1, 0, len(request.Inspections))}
	for _, inspection := range request.Inspections {
		observation, err := inspectExecutable(inspection)
		if err != nil {
			return ResponseV1{}, fmt.Errorf("inspect executable %q at %s: %w", inspection.ID, inspection.InvocationPath, err)
		}
		response.Observations = append(response.Observations, observation)
	}
	if err := ValidateResponseV1(request, response); err != nil {
		return ResponseV1{}, fmt.Errorf("validate probe response: %w", err)
	}
	return response, nil
}

func inspectExecutable(inspection ExecutableInspectionV1) (ExecutableObservationV1, error) {
	terminalPath, links, err := resolveExecutableLinks(inspection.InvocationPath)
	if err != nil {
		return ExecutableObservationV1{}, err
	}
	terminal, err := observeRegularFile(terminalPath)
	if err != nil {
		return ExecutableObservationV1{}, err
	}
	access, err := observeAccessPaths(inspection.InvocationPath, links, terminalPath)
	if err != nil {
		return ExecutableObservationV1{}, err
	}
	return ExecutableObservationV1{
		ID: inspection.ID, InvocationPath: inspection.InvocationPath,
		Links: links, Terminal: terminal, Access: access,
	}, nil
}

func resolveExecutableLinks(invocationPath string) (string, []LinkObservationV1, error) {
	current := invocationPath
	links := []LinkObservationV1{}
	seen := map[string]bool{}
	for {
		if seen[current] {
			return "", nil, fmt.Errorf("symbolic-link cycle at %s", current)
		}
		seen[current] = true
		info, err := os.Lstat(current)
		if err != nil {
			return "", nil, fmt.Errorf("inspect path %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return current, links, nil
		}
		target, err := os.Readlink(current)
		if err != nil {
			return "", nil, fmt.Errorf("read symbolic link %s: %w", current, err)
		}
		if target == "" || !utf8.ValidString(target) {
			return "", nil, fmt.Errorf("symbolic link %s has an invalid target", current)
		}
		resolved := target
		if !path.IsAbs(resolved) {
			resolved = path.Join(path.Dir(current), resolved)
		}
		resolved = path.Clean(resolved)
		uid, gid, err := numericOwnership(info)
		if err != nil {
			return "", nil, err
		}
		links = append(links, LinkObservationV1{
			Path: current, Target: target, ResolvedPath: resolved,
			Mode: modeString(info), UID: uid, GID: gid,
		})
		current = resolved
	}
}

func observeRegularFile(filePath string) (FileObservationV1, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return FileObservationV1{}, fmt.Errorf("open terminal %s: %w", filePath, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return FileObservationV1{}, fmt.Errorf("inspect terminal %s: %w", filePath, err)
	}
	if !info.Mode().IsRegular() {
		return FileObservationV1{}, fmt.Errorf("terminal %s is not a regular file", filePath)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return FileObservationV1{}, fmt.Errorf("hash terminal %s: %w", filePath, err)
	}
	uid, gid, err := numericOwnership(info)
	if err != nil {
		return FileObservationV1{}, err
	}
	return FileObservationV1{
		Path: filePath, Kind: "regular", Mode: modeString(info), Size: strconv.FormatInt(info.Size(), 10),
		SHA256: canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil))), UID: uid, GID: gid,
	}, nil
}

func observeAccessPaths(invocationPath string, links []LinkObservationV1, terminalPath string) ([]AccessObservationV1, error) {
	required := requiredAccessPaths(invocationPath, links, terminalPath)
	paths := make([]string, 0, len(required))
	for itemPath := range required {
		paths = append(paths, itemPath)
	}
	sort.Strings(paths)
	observations := make([]AccessObservationV1, 0, len(paths))
	for _, itemPath := range paths {
		info, err := os.Stat(itemPath)
		if err != nil {
			return nil, fmt.Errorf("inspect access path %s: %w", itemPath, err)
		}
		kind := required[itemPath]
		if kind == "regular" {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("terminal access path %s is not a regular file", itemPath)
			}
		} else if !info.IsDir() {
			return nil, fmt.Errorf("access path %s is not a directory", itemPath)
		}
		uid, gid, err := numericOwnership(info)
		if err != nil {
			return nil, err
		}
		observations = append(observations, AccessObservationV1{Path: itemPath, Kind: kind, Mode: modeString(info), UID: uid, GID: gid})
	}
	return observations, nil
}

func modeString(info os.FileInfo) string {
	return fmt.Sprintf("%04o", info.Mode().Perm())
}

func numericOwnership(info os.FileInfo) (string, string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", fmt.Errorf("path %s has no Linux stat ownership", info.Name())
	}
	return strconv.FormatUint(uint64(stat.Uid), 10), strconv.FormatUint(uint64(stat.Gid), 10), nil
}
