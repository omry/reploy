package blueprint

import (
	"strings"
	"time"
)

// Document is the fully resolved schema-1 blueprint consumed by Reploy.
// Parsing retains lazy expressions separately and produces this typed form only
// when the values required by the current operation are available.
type Document struct {
	Blueprint   Metadata
	Environment Environment
	Docker      Docker
}

type Metadata struct {
	Schema         int
	Version        string
	RequiresReploy string
	Compatibility  Compatibility
}

type Environment struct {
	ID              string
	ControlScript   string
	Vars            map[string]any
	Components      map[string]Component
	AllowConcurrent ConcurrentRunPolicy
	Terminal        Terminal
	Install         Install
	Mounts          map[string]EnvironmentMount
	Commands        map[string]Command
	Workload        *Workload
}

type ConcurrentRunPolicy string

const (
	ConcurrentRunAuto ConcurrentRunPolicy = "auto"
	ConcurrentRunYes  ConcurrentRunPolicy = "yes"
	ConcurrentRunNo   ConcurrentRunPolicy = "no"
)

type Terminal struct {
	ColorEnv string
}

type ComponentType string

const (
	ComponentTypeBase   ComponentType = "base"
	ComponentTypePython ComponentType = "python"
	ComponentTypeAPT    ComponentType = "apt"
)

type Component struct {
	Type        ComponentType
	Base        *BaseComponent
	Python      *PythonComponent
	APT         *APTComponent
	Options     map[string]ComponentOption
	Executables map[string]Executable
}

type BaseComponent struct {
	Image   string
	Exports map[string]BaseExecutableExport
}

type BaseExecutableExport struct {
	Executable string
}

type PythonComponent struct {
	Interpreter  CommandRequirement
	Requirements []string
}

type APTComponent struct {
	Packages []APTPackageRequest
}

type ComponentOption struct {
	Description        string
	PythonRequirements []string
	APTPackages        []APTPackageRequest
}

type EnvironmentMount struct {
	Target       string
	Writable     bool
	UpdatePolicy UpdatePolicy
}

type UpdatePolicy string

const (
	UpdatePreserve  UpdatePolicy = "preserve"
	UpdateReplace   UpdatePolicy = "replace"
	UpdateUnmanaged UpdatePolicy = "unmanaged"
)

type Executable struct {
	Binary     string
	Order      []ArgumentSegment
	ArgvPrefix []string
	ArgvSuffix []string
}

type ArgumentSegment string

const (
	ArgumentBinary    ArgumentSegment = "binary"
	ArgumentPrefix    ArgumentSegment = "prefix"
	ArgumentCommand   ArgumentSegment = "command"
	ArgumentForwarded ArgumentSegment = "forwarded"
	ArgumentSuffix    ArgumentSegment = "suffix"
)

var DefaultArgumentOrder = []ArgumentSegment{
	ArgumentBinary,
	ArgumentPrefix,
	ArgumentCommand,
	ArgumentForwarded,
	ArgumentSuffix,
}

type Command struct {
	Executable      string
	Trigger         []string
	NativeCommand   bool
	DeployedCommand bool
	ForwardFlags    []string
	Argv            []string
	Order           []ArgumentSegment
}

func (environment Environment) ResolveExecutableProfile(reference string) (string, Executable, bool) {
	componentName, profileName, found := strings.Cut(reference, ".")
	if !found || componentName == "" || profileName == "" || strings.Contains(profileName, ".") {
		return "", Executable{}, false
	}
	component, found := environment.Components[componentName]
	if !found {
		return "", Executable{}, false
	}
	profile, found := component.Executables[profileName]
	return componentName, profile, found
}

type Workload struct {
	Command   string
	Endpoints map[string]Endpoint
	Runtime   RuntimeEvents
}

type Endpoint struct {
	Scheme    string
	Port      int
	Readiness *Readiness
}

type Readiness struct {
	Path      string
	Timeout   time.Duration
	Interval  time.Duration
	TLSVerify bool
}

const (
	DefaultReadinessTimeout  = 30 * time.Second
	DefaultReadinessInterval = time.Second
)

type RuntimeEvents struct {
	BeforeStart []Step
	AfterStart  []Step
	BeforeStop  []Step
	AfterStop   []Step
}

type Step struct {
	Requires Requirements
	Actions  []Action
}

type Requirements struct {
	Endpoints []string
}

type Action struct {
	Environment []string
}

type Install struct {
	Target       InstallTarget
	System       SystemInstall
	AfterInstall []Step
	Success      InstallSuccess
}

type InstallTarget struct {
	DefaultPath  string
	DefaultPaths map[string]string
}

type SystemInstall struct {
	RunAs RunAs
}

type RunAs struct {
	User      string
	Group     string
	OnMissing string
}

type InstallSuccess struct {
	Lines []string
}

type Docker struct {
	Image    string
	Mounts   map[string]DockerMount
	Workload *DockerWorkload
}

type MountMode string

const (
	MountManagedBind MountMode = "managed-bind"
	MountBind        MountMode = "bind"
	MountVolume      MountMode = "volume"
	MountTmpfs       MountMode = "tmpfs"
)

type DockerMount struct {
	Extends  string
	Mode     MountMode
	Source   string
	Name     string
	Contract EnvironmentMount
}

type DockerWorkload struct {
	Restart   string
	Endpoints map[string]DockerEndpoint
}

type DockerEndpoint struct {
	Extends  string
	Bind     Bind
	Publish  Publication
	Endpoint Endpoint
}

type Bind struct {
	Address string
}

type Publication struct {
	Address  string
	Staging  int
	Deployed int
}

type Phase string

const (
	PhaseStaged    Phase = "staged"
	PhaseInstalled Phase = "installed"
)

type InstallScope string

const (
	InstallScopeUser   InstallScope = "user"
	InstallScopeSystem InstallScope = "system"
)
