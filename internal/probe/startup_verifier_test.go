package probe

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const validApplicationKernelStatus = `Name: reploy-probe
CapPrm: 0000000000000000
CapEff: 0000000000000000
CapBnd: 0000000000000000
NoNewPrivs: 1
Seccomp: 2
`

func TestVerifyApplicationKernelStatusAcceptsRequiredSandbox(t *testing.T) {
	if err := verifyApplicationKernelStatus([]byte(validApplicationKernelStatus)); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyApplicationKernelStatusFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "missing", content: strings.ReplaceAll(validApplicationKernelStatus, "Seccomp: 2\n", ""), want: "Seccomp is missing"},
		{name: "duplicate", content: validApplicationKernelStatus + "CapEff: 0\n", want: "CapEff appears more than once"},
		{name: "malformed", content: strings.ReplaceAll(validApplicationKernelStatus, "NoNewPrivs: 1", "NoNewPrivs: 1 2"), want: "NoNewPrivs is malformed"},
		{name: "seccomp", content: strings.ReplaceAll(validApplicationKernelStatus, "Seccomp: 2", "Seccomp: 0"), want: "Seccomp is 0, want 2"},
		{name: "no new privileges", content: strings.ReplaceAll(validApplicationKernelStatus, "NoNewPrivs: 1", "NoNewPrivs: 0"), want: "NoNewPrivs is 0, want 1"},
		{name: "effective capabilities", content: strings.ReplaceAll(validApplicationKernelStatus, "CapEff: 0000000000000000", "CapEff: 0000000000000001"), want: "CapEff is 0000000000000001"},
		{name: "permitted capabilities", content: strings.ReplaceAll(validApplicationKernelStatus, "CapPrm: 0000000000000000", "CapPrm: 0000000000000400"), want: "CapPrm is 0000000000000400"},
		{name: "bounding capabilities", content: strings.ReplaceAll(validApplicationKernelStatus, "CapBnd: 0000000000000000", "CapBnd: 000001ffffffffff"), want: "CapBnd is 000001ffffffffff"},
		{name: "invalid capability", content: strings.ReplaceAll(validApplicationKernelStatus, "CapBnd: 0000000000000000", "CapBnd: not-hex"), want: "not hexadecimal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := verifyApplicationKernelStatus([]byte(test.content))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestMainVerifyExecPreservesExactArgv(t *testing.T) {
	var got []string
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := mainWithActions(
		[]string{"verify-exec", "--", "/opt/app", "", "$(not-shell)"},
		strings.NewReader("ignored"), &stdout, &stderr,
		func() error { t.Fatal("verify mode reached hold"); return nil },
		func() error { t.Fatal("verify mode reached copy"); return nil },
		func() ([]byte, error) { return []byte(validApplicationKernelStatus), nil },
		func(argv []string) error { got = append([]string(nil), argv...); return nil },
	)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 || !reflect.DeepEqual(got, []string{"/opt/app", "", "$(not-shell)"}) {
		t.Fatalf("code=%d stdout=%q stderr=%q argv=%#v", code, stdout.String(), stderr.String(), got)
	}
}

func TestMainVerifyExecNeverExecutesAfterVerificationFailure(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := mainWithActions(
		[]string{"verify-exec", "--", "/opt/app"}, strings.NewReader(""), &stdout, &stderr,
		func() error { return nil }, func() error { return nil },
		func() ([]byte, error) {
			return []byte(strings.ReplaceAll(validApplicationKernelStatus, "Seccomp: 2", "Seccomp: 0")), nil
		},
		func([]string) error { t.Fatal("application executed after failed verification"); return nil },
	)
	if code != 1 || !strings.Contains(stderr.String(), "Seccomp is 0, want 2") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestMainVerifyExecRejectsMalformedContractAndExecutionFailure(t *testing.T) {
	for _, args := range [][]string{{"verify-exec"}, {"verify-exec", "/opt/app"}, {"verify-exec", "--", "relative"}} {
		var stderr bytes.Buffer
		code := mainWithActions(args, strings.NewReader(""), &bytes.Buffer{}, &stderr,
			func() error { return nil }, func() error { return nil },
			func() ([]byte, error) { return []byte(validApplicationKernelStatus), nil },
			func([]string) error { t.Fatal("invalid verify request executed"); return nil })
		if code == 0 {
			t.Fatalf("args %#v succeeded", args)
		}
	}

	var stderr bytes.Buffer
	code := mainWithActions(
		[]string{"verify-exec", "--", "/missing"}, strings.NewReader(""), &bytes.Buffer{}, &stderr,
		func() error { return nil }, func() error { return nil },
		func() ([]byte, error) { return []byte(validApplicationKernelStatus), nil },
		func([]string) error { return errors.New("no such file") },
	)
	if code != 1 || !strings.Contains(stderr.String(), `execute application "/missing": no such file`) {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
