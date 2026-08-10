package dockerdeploy

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/controlledsession"
)

// PrepareControlledSessionChannelV1 realizes only the private controller
// channel from a complete immutable execution plan. It does not create or
// start either container.
func PrepareControlledSessionChannelV1(plan ControlledSessionExecutionPlanV1) (*controlledsession.PrivateChannelV1, error) {
	if plan.Controller.SessionNetwork.Enabled {
		return nil, fmt.Errorf("prepare controlled-session channel: realized session-network prefixes are required")
	}
	return prepareControlledSessionChannelWithNetworkV1(plan, nil)
}

func prepareControlledSessionChannelWithNetworkV1(plan ControlledSessionExecutionPlanV1, sessionCIDRs []string) (*controlledsession.PrivateChannelV1, error) {
	config, err := controlledSessionPrivateChannelConfigV1(plan)
	if err != nil {
		return nil, err
	}
	channel, err := controlledsession.PreparePrivateChannelV1(config)
	if err != nil {
		return nil, fmt.Errorf("prepare planned controlled-session channel: %w", err)
	}
	if err := writeControlledSessionNetworkPolicyV1(plan, sessionCIDRs); err != nil {
		return nil, errors.Join(err, channel.Close())
	}
	return channel, nil
}

func writeControlledSessionNetworkPolicyV1(plan ControlledSessionExecutionPlanV1, sessionCIDRs []string) error {
	if !plan.Controller.SessionNetwork.Enabled {
		if len(sessionCIDRs) != 0 {
			return fmt.Errorf("prepare controlled-session channel: disabled session network received realized prefixes")
		}
		return nil
	}
	prefixes := append([]string(nil), sessionCIDRs...)
	sort.Strings(prefixes)
	for index, prefix := range prefixes {
		_, network, err := net.ParseCIDR(prefix)
		if err != nil || network.String() != prefix {
			return fmt.Errorf("prepare controlled-session channel: network prefix %q is not canonical CIDR", prefix)
		}
		if index > 0 && prefixes[index-1] == prefix {
			return fmt.Errorf("prepare controlled-session channel: duplicate network prefix %q", prefix)
		}
	}
	if len(prefixes) == 0 {
		return fmt.Errorf("prepare controlled-session channel: enabled session network has no realized prefixes")
	}
	policyPath := filepath.Join(plan.Channel.HostDirectory, controlledSessionNetworkPolicyFileNameV1)
	file, err := os.OpenFile(policyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return fmt.Errorf("create controlled-session network policy input: %w", err)
	}
	_, writeErr := file.WriteString(strings.Join(prefixes, "\n") + "\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("write controlled-session network policy input: %w", errors.Join(writeErr, closeErr)), os.Remove(policyPath))
	}
	return nil
}

func controlledSessionPrivateChannelConfigV1(plan ControlledSessionExecutionPlanV1) (controlledsession.PrivateChannelConfigV1, error) {
	if err := ValidateControlledSessionExecutionPlanV1(plan); err != nil {
		return controlledsession.PrivateChannelConfigV1{}, fmt.Errorf("prepare controlled-session channel plan: %w", err)
	}
	columns, err := strconv.ParseUint(plan.Workload.InitialColumns, 10, 32)
	if err != nil {
		return controlledsession.PrivateChannelConfigV1{}, fmt.Errorf("prepare controlled-session channel initial columns: %w", err)
	}
	rows, err := strconv.ParseUint(plan.Workload.InitialRows, 10, 32)
	if err != nil {
		return controlledsession.PrivateChannelConfigV1{}, fmt.Errorf("prepare controlled-session channel initial rows: %w", err)
	}
	return controlledsession.PrivateChannelConfigV1{
		HostDirectory: plan.Channel.HostDirectory,
		Opened: controlledsession.OpenedV2{
			Authorization:                         plan.Authorization,
			Endpoints:                             controlledSessionOpenedEndpointsV1(plan.Controller.SessionNetwork.Endpoints),
			Columns:                               uint32(columns),
			Rows:                                  uint32(rows),
			OutputFinalizationTimeoutMilliseconds: controlledsession.DefaultOutputFinalizationTimeoutMillisecondsV1,
		},
	}, nil
}
