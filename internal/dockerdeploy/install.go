package dockerdeploy

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/deploy"
)

type InstallOptions struct {
	Dir     string
	Target  string
	Service string
	Start   bool
	DryRun  bool
	Stdout  io.Writer
}

type installPlan struct {
	SourceDir       string
	TargetDir       string
	Service         string
	UnitPath        string
	Start           bool
	ComposeOverride bool
}

const defaultSystemdUnitDir = "/etc/systemd/system"

var installGeteuid = os.Geteuid
var installLookPath = exec.LookPath
var installRunCommand = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
var installSystemdUnitDir = defaultSystemdUnitDir

func Install(options InstallOptions) error {
	plan, err := newInstallPlan(options)
	if err != nil {
		return err
	}
	doctorCode := Doctor(DoctorOptions{Dir: options.Dir, Preinstall: true})
	if doctorCode != 0 {
		return fmt.Errorf("preinstall doctor failed")
	}
	if options.DryRun {
		printInstallDryRun(options.Stdout, plan)
		return nil
	}
	if installGeteuid() != 0 {
		return fmt.Errorf("install requires root unless --dry-run is set")
	}
	return applyInstallPlan(plan)
}

func newInstallPlan(options InstallOptions) (installPlan, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	if strings.TrimSpace(options.Target) == "" {
		return installPlan{}, fmt.Errorf("--to is required")
	}
	target, err := filepath.Abs(options.Target)
	if err != nil {
		return installPlan{}, err
	}
	if !filepath.IsAbs(options.Target) {
		return installPlan{}, fmt.Errorf("--to must be an absolute path: %s", options.Target)
	}
	if strings.ContainsAny(target, " \t\n") {
		return installPlan{}, fmt.Errorf("--to must not contain whitespace: %s", target)
	}
	if options.Service == "" {
		service, err := defaultInstallService(options.Dir)
		if err != nil {
			return installPlan{}, err
		}
		options.Service = service
	}
	if options.Service != "" && !validServiceName(options.Service) {
		return installPlan{}, fmt.Errorf("--service contains unsupported characters: %s", options.Service)
	}
	absoluteDir, err := filepath.Abs(options.Dir)
	if err != nil {
		return installPlan{}, err
	}
	_, overrideErr := os.Stat(filepath.Join(absoluteDir, ComposeOverrideFileName))
	if overrideErr != nil && !os.IsNotExist(overrideErr) {
		return installPlan{}, overrideErr
	}
	return installPlan{
		SourceDir:       absoluteDir,
		TargetDir:       target,
		Service:         options.Service,
		UnitPath:        filepath.Join(installSystemdUnitDir, options.Service+".service"),
		Start:           options.Start,
		ComposeOverride: overrideErr == nil,
	}, nil
}

func defaultInstallService(dir string) (string, error) {
	state, err := loadState(dir)
	if err != nil {
		return "", err
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return "", err
	}
	service := dockerNameSlug(pack.AppID, "reploy")
	if !validServiceName(service) {
		return "", fmt.Errorf("app id cannot be used as a systemd service name: %s", pack.AppID)
	}
	return service, nil
}

func printInstallDryRun(stdout io.Writer, plan installPlan) {
	if stdout == nil {
		return
	}
	fmt.Fprintf(stdout, "would install deployment: %s\n", plan.SourceDir)
	fmt.Fprintf(stdout, "target: %s\n", plan.TargetDir)
	fmt.Fprintf(stdout, "service: %s\n", plan.Service)
	fmt.Fprintf(stdout, "would write systemd unit: %s\n", plan.UnitPath)
	fmt.Fprintln(stdout, "would run: systemctl daemon-reload")
	fmt.Fprintf(stdout, "would run: systemctl enable %s.service\n", plan.Service)
	if plan.Start {
		fmt.Fprintf(stdout, "would run: systemctl restart %s.service\n", plan.Service)
		fmt.Fprintf(stdout, "would run: %s test\n", filepath.Join(plan.TargetDir, "reploy"))
		fmt.Fprintf(stdout, "would run: %s app config check --live\n", filepath.Join(plan.TargetDir, "reploy"))
	} else {
		fmt.Fprintln(stdout, "start: no")
	}
}

func validServiceName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_', r == '.', r == '@', r == '-':
		default:
			return false
		}
	}
	return true
}

func copyDeploymentTreeProtected(sourceDir string, targetDir string) error {
	sourceDir, err := filepath.Abs(sourceDir)
	if err != nil {
		return err
	}
	targetDir, err = filepath.Abs(targetDir)
	if err != nil {
		return err
	}
	return filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to copy symlink: %s", path)
		}
		relativePath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(targetDir, relativePath)
		if relativePath == "." {
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}
		if entry.IsDir() {
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to copy special file: %s", path)
		}
		return copyInstallFile(path, targetPath, info.Mode().Perm())
	})
}

func copyInstallFile(sourcePath string, targetPath string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	if err := target.Close(); err != nil {
		return err
	}
	return os.Chmod(targetPath, mode)
}

func systemdUnit(plan installPlan, dockerBin string, includeDockerUnit bool) string {
	dockerUnitLines := ""
	if includeDockerUnit {
		dockerUnitLines = "Requires=docker.service\nAfter=docker.service\n"
	}
	composeFiles := "--project-directory " + plan.TargetDir + " -f " + filepath.Join(plan.TargetDir, ComposeFileName)
	if plan.ComposeOverride {
		composeFiles += " -f " + filepath.Join(plan.TargetDir, ComposeOverrideFileName)
	}
	return fmt.Sprintf(`[Unit]
Description=Reploy Docker service (%s)
%s
[Service]
Type=simple
WorkingDirectory=%s
ExecStartPre=/bin/sh -c 'i=0; while [ "$i" -lt 120 ]; do [ -x "$1" ] && "$1" info >/dev/null 2>&1 && exit 0; i=$((i + 1)); sleep 1; done; echo "error: Docker API did not become ready for Reploy" >&2; exit 1' reploy-docker-ready %s
ExecStart=%s compose --env-file %s %s up
ExecStop=%s compose --env-file %s %s down
Restart=on-failure
RestartSec=10
TimeoutStartSec=180

[Install]
WantedBy=multi-user.target
`, plan.Service, dockerUnitLines, plan.TargetDir, dockerBin, dockerBin, filepath.Join(plan.TargetDir, DockerEnvFileName), composeFiles, dockerBin, filepath.Join(plan.TargetDir, DockerEnvFileName), composeFiles)
}

func writeInstalledState(dir string) error {
	state, err := loadState(dir)
	if err != nil {
		return err
	}
	state, err = withInferredBundleState(dir, state)
	if err != nil {
		return err
	}
	state.Phase = deploy.PhaseInstalled
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, StateFileName), append(content, '\n'), 0o644)
}

func applyInstallPlan(plan installPlan) error {
	if err := copyDeploymentTreeProtected(plan.SourceDir, plan.TargetDir); err != nil {
		return fmt.Errorf("copy deployment: %w", err)
	}
	if err := writeInstalledState(plan.TargetDir); err != nil {
		return fmt.Errorf("mark deployment installed: %w", err)
	}
	dockerBin, err := installLookPath("docker")
	if err != nil {
		return fmt.Errorf("docker command not found: %w", err)
	}
	systemctlBin, err := installLookPath("systemctl")
	if err != nil {
		return fmt.Errorf("systemctl command not found: %w", err)
	}
	includeDockerUnit := installRunCommand(systemctlBin, "cat", "docker.service") == nil
	if err := os.MkdirAll(filepath.Dir(plan.UnitPath), 0o755); err != nil {
		return err
	}
	unit := systemdUnit(plan, dockerBin, includeDockerUnit)
	if err := os.WriteFile(plan.UnitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	if err := os.Chmod(plan.UnitPath, 0o644); err != nil {
		return err
	}
	if err := installRunCommand(systemctlBin, "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if err := installRunCommand(systemctlBin, "enable", plan.Service+".service"); err != nil {
		return fmt.Errorf("systemctl enable %s.service: %w", plan.Service, err)
	}
	if plan.Start {
		if err := installRunCommand(systemctlBin, "restart", plan.Service+".service"); err != nil {
			return fmt.Errorf("systemctl restart %s.service: %w", plan.Service, err)
		}
		if err := runInstalledPostStartChecks(plan); err != nil {
			return err
		}
	}
	return nil
}

func runInstalledPostStartChecks(plan installPlan) error {
	helper := filepath.Join(plan.TargetDir, "reploy")
	if err := installRunCommand(helper, "test"); err != nil {
		return fmt.Errorf("installed server test: %w", err)
	}
	if err := installRunCommand(helper, "app", "config", "check", "--live"); err != nil {
		return fmt.Errorf("installed live config check: %w", err)
	}
	return nil
}
