package dockerdeploy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/distribution/reference"
	dockerconfig "github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/credentials"
)

// Resolve only the selected registry with Docker's maintained config/helper
// implementation. Never copy credential files or forward all stored logins.
func podmanBaseRegistryAuth(named reference.Named) ([]byte, error) {
	directory := os.Getenv("DOCKER_CONFIG")
	if directory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("locate Docker registry credential configuration")
		}
		directory = filepath.Join(home, ".docker")
	}
	config, err := dockerconfig.Load(directory)
	if err != nil {
		return nil, fmt.Errorf("read Docker registry credential configuration")
	}
	if !config.ContainsAuth() {
		config.CredentialsStore = credentials.DetectDefaultStore(config.CredentialsStore)
	}
	registry := reference.Domain(named)
	if registry == "docker.io" {
		registry = "https://index.docker.io/v1/"
	}
	auth, err := config.GetAuthConfig(registry)
	if err != nil {
		return nil, fmt.Errorf("resolve selected Docker registry credentials")
	}
	return json.Marshal(auth)
}
