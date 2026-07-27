package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/omry/reploy/internal/deploy"
)

const (
	ReployInternalDir    = ".reploy"
	DockerEnvFileName    = ".reploy/docker.env"
	RuntimeDirName       = ".reploy/runtime"
	ComposeFileName      = RuntimeDirName + "/compose.yaml"
	StateFileName        = ".reploy/state.json"
	ToolBinaryFileName   = ".reploy/bin/reploy"
	DefaultDeploymentDir = "reploy-staging"

	reployDeploymentScopeEnv      = "REPLOY_DEPLOYMENT_SCOPE"
	reployDeploymentScopeStaging  = "staging"
	reployDeploymentScopeDeployed = "deployed"
	reployInstallOwnerEnv         = "REPLOY_INSTALL_OWNER"
	reployInstallOwnerOnMissing   = "REPLOY_INSTALL_OWNER_ON_MISSING"
)

type UpdateResult struct {
	Path      string
	Status    deploy.UpdateStatus
	Ownership string
	Reason    string
}

func RequireStagingDeployment(dir string) error {
	content, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		return fmt.Errorf("read staging state: %w", err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		return err
	}
	if state.Deployment != nil {
		return fmt.Errorf("deployment is installed, not staged: %s", dir)
	}
	return nil
}
