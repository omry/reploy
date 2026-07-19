package blueprint

import "time"

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
}

type Environment struct {
	ID            string
	ControlScript string
	Vars          map[string]any
	Translations  map[string]Translation
	Components    map[string]Component
	Terminal      Terminal
	Install       Install
	Paths         map[string]Path
	Executables   map[string]Executable
	Commands      map[string]Command
	Workload      *Workload
}

type Terminal struct {
	ColorEnv string
}

type Translation struct {
	Type     ComponentType
	Scope    TranslationScope
	Root     string
	Mappings map[string]string
}

type TranslationScope string

const (
	TranslationScopeDevelopment TranslationScope = "development"
)

type ComponentType string

const (
	ComponentTypePython ComponentType = "python"
)

type Component struct {
	Type         ComponentType
	Optional     *OptionalComponent
	Requirements []string
}

type OptionalComponent struct {
	Group       string
	Description string
}

type Path struct {
	Container string
	Writable  bool
	Update    UpdatePolicy
}

type UpdatePolicy string

const (
	UpdatePreserve  UpdatePolicy = "preserve"
	UpdateReplace   UpdatePolicy = "replace"
	UpdateUnmanaged UpdatePolicy = "unmanaged"
)

type Executable struct {
	Component  string
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
	Extends string
	Mode    MountMode
	Source  string
	Name    string
	Path    Path
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
