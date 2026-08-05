package dockerdeploy

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

const (
	windowsRuntimeIDMinimumV1 = 100_000
	windowsRuntimeIDSpanV1    = 1_900_000_000
)

func windowsSIDRuntimeIdentityV1(sid string) (int, int, error) {
	sid = strings.TrimSpace(sid)
	parts := strings.Split(strings.ToUpper(sid), "-")
	if len(parts) < 4 || parts[0] != "S" {
		return 0, 0, fmt.Errorf("Windows SID is empty or malformed")
	}
	canonical := make([]string, len(parts))
	canonical[0] = "S"
	for index, part := range parts[1:] {
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("Windows SID is empty or malformed")
		}
		canonical[index+1] = strconv.FormatUint(value, 10)
	}
	digest := sha256.Sum256([]byte("reploy-windows-runtime-identity-v1\x00" + strings.Join(canonical, "-")))
	id := windowsRuntimeIDMinimumV1 + int(binary.BigEndian.Uint64(digest[:8])%windowsRuntimeIDSpanV1)
	return id, id, nil
}
