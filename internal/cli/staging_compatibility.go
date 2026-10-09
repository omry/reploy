package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/dockerdeploy"
)

// Unknown fields and unsupported schemas can indicate a newer staging writer.
// Do not prescribe restaging for missing files, permissions or installed state.
func printStagingCompatibilityHint(output io.Writer, dir string, err error, embedded bool) {
	if err == nil {
		return
	}
	message := err.Error()
	if !strings.Contains(message, "unknown field") && !strings.Contains(message, "resolved blueprint is not in its canonical wire form") && !(strings.Contains(message, "schema") && strings.Contains(message, "unsupported")) {
		return
	}
	content, readErr := os.ReadFile(filepath.Join(dir, dockerdeploy.StateFileName))
	if readErr != nil {
		return
	}
	var state struct {
		Schema     string          `json:"schema"`
		Deployment json.RawMessage `json:"deployment"`
	}
	if json.Unmarshal(content, &state) != nil || state.Schema == "" || (len(state.Deployment) > 0 && string(state.Deployment) != "null") {
		return
	}
	if embedded {
		fmt.Fprintf(output, "This controller runs Reploy %s from the staging directory.\n", deploy.ToolVersion)
	}
	fmt.Fprintln(output, "These staging files may be incompatible with this Reploy version.")
	directoryArg := shellQuoteArg(dir)
	if runtime.GOOS == "windows" {
		// Native Windows guidance uses PowerShell literal-string quoting.
		directoryArg = "'" + strings.ReplaceAll(dir, "'", "''") + "'"
	}
	fmt.Fprintf(output, "Try reploy stage --update --dir %s using the current installed Reploy CLI to refresh them.\n", directoryArg)
}
