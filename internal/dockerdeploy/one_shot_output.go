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

func prepareOneShotOutput(outputDir string, outputFile string) (*oneShotOutputSession, error) {
	if outputDir != "" && outputFile != "" {
		return nil, fmt.Errorf("--output-dir and --output-file are mutually exclusive")
	}
	if outputDir == "" && outputFile == "" {
		return &oneShotOutputSession{}, nil
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
