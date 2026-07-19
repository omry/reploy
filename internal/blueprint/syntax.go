package blueprint

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// Syntax is the structurally decoded blueprint before interpolation, extends,
// defaults, and cross-reference resolution.
type Syntax struct {
	Blueprint   MetadataSyntax    `yaml:"blueprint"`
	Environment EnvironmentSyntax `yaml:"environment"`
	Docker      DockerSyntax      `yaml:"docker"`
}

type MetadataSyntax struct {
	Schema         int    `yaml:"schema"`
	Version        string `yaml:"version"`
	RequiresReploy string `yaml:"requires_reploy"`
}

type EnvironmentSyntax struct {
	ID            string                       `yaml:"id"`
	ControlScript string                       `yaml:"control_script"`
	Vars          map[string]any               `yaml:"vars"`
	Translations  map[string]TranslationSyntax `yaml:"translations"`
	Components    map[string]ComponentSyntax   `yaml:"components"`
	Terminal      TerminalSyntax               `yaml:"terminal"`
	Install       InstallSyntax                `yaml:"install"`
	Paths         map[string]PathSyntax        `yaml:"paths"`
	Executables   map[string]ExecutableSyntax  `yaml:"executables"`
	Commands      map[string]CommandSyntax     `yaml:"commands"`
	Workload      *WorkloadSyntax              `yaml:"workload"`
}

type TerminalSyntax struct {
	ColorEnv string `yaml:"color_env"`
}

type TranslationSyntax struct {
	Type     string            `yaml:"type"`
	Scope    string            `yaml:"scope"`
	Root     string            `yaml:"root"`
	Mappings map[string]string `yaml:"mappings"`
}

type ComponentSyntax struct {
	Type         string                   `yaml:"type"`
	Optional     *OptionalComponentSyntax `yaml:"optional"`
	Requirements []string                 `yaml:"requirements"`
}

type OptionalComponentSyntax struct {
	Group       string `yaml:"group"`
	Description string `yaml:"description"`
}

type PathSyntax struct {
	Container string `yaml:"container"`
	Writable  any    `yaml:"writable"`
	Update    string `yaml:"update"`
}

type ExecutableSyntax struct {
	Component  string   `yaml:"component"`
	Binary     string   `yaml:"binary"`
	Order      []string `yaml:"order"`
	ArgvPrefix []string `yaml:"argv_prefix"`
	ArgvSuffix []string `yaml:"argv_suffix"`
}

type CommandSyntax struct {
	Executable      string   `yaml:"executable"`
	Trigger         []string `yaml:"trigger"`
	NativeCommand   any      `yaml:"native_command"`
	DeployedCommand any      `yaml:"deployed_command"`
	ForwardFlags    []string `yaml:"forward_flags"`
	Argv            []string `yaml:"argv"`
	Order           []string `yaml:"order"`
}

type WorkloadSyntax struct {
	Command   string                    `yaml:"command"`
	Endpoints map[string]EndpointSyntax `yaml:"endpoints"`
	Runtime   RuntimeEventsSyntax       `yaml:"runtime"`
}

type EndpointSyntax struct {
	Scheme    string           `yaml:"scheme"`
	Port      any              `yaml:"port"`
	Readiness *ReadinessSyntax `yaml:"readiness"`
}

type ReadinessSyntax struct {
	Path      string `yaml:"path"`
	Timeout   string `yaml:"timeout"`
	Interval  string `yaml:"interval"`
	TLSVerify any    `yaml:"tls_verify"`
}

type RuntimeEventsSyntax struct {
	BeforeStart []StepSyntax `yaml:"before_start"`
	AfterStart  []StepSyntax `yaml:"after_start"`
	BeforeStop  []StepSyntax `yaml:"before_stop"`
	AfterStop   []StepSyntax `yaml:"after_stop"`
}

type StepSyntax struct {
	Requires RequirementsSyntax `yaml:"requires"`
	Actions  []ActionSyntax     `yaml:"actions"`
}

type RequirementsSyntax struct {
	Endpoints []string `yaml:"endpoints"`
}

type ActionSyntax struct {
	Environment []string `yaml:"environment"`
}

type InstallSyntax struct {
	Target       InstallTargetSyntax  `yaml:"target"`
	System       SystemInstallSyntax  `yaml:"system"`
	AfterInstall []StepSyntax         `yaml:"after_install"`
	Success      InstallSuccessSyntax `yaml:"success"`
}

type InstallTargetSyntax struct {
	DefaultPath  string            `yaml:"default_path"`
	DefaultPaths map[string]string `yaml:"default_paths"`
}

type SystemInstallSyntax struct {
	RunAs RunAsSyntax `yaml:"run_as"`
}

type RunAsSyntax struct {
	User      string `yaml:"user"`
	Group     string `yaml:"group"`
	OnMissing string `yaml:"on_missing"`
}

type InstallSuccessSyntax struct {
	Lines []string `yaml:"lines"`
}

type DockerSyntax struct {
	Image    string                       `yaml:"image"`
	Mounts   map[string]DockerMountSyntax `yaml:"mounts"`
	Workload *DockerWorkloadSyntax        `yaml:"workload"`
}

type DockerMountSyntax struct {
	Extends string `yaml:"extends"`
	Mode    string `yaml:"mode"`
	Source  string `yaml:"source"`
	Name    string `yaml:"name"`
}

type DockerWorkloadSyntax struct {
	Restart   string                          `yaml:"restart"`
	Endpoints map[string]DockerEndpointSyntax `yaml:"endpoints"`
}

type DockerEndpointSyntax struct {
	Extends string            `yaml:"extends"`
	Bind    BindSyntax        `yaml:"bind"`
	Publish PublicationSyntax `yaml:"publish"`
}

type BindSyntax struct {
	Address string `yaml:"address"`
}

type PublicationSyntax struct {
	Address  string `yaml:"address"`
	Staging  any    `yaml:"staging"`
	Deployed any    `yaml:"deployed"`
}

func Decode(data []byte) (Syntax, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var source Syntax
	if err := decoder.Decode(&source); err != nil {
		return Syntax{}, fmt.Errorf("decode blueprint: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Syntax{}, fmt.Errorf("decode blueprint: multiple YAML documents are not supported")
		}
		return Syntax{}, fmt.Errorf("decode blueprint: %w", err)
	}
	if source.Blueprint.Schema != 1 {
		return Syntax{}, fmt.Errorf("blueprint.schema must be 1")
	}
	return source, nil
}
