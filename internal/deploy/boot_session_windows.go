//go:build windows

package deploy

import (
	"fmt"
	"time"

	"github.com/yusufpapurcu/wmi"
	"golang.org/x/sys/windows"
)

type windowsOperatingSystemV1 struct {
	LastBootUpTime time.Time
}

func currentBootSessionIDV1() (string, error) {
	var operatingSystems []windowsOperatingSystemV1
	const query = "SELECT LastBootUpTime FROM Win32_OperatingSystem"
	if err := wmi.Query(query, &operatingSystems); err != nil {
		return "", fmt.Errorf("query Windows last boot time through WMI: %w", err)
	}
	if len(operatingSystems) != 1 {
		return "", fmt.Errorf(
			"query Windows last boot time through WMI returned %d operating systems, want 1",
			len(operatingSystems),
		)
	}
	bootTime := operatingSystems[0].LastBootUpTime
	if bootTime.IsZero() {
		return "", fmt.Errorf("query Windows last boot time through WMI returned an empty timestamp")
	}
	return windowsBootSessionIDV1(bootTime), nil
}

func windowsBootSessionIDV1(bootTime time.Time) string {
	filetime := windows.NsecToFiletime(bootTime.UTC().UnixNano())
	value := uint64(filetime.HighDateTime)<<32 | uint64(filetime.LowDateTime)
	return fmt.Sprintf("windows-boot-%016x", value)
}
