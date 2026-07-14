package docs_test

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestYAMLFencesParse(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"APT_PROVIDER.md", "BLUEPRINT_ENVIRONMENT_MODEL.md"} {
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(strings.NewReader(string(content)))
		inYAML := false
		line := 0
		start := 0
		var block strings.Builder
		for scanner.Scan() {
			line++
			text := scanner.Text()
			if !inYAML && text == "```yaml" {
				inYAML = true
				start = line + 1
				block.Reset()
				continue
			}
			if inYAML && text == "```" {
				var value any
				if err := yaml.Unmarshal([]byte(block.String()), &value); err != nil {
					t.Errorf("%s:%d: invalid fenced YAML: %v", name, start, err)
				}
				inYAML = false
				continue
			}
			if inYAML {
				block.WriteString(text)
				block.WriteByte('\n')
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
		if inYAML {
			t.Errorf("%s:%d: unterminated fenced YAML", name, start-1)
		}
	}
}
