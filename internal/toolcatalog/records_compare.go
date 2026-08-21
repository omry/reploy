package toolcatalog

// Ordering and containment helpers for the canonical string collections used
// throughout the portable tool record model.

// recordStringSliceSubsetV1 compares canonical, sorted string collections.
func recordStringSliceSubsetV1(subset []string, superset []string) bool {
	left, right := 0, 0
	for left < len(subset) && right < len(superset) {
		switch {
		case subset[left] == superset[right]:
			left++
			right++
		case subset[left] > superset[right]:
			right++
		default:
			return false
		}
	}
	return left == len(subset)
}

// compareRecordStringSlicesV1 orders canonical, sorted string collections
// lexicographically.
func compareRecordStringSlicesV1(left []string, right []string) int {
	for index := 0; index < len(left) && index < len(right); index++ {
		switch {
		case left[index] < right[index]:
			return -1
		case left[index] > right[index]:
			return 1
		}
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	default:
		return 0
	}
}

func containsRecordValueV1(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
