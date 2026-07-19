package apt

import (
	"reflect"
	"strings"
	"testing"
)

func aptMixedResolvePlan() ResolvePlanV1 {
	return ResolvePlanV1{Schema: ResolvePlanSchemaV1, Packages: []ResolvePlanPackageV1{
		{Name: "hello", ResolverArchitecture: "amd64", SelectedVersion: "2.10"},
		{Name: "iproute2", ResolverArchitecture: "amd64", CurrentVersion: "6.1-1", SelectedVersion: "6.1-2"},
		{Name: "libc6", ResolverArchitecture: "amd64", CurrentVersion: "2.39", SelectedVersion: "2.39"},
		{Name: "perl-modules", ResolverArchitecture: "amd64", CurrentVersion: "5.38", SelectedVersion: "5.38"},
	}}
}

func TestBasePackageStateParserVerifiesNativeAllAndUpgradePredecessor(t *testing.T) {
	plan := aptMixedResolvePlan()
	names, err := ResolveBasePackageNamesV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{"iproute2", "libc6", "perl-modules"}) {
		t.Fatalf("names = %#v", names)
	}
	parser, err := NewBasePackageStateParserV1("amd64", plan)
	if err != nil {
		t.Fatal(err)
	}
	input := "perl-modules\t5.38\tall\tinstall ok installed\n" +
		"libc6:amd64\t2.39\tamd64\tinstall ok installed\n" +
		"iproute2:amd64\t6.1-1\tamd64\tinstall ok installed\n"
	for _, chunk := range []string{input[:13], input[13:71], input[71:]} {
		_, _ = parser.Write([]byte(chunk))
	}
	tuples, err := parser.Finish()
	if err != nil {
		t.Fatal(err)
	}
	want := []PackageTuple{
		{Name: "iproute2", Version: "6.1-1", Architecture: "amd64", Status: InstalledPackageStatusV1},
		{Name: "libc6", Version: "2.39", Architecture: "amd64", Status: InstalledPackageStatusV1},
		{Name: "perl-modules", Version: "5.38", Architecture: "all", Status: InstalledPackageStatusV1},
	}
	if !reflect.DeepEqual(tuples, want) {
		t.Fatalf("tuples = %#v, want %#v", tuples, want)
	}
}

func TestBasePackageStateParserRejectsMissingExtraDuplicateAndMismatchedRows(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "missing", input: "iproute2:amd64\t6.1-1\tamd64\tinstall ok installed\nlibc6:amd64\t2.39\tamd64\tinstall ok installed\n", want: "did not report"},
		{name: "extra", input: "extra\t1\tamd64\tinstall ok installed\n", want: "unplanned package"},
		{name: "duplicate", input: "libc6:amd64\t2.39\tamd64\tinstall ok installed\nlibc6:amd64\t2.39\tamd64\tinstall ok installed\n", want: "repeated"},
		{name: "version", input: "libc6:amd64\t2.40\tamd64\tinstall ok installed\n", want: "does not match"},
		{name: "architecture", input: "libc6:amd64\t2.39\tarm64\tinstall ok installed\n", want: "unsupported architecture"},
		{name: "qualifier", input: "libc6:arm64\t2.39\tamd64\tinstall ok installed\n", want: "binary-name architecture"},
		{name: "status", input: "libc6:amd64\t2.39\tamd64\tinstall ok unpacked\n", want: "exact installed state"},
		{name: "partial", input: "libc6:amd64\t2.39\tamd64\tinstall ok installed", want: "incomplete line"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parser, err := NewBasePackageStateParserV1("amd64", aptMixedResolvePlan())
			if err != nil {
				t.Fatal(err)
			}
			_, _ = parser.Write([]byte(test.input))
			_, err = parser.Finish()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
		})
	}
}
