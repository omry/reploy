package dockerdeploy

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func readDockerEnv(dir string) (map[string]string, error) {
	file, err := os.Open(filepath.Join(dir, DockerEnvFileName))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid docker.env line: %s", line)
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func envValue(values map[string]string, key string, defaultValue string) string {
	if value, ok := values[key]; ok && value != "" {
		return value
	}
	return defaultValue
}

func upsertDockerEnvValues(dir string, updates map[string]string) (bool, error) {
	return writeDockerEnvValues(dir, updates, true)
}

func updateExistingDockerEnvValues(dir string, updates map[string]string) (bool, error) {
	return writeDockerEnvValues(dir, updates, false)
}

func writeDockerEnvValues(dir string, updates map[string]string, appendMissing bool) (bool, error) {
	path := filepath.Join(dir, DockerEnvFileName)
	content, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	seen := map[string]bool{}
	changed := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, _, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value, shouldUpdate := updates[key]
		if !shouldUpdate {
			continue
		}
		seen[key] = true
		replacement := key + "=" + value
		if line != replacement {
			lines[index] = replacement
			changed = true
		}
	}
	missing := make([]string, 0, len(updates))
	if appendMissing {
		for key := range updates {
			if !seen[key] {
				missing = append(missing, key)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		for _, key := range missing {
			lines = append(lines, key+"="+updates[key])
		}
		changed = true
	}
	if !changed {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}
