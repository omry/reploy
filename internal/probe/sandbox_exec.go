package probe

import (
	"flag"
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"
)

type sandboxExecPlanV1 struct {
	UID                    uint32
	GID                    uint32
	Groups                 []uint32
	EnvironmentProfile     string
	ContractEnvironment    []EnvironmentVariableV1
	RecordExitStatus       bool
	AllowPublic            bool
	AllowLocal             bool
	AllowAmbiguous         bool
	InboundTCP             []uint16
	SessionNetworkPrefixes string
	SessionNetworkPeer     string
	InstallRules           bool
	Argv                   []string
}

func parseSandboxExecPlanV1(args []string, installRules bool) (sandboxExecPlanV1, error) {
	separator := -1
	for index, argument := range args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator == len(args)-1 {
		return sandboxExecPlanV1{}, fmt.Errorf("requires -- followed by an absolute application command")
	}
	set := flag.NewFlagSet("sandbox-exec", flag.ContinueOnError)
	set.SetOutput(new(strings.Builder))
	uid := set.String("uid", "", "application UID")
	gid := set.String("gid", "", "application GID")
	groups := set.String("groups", "", "comma-separated supplementary GIDs")
	environmentProfile := set.String("environment-profile", "", "fixed application environment profile")
	contractEnvironment := new(contractEnvironmentFlagV1)
	set.Var(contractEnvironment, "environment-entry", "additional NAME=VALUE contract runtime environment entry")
	recordExitStatus := set.Bool("record-exit-status", false, "record the observed application exit status through the fixed trusted channel")
	public := set.String("public", "", "public network policy")
	local := set.String("local", "", "local network policy")
	ambiguous := set.String("ambiguous", "", "ambiguous destination policy")
	inbound := set.String("inbound-tcp", "", "comma-separated inbound TCP ports")
	sessionNetworkPrefixes := set.String("session-network-prefixes", "", "trusted file containing session-network CIDRs and participant addresses")
	sessionNetworkPeer := set.String("session-network-peer", "", "fixed session-network peer alias")
	if err := set.Parse(args[:separator]); err != nil {
		return sandboxExecPlanV1{}, err
	}
	if len(set.Args()) != 0 {
		return sandboxExecPlanV1{}, fmt.Errorf("unexpected positional sandbox arguments")
	}
	argv := args[separator+1:]
	parsedUID, err := parseCredentialV1(*uid)
	if err != nil {
		return sandboxExecPlanV1{}, fmt.Errorf("parse --uid: %w", err)
	}
	parsedGID, err := parseCredentialV1(*gid)
	if err != nil {
		return sandboxExecPlanV1{}, fmt.Errorf("parse --gid: %w", err)
	}
	parsedGroups, err := parseCredentialListV1(*groups)
	if err != nil {
		return sandboxExecPlanV1{}, fmt.Errorf("parse --groups: %w", err)
	}
	if !installRules && *inbound != "" {
		return sandboxExecPlanV1{}, fmt.Errorf("--inbound-tcp is not accepted by restricted-exec")
	}
	if !installRules && *sessionNetworkPrefixes != "" {
		return sandboxExecPlanV1{}, fmt.Errorf("--session-network-prefixes is not accepted by restricted-exec")
	}
	if !installRules && *sessionNetworkPeer != "" {
		return sandboxExecPlanV1{}, fmt.Errorf("--session-network-peer is not accepted by restricted-exec")
	}
	if installRules && *environmentProfile != "" {
		return sandboxExecPlanV1{}, fmt.Errorf("--environment-profile is not accepted by sandbox-exec")
	}
	if !installRules && *environmentProfile != "" && *environmentProfile != PortableToolEnvironmentProfileV1 {
		return sandboxExecPlanV1{}, fmt.Errorf("--environment-profile must be %s", PortableToolEnvironmentProfileV1)
	}
	parsedContractEnvironment, err := parseContractEnvironmentV1(*contractEnvironment, *environmentProfile)
	if err != nil {
		return sandboxExecPlanV1{}, fmt.Errorf("parse --environment-entry: %w", err)
	}
	if *recordExitStatus && (installRules || *environmentProfile != PortableToolEnvironmentProfileV1) {
		return sandboxExecPlanV1{}, fmt.Errorf("--record-exit-status requires restricted-exec with environment profile %s", PortableToolEnvironmentProfileV1)
	}
	if *sessionNetworkPrefixes != "" && (!path.IsAbs(*sessionNetworkPrefixes) || path.Clean(*sessionNetworkPrefixes) != *sessionNetworkPrefixes) {
		return sandboxExecPlanV1{}, fmt.Errorf("--session-network-prefixes must be an absolute clean path")
	}
	if (*sessionNetworkPrefixes == "") != (*sessionNetworkPeer == "") {
		return sandboxExecPlanV1{}, fmt.Errorf("--session-network-prefixes and --session-network-peer must be specified together")
	}
	if *sessionNetworkPeer != "" && *sessionNetworkPeer != "controller" && *sessionNetworkPeer != "workload" {
		return sandboxExecPlanV1{}, fmt.Errorf("--session-network-peer must be controller or workload")
	}
	parsedInbound, err := parseDecimalListV1(*inbound, 1, 65535)
	if err != nil {
		return sandboxExecPlanV1{}, fmt.Errorf("parse --inbound-tcp: %w", err)
	}
	allowPublic, err := parseNetworkAccessV1("--public", *public, installRules)
	if err != nil {
		return sandboxExecPlanV1{}, err
	}
	allowLocal, err := parseNetworkAccessV1("--local", *local, installRules)
	if err != nil {
		return sandboxExecPlanV1{}, err
	}
	allowAmbiguous, err := parseAmbiguousNetworkAccessV1(*ambiguous, allowPublic, allowLocal, installRules)
	if err != nil {
		return sandboxExecPlanV1{}, err
	}
	ports := make([]uint16, len(parsedInbound))
	for index, value := range parsedInbound {
		ports[index] = uint16(value)
	}
	return sandboxExecPlanV1{
		UID: parsedUID, GID: parsedGID, Groups: parsedGroups, EnvironmentProfile: *environmentProfile,
		ContractEnvironment: parsedContractEnvironment, RecordExitStatus: *recordExitStatus,
		AllowPublic: allowPublic, AllowLocal: allowLocal, AllowAmbiguous: allowAmbiguous,
		InboundTCP: ports, SessionNetworkPrefixes: *sessionNetworkPrefixes, SessionNetworkPeer: *sessionNetworkPeer, InstallRules: installRules,
		Argv: append([]string(nil), argv...),
	}, nil
}

const PortableToolEnvironmentProfileV1 = "portable-tool-v1"

type EnvironmentVariableV1 struct {
	Name  string
	Value string
}

// PortableToolEnvironmentV1 returns a fresh copy of the fixed environment
// installed by restricted-exec for portable-tool validation probes.
func PortableToolEnvironmentV1() []EnvironmentVariableV1 {
	return []EnvironmentVariableV1{
		{Name: "HOME", Value: "/tmp"},
		{Name: "LANG", Value: "C"},
		{Name: "LC_ALL", Value: "C"},
		{Name: "PATH", Value: "/usr/bin:/bin"},
		{Name: "TMPDIR", Value: "/tmp"},
	}
}

// contractEnvironmentFlagV1 collects repeated --environment-entry values in
// the exact order they were supplied. Ordering and uniqueness are enforced
// during parsing so a malformed invocation fails before any application runs.
type contractEnvironmentFlagV1 []string

func (values *contractEnvironmentFlagV1) String() string { return strings.Join(*values, ",") }

func (values *contractEnvironmentFlagV1) Set(value string) error {
	*values = append(*values, value)
	return nil
}

// parseContractEnvironmentV1 validates the selected closure's contract runtime
// environment entries. The entries are additional to the fixed profile and can
// never replace it: a name owned by the fixed profile is rejected rather than
// applied, so a definition cannot weaken executor-owned policy such as PATH or
// TMPDIR. Entries must be uniquely named and sorted so the resulting
// environment is one canonical, reproducible sequence.
func parseContractEnvironmentV1(values []string, profile string) ([]EnvironmentVariableV1, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if profile != PortableToolEnvironmentProfileV1 {
		return nil, fmt.Errorf("requires environment profile %s", PortableToolEnvironmentProfileV1)
	}
	fixed := make(map[string]struct{}, len(PortableToolEnvironmentV1()))
	for _, variable := range PortableToolEnvironmentV1() {
		fixed[variable.Name] = struct{}{}
	}
	result := make([]EnvironmentVariableV1, 0, len(values))
	for index, value := range values {
		name, entryValue, found := strings.Cut(value, "=")
		if !found {
			return nil, fmt.Errorf("%q must use NAME=VALUE", value)
		}
		if !validContractEnvironmentNameV1(name) {
			return nil, fmt.Errorf("name %q must match [A-Z][A-Z0-9_]*", name)
		}
		if containsControlCharacterV1(entryValue) {
			return nil, fmt.Errorf("value for %q contains a control character", name)
		}
		if _, reserved := fixed[name]; reserved {
			return nil, fmt.Errorf("name %q is owned by environment profile %s", name, PortableToolEnvironmentProfileV1)
		}
		if index > 0 && result[index-1].Name >= name {
			return nil, fmt.Errorf("names must be unique and sorted, but %q follows %q", name, result[index-1].Name)
		}
		result = append(result, EnvironmentVariableV1{Name: name, Value: entryValue})
	}
	return result, nil
}

func validContractEnvironmentNameV1(value string) bool {
	if value == "" || value[0] < 'A' || value[0] > 'Z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		char := value[index]
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func containsControlCharacterV1(value string) bool {
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return true
		}
	}
	return false
}

func sandboxExecEnvironmentV1(profile string, contract []EnvironmentVariableV1, inherited []string) ([]string, error) {
	if profile == "" {
		if len(contract) != 0 {
			return nil, fmt.Errorf("contract environment entries require an application environment profile")
		}
		return append([]string(nil), inherited...), nil
	}
	if profile != PortableToolEnvironmentProfileV1 {
		return nil, fmt.Errorf("unsupported application environment profile %q", profile)
	}
	variables := PortableToolEnvironmentV1()
	environment := make([]string, 0, len(variables)+len(contract))
	for _, variable := range variables {
		environment = append(environment, variable.Name+"="+variable.Value)
	}
	// Contract entries follow the fixed profile and are already proven not to
	// collide with it, so no fixed value can be shadowed by a later entry.
	for _, variable := range contract {
		environment = append(environment, variable.Name+"="+variable.Value)
	}
	return environment, nil
}

func parseCredentialV1(value string) (uint32, error) {
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil || value == "" || len(value) > 1 && value[0] == '0' || parsed == math.MaxUint32 || strconv.FormatUint(parsed, 10) != value {
		return 0, fmt.Errorf("%q must be a canonical unsigned 32-bit decimal value below %d", value, uint64(math.MaxUint32))
	}
	return uint32(parsed), nil
}

func parseCredentialListV1(value string) ([]uint32, error) {
	if value == "" {
		return []uint32{}, nil
	}
	result := []uint32{}
	var previous uint32
	for index, item := range strings.Split(value, ",") {
		parsed, err := parseCredentialV1(item)
		if err != nil {
			return nil, err
		}
		if index > 0 && parsed <= previous {
			return nil, fmt.Errorf("values must be unique and sorted")
		}
		result = append(result, parsed)
		previous = parsed
	}
	return result, nil
}

func parseAmbiguousNetworkAccessV1(value string, allowPublic bool, allowLocal bool, required bool) (bool, error) {
	if !required {
		if value != "" {
			return false, fmt.Errorf("--ambiguous is not accepted by restricted-exec")
		}
		return false, nil
	}
	switch value {
	case "require-both":
		return allowPublic && allowLocal, nil
	case "allow":
		return true, nil
	default:
		return false, fmt.Errorf("--ambiguous must be require-both or allow")
	}
}

func parseNetworkAccessV1(name string, value string, required bool) (bool, error) {
	if !required {
		if value != "" {
			return false, fmt.Errorf("%s is not accepted by restricted-exec", name)
		}
		return false, nil
	}
	switch value {
	case "allow":
		return true, nil
	case "deny":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be allow or deny", name)
	}
}

func parseDecimalListV1(value string, minimum int, maximum int) ([]int, error) {
	if value == "" {
		return []int{}, nil
	}
	result := []int{}
	previous := -1
	for _, item := range strings.Split(value, ",") {
		parsed, err := strconv.Atoi(item)
		if err != nil || parsed < minimum || parsed > maximum {
			return nil, fmt.Errorf("%q is outside %d..%d", item, minimum, maximum)
		}
		if parsed <= previous {
			return nil, fmt.Errorf("values must be unique and sorted")
		}
		result = append(result, parsed)
		previous = parsed
	}
	return result, nil
}
