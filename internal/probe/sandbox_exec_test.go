package probe

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseSandboxExecPlanV1PreservesPolicyIdentityAndArgv(t *testing.T) {
	plan, err := parseSandboxExecPlanV1([]string{
		"--uid", "501", "--gid", "20", "--groups", "33,44",
		"--public", "allow", "--local", "deny", "--inbound-tcp", "8080,8443",
		"--", "/opt/app", "", "$(not-shell)",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.UID != 501 || plan.GID != 20 || !reflect.DeepEqual(plan.Groups, []int{33, 44}) ||
		!plan.AllowPublic || plan.AllowLocal || !reflect.DeepEqual(plan.InboundTCP, []uint16{8080, 8443}) ||
		!reflect.DeepEqual(plan.Argv, []string{"/opt/app", "", "$(not-shell)"}) {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestParseSandboxExecPlanV1FailsClosed(t *testing.T) {
	base := []string{"--uid", "501", "--gid", "20", "--public", "deny", "--local", "deny", "--", "/opt/app"}
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "separator", args: base[:len(base)-2], want: "requires --"},
		{name: "identity", args: []string{"--uid", "-1", "--gid", "20", "--public", "deny", "--local", "deny", "--", "/opt/app"}, want: "non-negative"},
		{name: "policy", args: []string{"--uid", "1", "--gid", "2", "--public", "yes", "--local", "deny", "--", "/opt/app"}, want: "--public"},
		{name: "groups", args: []string{"--uid", "1", "--gid", "2", "--groups", "44,33", "--public", "deny", "--local", "deny", "--", "/opt/app"}, want: "unique and sorted"},
		{name: "port", args: []string{"--uid", "1", "--gid", "2", "--public", "deny", "--local", "deny", "--inbound-tcp", "0", "--", "/opt/app"}, want: "outside 1..65535"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseSandboxExecPlanV1(test.args, true)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRestrictedExecDoesNotAcceptNetworkSetupArguments(t *testing.T) {
	_, err := parseSandboxExecPlanV1([]string{
		"--uid", "501", "--gid", "20", "--public", "allow", "--", "/bin/true",
	}, false)
	if err == nil || !strings.Contains(err.Error(), "--public") {
		t.Fatalf("restricted exec network argument error = %v", err)
	}
}
