package dockerdeploy

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
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
