package dockerdeploy

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"time"
)

type ControlHealthOptions struct {
	Dir     string
	Stdout  io.Writer
	Timeout time.Duration
}

func ControlHealth(options ControlHealthOptions) error {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	if options.Timeout == 0 {
		options.Timeout = 5 * time.Second
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return err
	}
	stdout, _ := deploymentOutputWritersForDeployment(options.Dir, state, options.Stdout, nil)
	serverURL, err := ServerURL(options.Dir)
	if err != nil {
		return err
	}
	health, err := healthConfig(options.Dir)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout: options.Timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: !healthTLSVerify(health)},
		},
	}
	response, err := client.Get(serverURL.String())
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("health check returned HTTP %d", response.StatusCode)
	}
	if stdout != nil {
		_, err = io.Copy(stdout, response.Body)
	}
	return err
}
