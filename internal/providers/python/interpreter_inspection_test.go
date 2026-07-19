package python

import (
	"reflect"
	"strings"
	"testing"
)

func TestInterpreterInspectionArgvIsFixedAndAbsolute(t *testing.T) {
	argv, err := InterpreterInspectionArgv("/usr/bin/python3")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/usr/bin/python3", "-I", "-S", "-c",
		`import sys; print(".".join(map(str, sys.version_info[:3])))`,
	}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("inspection argv = %#v, want %#v", argv, want)
	}
	for _, executable := range []string{"", "python3", "/usr/bin/../bin/python3", `/usr/bin\python3`} {
		if _, err := InterpreterInspectionArgv(executable); err == nil {
			t.Fatalf("inspection accepted executable %q", executable)
		}
	}
}

func TestParseInterpreterInspectionOutput(t *testing.T) {
	for _, output := range []string{"3.13.2\n", "3.13.2"} {
		version, err := ParseInterpreterInspectionOutput([]byte(output))
		if err != nil {
			t.Fatal(err)
		}
		if version != "3.13.2" {
			t.Fatalf("version = %q", version)
		}
	}
	for _, output := range []string{
		"", "3.13", "3.13.2.1", "3.013.2", "3.13.x", " 3.13.2\n", "3.13.2\nwarning\n",
	} {
		t.Run(strings.ReplaceAll(output, "\n", "_"), func(t *testing.T) {
			if _, err := ParseInterpreterInspectionOutput([]byte(output)); err == nil {
				t.Fatalf("inspection accepted output %q", output)
			}
		})
	}
}
