package dockerdeploy

import (
	"archive/zip"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func runDockerIntegration(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, "docker", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func hasPOSIXPermissionBits() bool { return runtime.GOOS != "windows" }

func writeFakeCommand(t *testing.T, dir string, name string, posixScript string, windowsScript string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := posixScript
	mode := fs.FileMode(0o755)
	if runtime.GOOS == "windows" {
		path += ".cmd"
		content = windowsScript
		mode = 0o644
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func containsInOrder(values []string, sequence []string) bool {
	for start := range values {
		if start+len(sequence) > len(values) {
			return false
		}
		matched := true
		for offset, value := range sequence {
			if values[start+offset] != value {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return len(sequence) == 0
}

func containsAdjacent(values []string, first string, second string) bool {
	for index := 0; index+1 < len(values); index++ {
		if values[index] == first && values[index+1] == second {
			return true
		}
	}
	return false
}

func writePythonIntegrationWheel(t *testing.T, filename string) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	files := map[string]string{
		"demo_server.py":                             "def main():\n    print('hello from generated Python image')\n",
		"demo_server-1.0.dist-info/METADATA":         "Metadata-Version: 2.1\nName: demo-server\nVersion: 1.0\n\n",
		"demo_server-1.0.dist-info/WHEEL":            "Wheel-Version: 1.0\nGenerator: reploy-integration\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
		"demo_server-1.0.dist-info/entry_points.txt": "[console_scripts]\ndemo-server = demo_server:main\n",
		"demo_server-1.0.dist-info/RECORD":           "",
	}
	for name, content := range files {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
