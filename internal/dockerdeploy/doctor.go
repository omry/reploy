package dockerdeploy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/python"
)

type DoctorOptions struct {
	Dir        string
	Preinstall bool
	Quiet      bool
	Stdout     io.Writer
}

type DoctorFinding struct {
	Status  string
	Message string
}

func Doctor(options DoctorOptions) int {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	findings := doctorFindings(options.Dir, options.Preinstall)
	colors := doctorStatusColors(options.Stdout)
	exitCode := 0
	for _, finding := range findings {
		if finding.Status == "fail" {
			exitCode = 1
		}
		if options.Stdout != nil && !(options.Quiet && finding.Status == "ok") {
			fmt.Fprintf(options.Stdout, "%s: %s\n", colors.status(finding.Status), finding.Message)
		}
	}
	return exitCode
}

type doctorColors struct {
	enabled bool
}

func doctorStatusColors(output io.Writer) doctorColors {
	return doctorColors{enabled: outputColorEnabled(output)}
}

func (colors doctorColors) status(status string) string {
	if !colors.enabled {
		return status
	}
	switch status {
	case "ok":
		return "\x1b[32mok\x1b[0m"
	case "fail":
		return "\x1b[31mfail\x1b[0m"
	default:
		return status
	}
}

func outputColorEnabled(output io.Writer) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("REPLOY_COLOR"))) {
	case "always":
		return true
	case "never":
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if !terminalLooksColorCapable() {
		return false
	}
	return writerLooksTerminal(output)
}

func doctorFindings(dir string, preinstall bool) []DoctorFinding {
	required := []string{
		"reploy",
		ComposeFileName,
		DockerEnvFileName,
		RequirementsFileName,
		StateFileName,
		ManifestFileName,
	}
	findings := []DoctorFinding{}
	for _, relativePath := range required {
		path := filepath.Join(dir, relativePath)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				findings = append(findings, DoctorFinding{Status: "fail", Message: "missing file: " + path})
			} else {
				findings = append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("cannot inspect file: %s: %v", path, err)})
			}
			continue
		}
		findings = append(findings, DoctorFinding{Status: "ok", Message: "file exists: " + path})
	}
	manifest, err := deploy.LoadDeploymentManifest(filepath.Join(dir, ManifestFileName))
	if err != nil {
		findings = append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("cannot read manifest: %v", err)})
		return findings
	}
	for relativePath, entry := range manifest.Files {
		if doctorSkipsGeneratedFileDrift(relativePath, preinstall) {
			path := filepath.Join(dir, filepath.FromSlash(relativePath))
			findings = append(findings, DoctorFinding{Status: "ok", Message: "generated file drift ignored for preinstall; install overwrites target: " + path})
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(relativePath))
		hash, err := deploy.HashFile(path)
		if err != nil {
			findings = append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("cannot hash generated file: %s: %v", path, err)})
			continue
		}
		if hash != entry.SHA256 {
			findings = append(findings, DoctorFinding{Status: "fail", Message: "generated file has local edits: " + path})
			continue
		}
		findings = append(findings, DoctorFinding{Status: "ok", Message: "generated file matches manifest: " + path})
	}
	if preinstall {
		findings = append(findings, doctorPreinstallFindings(dir)...)
	}
	return findings
}

func doctorSkipsGeneratedFileDrift(relativePath string, preinstall bool) bool {
	return preinstall && filepath.ToSlash(relativePath) == ToolBinaryFileName
}

func doctorPreinstallFindings(dir string) []DoctorFinding {
	findings := []DoctorFinding{}
	values, err := readDockerEnv(dir)
	if err != nil {
		return append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("cannot read %s: %v", DockerEnvFileName, err)})
	}
	for key, defaultValue := range map[string]string{
		"REPLOY_CONFIG_DIR":        "./conf",
		"REPLOY_REQUIREMENTS_FILE": "./" + RequirementsFileName,
		"REPLOY_BUNDLE_DIR":        "./" + BundleDirName,
		"REPLOY_RUNTIME_DIR":       "./" + RuntimeDirName,
		"REPLOY_DATA_DIR":          "./data",
	} {
		value := envValue(values, key, defaultValue)
		switch {
		case value == "":
			findings = append(findings, DoctorFinding{Status: "fail", Message: "runtime path is empty: " + key})
		case filepath.IsAbs(value):
			findings = append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("runtime path must be relative for install: %s=%s", key, value)})
		case filepath.Clean(value) == ".." || strings.HasPrefix(filepath.Clean(value), "../"):
			findings = append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("runtime path must stay under deployment directory: %s=%s", key, value)})
		case strings.ContainsAny(value, " \t\n"):
			findings = append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("runtime path must not contain whitespace: %s=%s", key, value)})
		default:
			findings = append(findings, DoctorFinding{Status: "ok", Message: "install runtime path is relative: " + key})
		}
	}
	owner, err := resolveInstallOwner(values)
	if err != nil {
		if spec, createErr := installOwnerCreationSpecForResolveError(values, err); createErr == nil {
			findings = append(findings, DoctorFinding{Status: "ok", Message: "install owner will be created if missing: " + spec})
		} else {
			findings = append(findings, DoctorFinding{Status: "fail", Message: "install owner must resolve to a non-root uid:gid: " + createErr.Error()})
		}
	} else {
		findings = append(findings, DoctorFinding{Status: "ok", Message: fmt.Sprintf("install owner resolves to %s (%d:%d)", owner.Spec, owner.UID, owner.GID)})
	}
	state, err := loadState(dir)
	if err != nil {
		return append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("cannot read state: %v", err)})
	}
	state, err = withInferredBundleState(dir, state)
	if err != nil {
		return append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("cannot infer bundle state: %v", err)})
	}
	bundleDir, err := deploymentBundleDir(dir)
	if err != nil {
		return append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("cannot resolve bundle dir: %v", err)})
	}
	for _, root := range state.Bundle.Roots {
		if root.Provider != python.ProviderName {
			continue
		}
		switch root.Kind {
		case "source":
			findings = append(findings, DoctorFinding{Status: "fail", Message: "persistent source roots are not installable; add a package or wheel artifact instead: " + root.Source})
		case "wheel":
			if !strings.HasPrefix(root.Source, "/bundle/") {
				findings = append(findings, DoctorFinding{Status: "fail", Message: "wheel root must live in deployment bundle for install: " + root.Source})
				continue
			}
			wheelPath := filepath.Join(bundleDir, strings.TrimPrefix(root.Source, "/bundle/"))
			if _, err := os.Stat(wheelPath); err != nil {
				if os.IsNotExist(err) {
					findings = append(findings, DoctorFinding{Status: "fail", Message: "wheel root is missing from deployment bundle: " + wheelPath})
				} else {
					findings = append(findings, DoctorFinding{Status: "fail", Message: fmt.Sprintf("cannot inspect wheel root: %s: %v", wheelPath, err)})
				}
				continue
			}
			findings = append(findings, DoctorFinding{Status: "ok", Message: "wheel root exists: " + wheelPath})
		}
	}
	if !hasDoctorFailure(findings) {
		findings = append(findings, DoctorFinding{Status: "ok", Message: "preinstall checks passed"})
	}
	return findings
}

func hasDoctorFailure(findings []DoctorFinding) bool {
	for _, finding := range findings {
		if finding.Status == "fail" {
			return true
		}
	}
	return false
}
