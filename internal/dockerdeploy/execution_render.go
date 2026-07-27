package dockerdeploy

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type DockerRenderedInputs struct {
	Compose     []byte
	Environment map[string]string
	DryRun      []string
	Status      DockerPlanStatus
	Control     DockerControlInput
}

type DockerPlanStatus struct {
	Environment string
	Phase       string
	Scope       string
	Image       string
	Container   string
	Endpoints   []string
}

type DockerControlInput struct {
	Script      string
	Environment string
	HasWorkload bool
}

type composePlanDocument struct {
	Name     string                        `yaml:"name"`
	Services map[string]composePlanService `yaml:"services"`
	Networks map[string]composePlanNetwork `yaml:"networks"`
	Volumes  map[string]any                `yaml:"volumes,omitempty"`
}

type composePlanNetwork struct {
	Name string `yaml:"name"`
}

type composePlanService struct {
	Image         string             `yaml:"image"`
	PullPolicy    string             `yaml:"pull_policy"`
	ContainerName string             `yaml:"container_name"`
	User          string             `yaml:"user"`
	Restart       string             `yaml:"restart,omitempty"`
	Entrypoint    []string           `yaml:"entrypoint,omitempty,flow"`
	Command       []string           `yaml:"command,omitempty,flow"`
	StdinOpen     bool               `yaml:"stdin_open,omitempty"`
	Volumes       []composePlanMount `yaml:"volumes,omitempty"`
	Ports         []string           `yaml:"ports,omitempty"`
	ReadOnly      bool               `yaml:"read_only"`
	Environment   map[string]string  `yaml:"environment"`
	Tmpfs         []string           `yaml:"tmpfs"`
}

type composePlanMount struct {
	Type     string `yaml:"type"`
	Source   string `yaml:"source,omitempty"`
	Target   string `yaml:"target"`
	ReadOnly bool   `yaml:"read_only,omitempty"`
}

const privateWorkloadEnvironmentLauncherV1 = `set -eu
reploy_private_environment_ready=
while IFS= read -r reploy_private_environment_line; do
  if [ -z "$reploy_private_environment_line" ]; then
    reploy_private_environment_ready=1
    break
  fi
  export "$reploy_private_environment_line"
done
[ "$reploy_private_environment_ready" = 1 ] || exit 70
exec "$@" </dev/null
`

func RenderDockerInputs(plan DockerExecutionPlan, controlScript string) (DockerRenderedInputs, error) {
	if controlScript == "" {
		return DockerRenderedInputs{}, fmt.Errorf("control script is required")
	}
	service := composePlanService{
		Image: plan.Image, PullPolicy: "never", ContainerName: plan.ContainerName, User: plan.RuntimeUser.DockerUser, Restart: plan.Restart,
		ReadOnly: true, Environment: temporaryEnvironmentForPlan(plan), Tmpfs: []string{temporaryHomeMountForPlan(plan)},
	}
	if plan.Workload != nil {
		service.Command = append([]string(nil), plan.Workload.Argv...)
	}
	if plan.PrivateEnvironment {
		if plan.Workload == nil {
			return DockerRenderedInputs{}, fmt.Errorf("private workload environment injection requires a workload")
		}
		if strings.TrimSpace(plan.Restart) != "" && strings.TrimSpace(plan.Restart) != "no" {
			return DockerRenderedInputs{}, fmt.Errorf("private workload environment injection does not support Docker restart policy %q", plan.Restart)
		}
		composeLauncher := strings.ReplaceAll(privateWorkloadEnvironmentLauncherV1, "$", "$$")
		service.Entrypoint = []string{"/bin/sh", "-c", composeLauncher, "reploy-private-environment"}
		service.StdinOpen = true
	}
	volumes := map[string]any{}
	for _, mount := range plan.Mounts {
		item := composePlanMount{Type: string(mount.Mode), Source: mount.Source, Target: mount.Target, ReadOnly: mount.ReadOnly}
		switch mount.Mode {
		case "managed-bind", "bind":
			item.Type = "bind"
		case "volume":
			volumes[mount.Source] = map[string]any{"name": mount.Source}
		case "tmpfs":
			item.Source = ""
		default:
			return DockerRenderedInputs{}, fmt.Errorf("unsupported rendered mount mode %q", mount.Mode)
		}
		service.Volumes = append(service.Volumes, item)
	}
	endpointNames := []string{}
	if plan.Workload != nil {
		for name := range plan.Workload.Endpoints {
			endpointNames = append(endpointNames, name)
		}
		sort.Strings(endpointNames)
		for _, name := range endpointNames {
			endpoint := plan.Workload.Endpoints[name]
			service.Ports = append(service.Ports, fmt.Sprintf("%s:%d:%d", endpoint.PublishAddress, endpoint.PublishedPort, endpoint.ContainerPort))
		}
	}
	document := composePlanDocument{
		Name: plan.NetworkName, Services: map[string]composePlanService{"environment": service},
		Networks: map[string]composePlanNetwork{"default": {Name: plan.NetworkName}}, Volumes: volumes,
	}
	compose, err := yaml.Marshal(document)
	if err != nil {
		return DockerRenderedInputs{}, err
	}
	scope := ""
	if plan.Scope != nil {
		scope = string(*plan.Scope)
	}
	environment := map[string]string{
		"REPLOY_ENVIRONMENT_ID": plan.EnvironmentID,
		"REPLOY_PHASE":          string(plan.Phase),
		"REPLOY_IMAGE":          plan.Image,
		"REPLOY_CONTAINER_NAME": plan.ContainerName,
		"REPLOY_DOCKER_USER":    plan.RuntimeUser.DockerUser,
	}
	if scope != "" {
		environment["REPLOY_SCOPE"] = scope
	}
	for _, name := range endpointNames {
		endpoint := plan.Workload.Endpoints[name]
		suffix := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
		environment["REPLOY_ENDPOINT_"+suffix+"_HOST"] = endpoint.PublishAddress
		environment["REPLOY_ENDPOINT_"+suffix+"_PORT"] = strconv.Itoa(endpoint.PublishedPort)
	}
	dryRun := []string{fmt.Sprintf("would use generated image %s", plan.Image)}
	if plan.Workload != nil {
		dryRun = append(dryRun, fmt.Sprintf("would manage workload container %s", plan.ContainerName))
	}
	statusEndpoints := make([]string, 0, len(endpointNames))
	for _, name := range endpointNames {
		endpoint := plan.Workload.Endpoints[name]
		statusEndpoints = append(statusEndpoints, fmt.Sprintf("%s=%s://%s:%d", name, endpoint.Scheme, endpoint.PublishAddress, endpoint.PublishedPort))
	}
	return DockerRenderedInputs{
		Compose: compose, Environment: environment, DryRun: dryRun,
		Status:  DockerPlanStatus{Environment: plan.EnvironmentID, Phase: string(plan.Phase), Scope: scope, Image: plan.Image, Container: plan.ContainerName, Endpoints: statusEndpoints},
		Control: DockerControlInput{Script: controlScript, Environment: plan.EnvironmentID, HasWorkload: plan.Workload != nil},
	}, nil
}

func temporaryHomeForPlan(plan DockerExecutionPlan) string {
	if strings.TrimSpace(plan.TemporaryHome) == "" {
		return environmentTemporaryHome
	}
	return plan.TemporaryHome
}

func temporaryHomeMountForPlan(plan DockerExecutionPlan) string {
	return temporaryHomeForPlan(plan) + ":rw,noexec,nosuid,nodev,size=64m,mode=1777"
}

func transientHomeMountForPlan(plan DockerExecutionPlan) string {
	return "type=volume,destination=" + temporaryHomeForPlan(plan) + ",volume-nocopy"
}

func temporaryEnvironmentForPlan(plan DockerExecutionPlan) map[string]string {
	home := temporaryHomeForPlan(plan)
	return map[string]string{"HOME": home, "TMPDIR": home}
}
