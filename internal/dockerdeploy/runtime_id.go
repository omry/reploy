package dockerdeploy

import (
	"fmt"
	"strconv"
)

const runtimeIDUnchangedSentinelV1 = ^uint32(0)

func runtimeIDFromNativeIntV1(value int) (uint32, error) {
	return runtimeIDFromNativeWidthV1(int64(value), strconv.IntSize)
}

func runtimeIDFromNativeWidthV1(value int64, bits int) (uint32, error) {
	if bits != 32 && bits != 64 {
		return 0, fmt.Errorf("runtime ID native width must be 32 or 64 bits")
	}
	if bits == 32 && (value < -1<<31 || value > 1<<31-1) {
		return 0, fmt.Errorf("runtime ID does not fit a native 32-bit integer")
	}
	if bits == 64 && value < 0 {
		return 0, fmt.Errorf("runtime ID must not be negative")
	}
	id := uint32(value)
	if id == runtimeIDUnchangedSentinelV1 || bits == 64 && value > int64(runtimeIDUnchangedSentinelV1) {
		return 0, fmt.Errorf("runtime ID must be an unsigned 32-bit value other than %d", runtimeIDUnchangedSentinelV1)
	}
	return id, nil
}

func runtimeIDsFromNativeIntsV1(values []int) ([]uint32, error) {
	result := make([]uint32, len(values))
	for index, value := range values {
		id, err := runtimeIDFromNativeIntV1(value)
		if err != nil {
			return nil, fmt.Errorf("runtime ID at index %d: %w", index, err)
		}
		result[index] = id
	}
	return result, nil
}

func runtimeIDStringV1(value uint32) string {
	return strconv.FormatUint(uint64(value), 10)
}
