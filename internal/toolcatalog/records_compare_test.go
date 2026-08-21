package toolcatalog

import "testing"

func TestRecordStringSliceSubsetV1(t *testing.T) {
	tests := []struct {
		name     string
		subset   []string
		superset []string
		want     bool
	}{
		{name: "empty", want: true},
		{name: "equal", subset: []string{"a", "b"}, superset: []string{"a", "b"}, want: true},
		{name: "proper subset", subset: []string{"a", "c"}, superset: []string{"a", "b", "c"}, want: true},
		{name: "missing", subset: []string{"a", "d"}, superset: []string{"a", "b", "c"}},
		{name: "canonical order required", subset: []string{"b", "a"}, superset: []string{"a", "b"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := recordStringSliceSubsetV1(test.subset, test.superset); got != test.want {
				t.Fatalf("recordStringSliceSubsetV1(%v, %v) = %v, want %v", test.subset, test.superset, got, test.want)
			}
		})
	}
}

func TestCompareRecordStringSlicesV1(t *testing.T) {
	tests := []struct {
		name        string
		left, right []string
		want        int
	}{
		{name: "equal", left: []string{"a", "b"}, right: []string{"a", "b"}},
		{name: "element less", left: []string{"a", "b"}, right: []string{"a", "c"}, want: -1},
		{name: "element greater", left: []string{"b"}, right: []string{"a"}, want: 1},
		{name: "prefix less", left: []string{"a"}, right: []string{"a", "b"}, want: -1},
		{name: "prefix greater", left: []string{"a", "b"}, right: []string{"a"}, want: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := compareRecordStringSlicesV1(test.left, test.right); got != test.want {
				t.Fatalf("compareRecordStringSlicesV1(%v, %v) = %d, want %d", test.left, test.right, got, test.want)
			}
		})
	}
}

func TestContainsRecordValueV1(t *testing.T) {
	if !containsRecordValueV1([]string{"a", "b"}, "b") {
		t.Fatal("containsRecordValueV1 did not find present value")
	}
	if containsRecordValueV1([]string{"a", "b"}, "c") {
		t.Fatal("containsRecordValueV1 found absent value")
	}
}
