package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	runtimeOutputDirectoryVariable = "REPLOY_OUTPUT_DIR"
	runtimeOutputFileVariable      = "REPLOY_OUTPUT_FILE"
	runtimeOutputFileName          = "output"
)

type transientOutputMount struct {
	HostDirectory string
	Variable      string
	ContainerPath string
}

type oneShotOutputSession struct {
	mount       *transientOutputMount
	stagingDir  string
	stagingFile string
	finalFile   string
}

type oneShotOutputBackend struct {
	currentUID func() uint32
	currentGID func() uint32
	chown      func(string, uint32, uint32) error
}

func prepareOneShotOutput(outputDir string, outputFile string, runtimeUser RuntimeUserPlan) (*oneShotOutputSession, error) {
	return prepareOneShotOutputWithBackend(outputDir, outputFile, runtimeUser, oneShotOutputOwnershipBackend())
}

func prepareOneShotOutputWithBackend(
	outputDir string,
	outputFile string,
	runtimeUser RuntimeUserPlan,
	backend oneShotOutputBackend,
) (*oneShotOutputSession, error) {
	if outputDir != "" && outputFile != "" {
		return nil, fmt.Errorf("--output-dir and --output-file are mutually exclusive")
	}
	if outputDir == "" && outputFile == "" {
		return &oneShotOutputSession{}, nil
	}
	if runtimeUser.UID == 0 {
		return nil, fmt.Errorf("root application runtime cannot use --output-dir or --output-file until the root-safe output contract is implemented")
	}
	if backend.currentUID == nil || backend.currentGID == nil || backend.chown == nil {
		return nil, fmt.Errorf("prepare one-shot output requires a complete ownership backend")
	}
	if outputDir != "" {
		absolute, err := filepath.Abs(outputDir)
		if err != nil {
			return nil, fmt.Errorf("resolve output directory: %w", err)
		}
		if err := os.MkdirAll(absolute, 0o755); err != nil {
			return nil, fmt.Errorf("create output directory: %w", err)
		}
		info, err := os.Stat(absolute)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("output directory is not a directory: %s", absolute)
		}
		return &oneShotOutputSession{mount: &transientOutputMount{
			HostDirectory: absolute, Variable: runtimeOutputDirectoryVariable, ContainerPath: runtimeOutputRoot,
		}}, nil
	}

	finalFile, err := filepath.Abs(outputFile)
	if err != nil {
		return nil, fmt.Errorf("resolve output file: %w", err)
	}
	parent := filepath.Dir(finalFile)
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return nil, fmt.Errorf("output file parent is not a directory: %s", parent)
	}
	if _, err := os.Lstat(finalFile); err == nil {
		return nil, fmt.Errorf("output file already exists: %s", finalFile)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect output file: %w", err)
	}
	stagingDir := filepath.Join(parent, "."+filepath.Base(finalFile)+".reploy-output")
	if err := os.Mkdir(stagingDir, 0o700); err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("output file is reserved by existing Reploy staging directory: %s", stagingDir)
		}
		return nil, fmt.Errorf("reserve output file: %w", err)
	}
	if runtimeUser.UID == runtimeIDUnchangedSentinelV1 || runtimeUser.GID == runtimeIDUnchangedSentinelV1 {
		_ = os.Remove(stagingDir)
		return nil, fmt.Errorf("reserve output file requires a numeric runtime user")
	}
	if runtimeUser.UID != backend.currentUID() || runtimeUser.GID != backend.currentGID() {
		if err := backend.chown(stagingDir, runtimeUser.UID, runtimeUser.GID); err != nil {
			_ = os.Remove(stagingDir)
			return nil, fmt.Errorf("make output reservation writable by runtime user %d:%d: %w", runtimeUser.UID, runtimeUser.GID, err)
		}
	}
	info, err := os.Lstat(stagingDir)
	if err != nil {
		_ = os.RemoveAll(stagingDir)
		return nil, fmt.Errorf("verify output reservation: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(stagingDir)
		return nil, fmt.Errorf("output reservation was replaced by a symbolic link: %s", stagingDir)
	}
	if !info.IsDir() {
		_ = os.RemoveAll(stagingDir)
		return nil, fmt.Errorf("output reservation is not a directory: %s", stagingDir)
	}
	return &oneShotOutputSession{
		mount: &transientOutputMount{
			HostDirectory: stagingDir, Variable: runtimeOutputFileVariable,
			ContainerPath: runtimeOutputRoot + "/" + runtimeOutputFileName,
		},
		stagingDir: stagingDir, stagingFile: filepath.Join(stagingDir, runtimeOutputFileName), finalFile: finalFile,
	}, nil
}

func (session *oneShotOutputSession) abort() error {
	if session == nil || session.stagingDir == "" {
		return nil
	}
	return os.RemoveAll(session.stagingDir)
}

func (session *oneShotOutputSession) publish() error {
	if session == nil || session.stagingDir == "" {
		return nil
	}
	info, err := os.Lstat(session.stagingFile)
	if err != nil {
		return fmt.Errorf("one-shot command did not create %s: %w", runtimeOutputFileVariable, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must name one regular file", runtimeOutputFileVariable)
	}
	if err := os.Link(session.stagingFile, session.finalFile); err != nil {
		return fmt.Errorf("publish output file without overwriting its destination: %w", err)
	}
	if err := os.RemoveAll(session.stagingDir); err != nil {
		return fmt.Errorf("output file was published but staging cleanup failed: %w", err)
	}
	return nil
}
