package providers

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/portabletool"
)

// SupportedPythonClaimV1 is the provider-owned representation of one
// supported interpreter claim. Exact is false for a complete minor series and
// true for one complete major.minor.patch release.
type SupportedPythonClaimV1 struct {
	Major int
	Minor int
	Patch int
	Exact bool
}

// ParseSupportedPythonClaimV1 validates and parses one canonical claim.
func ParseSupportedPythonClaimV1(value string) (SupportedPythonClaimV1, error) {
	if err := portabletool.ValidatePythonInterpreterVersionV1(value); err != nil {
		return SupportedPythonClaimV1{}, err
	}
	parts := strings.Split(value, ".")
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	claim := SupportedPythonClaimV1{Major: major, Minor: minor}
	if len(parts) == 3 {
		claim.Patch, _ = strconv.Atoi(parts[2])
		claim.Exact = true
	}
	return claim, nil
}

func (claim SupportedPythonClaimV1) String() string {
	if claim.Exact {
		return fmt.Sprintf("%d.%d.%d", claim.Major, claim.Minor, claim.Patch)
	}
	return fmt.Sprintf("%d.%d", claim.Major, claim.Minor)
}

func compareSupportedPythonClaimsV1(left, right SupportedPythonClaimV1) int {
	if left.Major != right.Major {
		if left.Major < right.Major {
			return -1
		}
		return 1
	}
	if left.Minor != right.Minor {
		if left.Minor < right.Minor {
			return -1
		}
		return 1
	}
	if left.Exact != right.Exact {
		if !left.Exact {
			return -1
		}
		return 1
	}
	if !left.Exact || left.Patch == right.Patch {
		return 0
	}
	if left.Patch < right.Patch {
		return -1
	}
	return 1
}

func sameSupportedPythonMinorV1(left, right SupportedPythonClaimV1) bool {
	return left.Major == right.Major && left.Minor == right.Minor
}

// NormalizeSupportedPythonClaimsV1 sorts claims numerically, removes
// duplicates, and removes exact releases subsumed by a minor-series claim.
func NormalizeSupportedPythonClaimsV1(values []string) ([]string, error) {
	claims := make([]SupportedPythonClaimV1, 0, len(values))
	for _, value := range values {
		claim, err := ParseSupportedPythonClaimV1(value)
		if err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	sort.Slice(claims, func(left, right int) bool {
		return compareSupportedPythonClaimsV1(claims[left], claims[right]) < 0
	})
	result := make([]string, 0, len(claims))
	seriesByMinor := make(map[[2]int]struct{})
	for index, claim := range claims {
		if index > 0 && claims[index-1] == claim {
			continue
		}
		minorKey := [2]int{claim.Major, claim.Minor}
		if !claim.Exact {
			seriesByMinor[minorKey] = struct{}{}
		} else if _, subsumed := seriesByMinor[minorKey]; subsumed {
			continue
		}
		result = append(result, claim.String())
	}
	return result, nil
}

// IntersectSupportedPythonClaimsV1 performs one linear intersection over
// normalized claims. A series intersects every exact patch in that series.
func IntersectSupportedPythonClaimsV1(left, right []string) ([]string, error) {
	leftNormalized, err := NormalizeSupportedPythonClaimsV1(left)
	if err != nil {
		return nil, err
	}
	rightNormalized, err := NormalizeSupportedPythonClaimsV1(right)
	if err != nil {
		return nil, err
	}
	leftClaims := make([]SupportedPythonClaimV1, len(leftNormalized))
	rightClaims := make([]SupportedPythonClaimV1, len(rightNormalized))
	for index, value := range leftNormalized {
		leftClaims[index], _ = ParseSupportedPythonClaimV1(value)
	}
	for index, value := range rightNormalized {
		rightClaims[index], _ = ParseSupportedPythonClaimV1(value)
	}
	result := []string{}
	for leftIndex, rightIndex := 0, 0; leftIndex < len(leftClaims) && rightIndex < len(rightClaims); {
		leftClaim, rightClaim := leftClaims[leftIndex], rightClaims[rightIndex]
		if !sameSupportedPythonMinorV1(leftClaim, rightClaim) {
			if compareSupportedPythonClaimsV1(leftClaim, rightClaim) < 0 {
				leftIndex++
			} else {
				rightIndex++
			}
			continue
		}
		switch {
		case !leftClaim.Exact && rightClaim.Exact:
			result = append(result, rightClaim.String())
			rightIndex++
		case leftClaim.Exact && !rightClaim.Exact:
			result = append(result, leftClaim.String())
			leftIndex++
		case !leftClaim.Exact && !rightClaim.Exact:
			result = append(result, leftClaim.String())
			leftIndex++
			rightIndex++
		case leftClaim.Patch == rightClaim.Patch:
			result = append(result, leftClaim.String())
			leftIndex++
			rightIndex++
		case leftClaim.Patch < rightClaim.Patch:
			leftIndex++
		default:
			rightIndex++
		}
	}
	return NormalizeSupportedPythonClaimsV1(result)
}
