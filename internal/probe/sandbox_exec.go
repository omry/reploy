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
	AllowPublic            bool
	AllowLocal             bool
	AllowAmbiguous         bool
	InboundTCP             []uint16
	SessionNetworkPrefixes string
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
	public := set.String("public", "", "public network policy")
	local := set.String("local", "", "local network policy")
	ambiguous := set.String("ambiguous", "", "ambiguous destination policy")
	inbound := set.String("inbound-tcp", "", "comma-separated inbound TCP ports")
	sessionNetworkPrefixes := set.String("session-network-prefixes", "", "trusted file containing session-network CIDRs")
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
	if *sessionNetworkPrefixes != "" && (!path.IsAbs(*sessionNetworkPrefixes) || path.Clean(*sessionNetworkPrefixes) != *sessionNetworkPrefixes) {
		return sandboxExecPlanV1{}, fmt.Errorf("--session-network-prefixes must be an absolute clean path")
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
		UID: parsedUID, GID: parsedGID, Groups: parsedGroups,
		AllowPublic: allowPublic, AllowLocal: allowLocal, AllowAmbiguous: allowAmbiguous,
		InboundTCP: ports, SessionNetworkPrefixes: *sessionNetworkPrefixes, InstallRules: installRules,
		Argv: append([]string(nil), argv...),
	}, nil
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
