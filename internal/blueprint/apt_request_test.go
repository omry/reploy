package blueprint

import (
	"strings"
	"testing"
)

func TestParseAPTPackageRequest(t *testing.T) {
	for _, test := range []struct {
		input   string
		name    string
		version string
	}{
		{input: "ca-certificates", name: "ca-certificates"},
		{input: "libstdc++6", name: "libstdc++6"},
		{input: "x+", name: "x+"},
		{input: "x-", name: "x-"},
		{input: "python3=3.11.2-1+deb12u1", name: "python3", version: "3.11.2-1+deb12u1"},
		{input: "python3=1:3.11.2-1+deb12u1", name: "python3", version: "1:3.11.2-1+deb12u1"},
		{input: "example=1:2:3-1", name: "example", version: "1:2:3-1"},
		{input: "example=1.0~rc1-2", name: "example", version: "1.0~rc1-2"},
	} {
		request, err := ParseAPTPackageRequest(test.input)
		if err != nil {
			t.Fatalf("ParseAPTPackageRequest(%q): %v", test.input, err)
		}
		if request.Name != test.name || request.Version != test.version || request.Exports == nil {
			t.Fatalf("ParseAPTPackageRequest(%q) = %#v", test.input, request)
		}
		canonical, err := request.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		if canonical != test.input {
			t.Fatalf("Canonical() = %q, want %q", canonical, test.input)
		}
	}
}

func TestParseAPTPackageRequestRejectsBroaderAPTGrammar(t *testing.T) {
	for _, value := range []string{
		"", "x", " curl", "curl ", "curl\n", "-curl", ".curl",
		"curl/stable", "curl:amd64", "curl>=1", "curl=", "curl==1",
		"curl=latest", "curl=1::2", "curl=:1", "curl=1.0-",
		"./curl.deb", "/tmp/curl.deb", "?name(curl)", "--option",
		string([]byte{'c', 'u', 'r', 'l', 0xff}),
	} {
		if _, err := ParseAPTPackageRequest(value); err == nil {
			t.Fatalf("ParseAPTPackageRequest(%q) succeeded", value)
		}
	}
}

func TestValidateAPTPackageRequestExports(t *testing.T) {
	request, err := ParseAPTPackageRequest("python3=3.11.2-1")
	if err != nil {
		t.Fatal(err)
	}
	request.Exports["python"] = ExecutableExport{Executable: "/usr/bin/python3"}
	if err := ValidateAPTPackageRequest(request); err != nil {
		t.Fatal(err)
	}
	for _, executable := range []string{"", "usr/bin/python3", "/usr/bin/../bin/python3", "/", `C:\python.exe`} {
		request.Exports["python"] = ExecutableExport{Executable: executable}
		if err := ValidateAPTPackageRequest(request); err == nil {
			t.Fatalf("executable %q was accepted", executable)
		}
	}
	request.Exports = map[string]ExecutableExport{"Python": {Executable: "/usr/bin/python3"}}
	if err := ValidateAPTPackageRequest(request); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("invalid export name error = %v", err)
	}
}

func TestCommandRequirementValidation(t *testing.T) {
	for _, requirement := range []CommandRequirement{
		{Command: "python"},
		{Command: "python", Version: ">=3.11,<3.12", Supplier: "base"},
		{Command: "python", Supplier: "system"},
	} {
		if err := requirement.Validate("interpreter"); err != nil {
			t.Fatalf("Validate(%#v): %v", requirement, err)
		}
	}
	for _, requirement := range []CommandRequirement{
		{},
		{Command: "Python"},
		{Command: "python", Version: " >=3.11"},
		{Command: "python", Supplier: "system.python"},
	} {
		if err := requirement.Validate("interpreter"); err == nil {
			t.Fatalf("Validate(%#v) succeeded", requirement)
		}
	}
}
