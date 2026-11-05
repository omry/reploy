package deploy

import (
	"fmt"
	"net"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
)

// DockerOverridesV1 deliberately supports only a named endpoint's host address.
// It cannot change container bindings or either phase's ports.
type DockerOverridesV1 struct {
	Workload DockerWorkloadOverridesV1 `yaml:"workload,omitempty"`
}
type DockerWorkloadOverridesV1 struct {
	Endpoints map[string]DockerEndpointOverrideV1 `yaml:"endpoints,omitempty"`
}
type DockerEndpointOverrideV1 struct {
	Publish DockerPublicationOverrideV1 `yaml:"publish"`
}
type DockerPublicationOverrideV1 struct {
	Address string `yaml:"address"`
}

func (overrides PackageOverridesV1) PublishedAddressesV1() map[string]string {
	if len(overrides.Docker.Workload.Endpoints) == 0 {
		return nil
	}
	result := make(map[string]string, len(overrides.Docker.Workload.Endpoints))
	for name, endpoint := range overrides.Docker.Workload.Endpoints {
		result[name] = endpoint.Publish.Address
	}
	return result
}

// ValidatePublishedAddressV1 accepts Docker host IP addresses, including the
// bracketed IPv6 spelling already handled by runtime probes.
// Omission, rather than an empty value, restores the blueprint default.
func ValidatePublishedAddressV1(address string) error {
	host := address
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
		if !strings.Contains(host, ":") {
			return fmt.Errorf("brackets require an IPv6 host address")
		}
	}
	if host == "" || net.ParseIP(host) == nil {
		return fmt.Errorf("published address %q must be an IPv4 or IPv6 host address (no port); reset by removing the override", address)
	}
	return nil
}

func validatePublishedAddressesV1(addresses map[string]string) error {
	for _, name := range sortedKeys(addresses) {
		if name == "" || strings.TrimSpace(name) != name || containsControl(name) {
			return fmt.Errorf("published-address override endpoint name %q is invalid", name)
		}
		if err := ValidatePublishedAddressV1(addresses[name]); err != nil {
			return fmt.Errorf("overrides docker.workload.endpoints.%s.publish.address: %w", name, err)
		}
	}
	return nil
}

func (overrides PackageOverridesV1) CheckDeclarationsV1(document blueprint.Document) error {
	if err := overrides.CheckIdentityMappingDeclaration(document); err != nil {
		return err
	}
	_, err := ResolvePublishedAddressesV1(document, overrides.PublishedAddressesV1())
	return err
}

// ResolvePublishedAddressesV1 is the shared publication rule for staged builds,
// installed plans, and reconstruction from immutable build locks. It never
// changes the authored document or any other endpoint property.
func ResolvePublishedAddressesV1(document blueprint.Document, overrides map[string]string) (map[string]string, error) {
	if err := validatePublishedAddressesV1(overrides); err != nil {
		return nil, err
	}
	result := map[string]string{}
	if document.Docker.Workload != nil {
		for name, endpoint := range document.Docker.Workload.Endpoints {
			address := strings.TrimSpace(endpoint.Publish.Address)
			if address == "" {
				address = "127.0.0.1"
			}
			result[name] = address
		}
	}
	for _, name := range sortedKeys(overrides) {
		if _, exists := result[name]; !exists {
			return nil, fmt.Errorf("published-address override references unknown Docker workload endpoint %q", name)
		}
		result[name] = overrides[name]
	}
	return result, nil
}
