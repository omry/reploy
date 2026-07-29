//go:build windows

package deploy

import (
	"strings"
	"testing"
	"time"
)

func TestWindowsBootSessionIDV1NormalizesWMITimestampToUTC(t *testing.T) {
	philippines := time.FixedZone("UTC+8", 8*60*60)
	bootTime := time.Date(2026, time.July, 30, 4, 24, 40, 500_000_000, philippines)
	const want = "windows-boot-01dd1f9848a13f40"
	if got := windowsBootSessionIDV1(bootTime); got != want {
		t.Fatalf("Windows boot-session identity = %q, want %q", got, want)
	}
}

func TestCurrentBootSessionIDV1ReadsWMI(t *testing.T) {
	got, err := currentBootSessionIDV1()
	if err != nil {
		t.Fatalf("currentBootSessionIDV1() error: %v", err)
	}
	if !strings.HasPrefix(got, "windows-boot-") {
		t.Fatalf("currentBootSessionIDV1() = %q, want Windows boot identity", got)
	}
}
