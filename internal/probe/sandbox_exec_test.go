package probe

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseSandboxExecPlanV1PreservesPolicyIdentityAndArgv(t *testing.T) {
	plan, err := parseSandboxExecPlanV1([]string{
		"--uid", "501", "--gid", "20", "--groups", "33,44",
		"--public", "allow", "--local", "deny", "--ambiguous", "allow", "--inbound-tcp", "8080,8443",
		"--", "/opt/app", "", "$(not-shell)",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.UID != 501 || plan.GID != 20 || !reflect.DeepEqual(plan.Groups, []int{33, 44}) ||
		!plan.AllowPublic || plan.AllowLocal || !plan.AllowAmbiguous || !reflect.DeepEqual(plan.InboundTCP, []uint16{8080, 8443}) ||
		!reflect.DeepEqual(plan.Argv, []string{"/opt/app", "", "$(not-shell)"}) {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestParseSandboxExecPlanV1RequireBothAllowsAmbiguousOnlyWithBothGrants(t *testing.T) {
	for _, test := range []struct {
		name      string
		public    string
		local     string
		wantAllow bool
	}{
		{name: "neither", public: "deny", local: "deny"},
		{name: "public only", public: "allow", local: "deny"},
		{name: "local only", public: "deny", local: "allow"},
		{name: "both", public: "allow", local: "allow", wantAllow: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := parseSandboxExecPlanV1([]string{
				"--uid", "501", "--gid", "20",
				"--public", test.public, "--local", test.local, "--ambiguous", "require-both",
				"--", "/opt/app",
			}, true)
			if err != nil {
				t.Fatal(err)
			}
			if plan.AllowAmbiguous != test.wantAllow {
				t.Fatalf("AllowAmbiguous = %t, want %t", plan.AllowAmbiguous, test.wantAllow)
			}
		})
	}
}

func TestParseSandboxExecPlanV1FailsClosed(t *testing.T) {
	base := []string{"--uid", "501", "--gid", "20", "--public", "deny", "--local", "deny", "--ambiguous", "require-both", "--", "/opt/app"}
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "separator", args: base[:len(base)-2], want: "requires --"},
		{name: "identity", args: []string{"--uid", "-1", "--gid", "20", "--public", "deny", "--local", "deny", "--ambiguous", "require-both", "--", "/opt/app"}, want: "non-negative"},
		{name: "policy", args: []string{"--uid", "1", "--gid", "2", "--public", "yes", "--local", "deny", "--ambiguous", "require-both", "--", "/opt/app"}, want: "--public"},
		{name: "ambiguous policy", args: []string{"--uid", "1", "--gid", "2", "--public", "deny", "--local", "deny", "--ambiguous", "yes", "--", "/opt/app"}, want: "--ambiguous"},
		{name: "groups", args: []string{"--uid", "1", "--gid", "2", "--groups", "44,33", "--public", "deny", "--local", "deny", "--ambiguous", "require-both", "--", "/opt/app"}, want: "unique and sorted"},
		{name: "port", args: []string{"--uid", "1", "--gid", "2", "--public", "deny", "--local", "deny", "--ambiguous", "require-both", "--inbound-tcp", "0", "--", "/opt/app"}, want: "outside 1..65535"},
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
	for _, argument := range []string{"--public", "--local", "--ambiguous", "--inbound-tcp"} {
		t.Run(argument, func(t *testing.T) {
			value := "allow"
			if argument == "--inbound-tcp" {
				value = "8080"
			}
			_, err := parseSandboxExecPlanV1([]string{
				"--uid", "501", "--gid", "20", argument, value, "--", "/bin/true",
			}, false)
			if err == nil || !strings.Contains(err.Error(), argument) {
				t.Fatalf("restricted exec network argument error = %v", err)
			}
		})
	}
}
