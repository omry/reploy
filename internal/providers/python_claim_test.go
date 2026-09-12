package providers

import (
	"reflect"
	"testing"
)

func TestSupportedPythonClaimsNormalizationLawsV1(t *testing.T) {
	inputs := [][]string{
		{"3.10.2", "3.9", "3.10", "3.10.1", "3.9", "4.0.1"},
		{"4.0.1", "3.9", "3.10.1", "3.10", "3.9", "3.10.2"},
	}
	want := []string{"3.9", "3.10", "4.0.1"}
	for _, input := range inputs {
		normalized, err := NormalizeSupportedPythonClaimsV1(input)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(normalized, want) {
			t.Fatalf("normalized = %#v, want %#v", normalized, want)
		}
		normalizedAgain, err := NormalizeSupportedPythonClaimsV1(normalized)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(normalizedAgain, normalized) {
			t.Fatalf("normalization is not idempotent: %#v then %#v", normalized, normalizedAgain)
		}
		if len(normalized) > len(input) {
			t.Fatalf("normalization expanded %d claims to %d", len(input), len(normalized))
		}
	}
}

func TestSupportedPythonClaimsIntersectionSemanticsV1(t *testing.T) {
	tests := []struct {
		name        string
		left, right []string
		want        []string
	}{
		{name: "matching series", left: []string{"3.12"}, right: []string{"3.12"}, want: []string{"3.12"}},
		{name: "series and patches", left: []string{"3.12"}, right: []string{"3.12.1", "3.12.7", "3.13"}, want: []string{"3.12.1", "3.12.7"}},
		{name: "matching exact patch", left: []string{"3.12.7"}, right: []string{"3.12.7"}, want: []string{"3.12.7"}},
		{name: "unequal series", left: []string{"3.12"}, right: []string{"3.13"}, want: []string{}},
		{name: "unequal exact patches", left: []string{"3.12.6"}, right: []string{"3.12.7"}, want: []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := IntersectSupportedPythonClaimsV1(test.left, test.right)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("intersection = %#v, want %#v", got, test.want)
			}
			reversed, err := IntersectSupportedPythonClaimsV1(test.right, test.left)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(reversed, test.want) {
				t.Fatalf("reversed intersection = %#v, want %#v", reversed, test.want)
			}
			if len(got) > len(test.left)+len(test.right) {
				t.Fatalf("intersection expanded %d inputs to %d results", len(test.left)+len(test.right), len(got))
			}
		})
	}
}
