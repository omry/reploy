package python

import "testing"

func TestWheelRequirementDistributionName(t *testing.T) {
	tests := []struct {
		requirement, want string
		valid             bool
	}{
		{"Requests[security,tests]>=2.0,<3; python_version >= '3.8' and (os_name == 'posix' or sys_platform == 'win32')", "requests", true},
		{"demo @ https://example.com/demo-1.0.whl#sha256=abc ; python_version >= '3.8'", "demo", true},
		{"demo (>=1.2)", "demo", true},
		{"demo>=1.2;python_version>='3.8'", "demo", true},
		{"demo; 'posix' == os_name", "demo", true},
		{"demo; python_version=='3'and'posix'==os_name", "demo", true},
		{"demo[ ]", "demo", true},
		{"demo>=1,", "demo", true},
		{"demo===legacy_build", "demo", true},
		{"demo===foo/bar", "demo", true},
		{"demo===foo:bar@host#?{}[]\\", "demo", true},
		{"demo===foo=bar; os_name == 'posix'", "demo", true},
		{"demo(===foo/bar)", "demo", true},
		{"demo===", "demo", true},
		{"demo===,>=1", "demo", true},
		{"demo; python_implementation == 'CPython'", "demo", true},
		{"demo; 'posix' == os.name", "demo", true},
		{"demo; sys.platform == 'win32'", "demo", true},
		{"demo; platform.version == '1'", "demo", true},
		{"demo; platform.machine == 'arm64'", "demo", true},
		{"demo; platform.python_implementation == 'CPython'", "demo", true},
		{"demo; platform_release == 'café'", "demo", true},
		{"demo; \"東京\" == platform_release", "demo", true},
		{"demo; platform_release == '☃; (and)'", "demo", true},
		{"demo; platform_release == 'cafe\u0301'", "demo", true},
		{"demo>=2.0RC1", "demo", true},
		{"demo!=2.0PRE1,>=v01.0POST", "demo", true},
		{"demo==02.00.*", "demo", true},
		{"demo; python_version==implementation_version", "demo", true},
		{"demo[]", "demo", true},
		{"demo @ ./local/pkg.whl", "demo", true},
		{"demo @ https://example.com/a;param", "demo", true},
		{"demo @ https://example.com/a;param ; python_version >= '3.8'", "demo", true},
		{"demo @ ./a%20b.whl", "demo", true},
		{"requests !!!", "", false}, {"demo[bad!extra]", "", false},
		{"demo-", "", false}, {"demo.", "", false}, {"demo()", "", false},
		{"demo @ https://example.com/a%2", "", false}, {"demo @ https://example.com/a{b}", "", false},
		{"demo; unknown == 'x'", "", false}, {"demo >=", "", false},
		{"demo; extras == 'x'", "", false},
		{"demo; python_version == '3' and", "", false}, {"demo; (python_version == '3'", "", false},
		{"demo >=1 trailing", "", false},
		{"demo; os_name ==== 'posix'", "", false},
		{"demo; os_name == 'po\nsix'", "", false},
		{"demo\n", "", false},
		{"demo>=1,,<2", "", false},
		{"demo>=1<2", "", false},
		{"demo>=1||<2", "", false},
		{"demo *", "", false},
		{"demo==2.0RC1.*", "", false},
		{"demo((>=1))", "", false},
		{"demo===foo bar", "", false},
		{"demo===foo)", "", false},
		{"demo===foo; unknown == 'x'", "", false},
		{"demo==foo/bar", "", false},
		{"demo; platform.release == '1'", "", false},
		{"demo; platform.system == 'x'", "", false},
		{"demo; implementation.name == 'x'", "", false},
		{"demo; python.version == '3'", "", false},
		{"demo; platform_release == '\xff'", "", false},
		{"demo; platform_release == '\xc0\xaf'", "", false},
		{"demo; platform_release == '\x7f'", "", false},
		{"démø; platform_release == 'café'", "", false},
		{"demo[é]; platform_release == 'café'", "", false},
		{"demo===é; platform_release == 'café'", "", false},
		{"demo; platform_release == café", "", false},
		{"demo; platfórm_release == 'café'", "", false},
		{"demo; platform_release == 'café", "", false},
		{"\u00a0demo; platform_release == 'café'", "", false},
		{"demo\u00a0; platform_release == 'café'", "", false},
		{"demo;\u00a0platform_release == 'café'", "", false},
		{"demo; platform_release == 'café'\u00a0", "", false},
	}
	for _, test := range tests {
		got, err := wheelRequirementDistributionName(test.requirement)
		if test.valid {
			if err != nil || got != test.want {
				t.Errorf("%q = %q, %v; want %q", test.requirement, got, err, test.want)
			}
		} else if err == nil {
			t.Errorf("%q unexpectedly accepted as %q", test.requirement, got)
		}
	}
	deep := "demo; "
	for i := 0; i < 65; i++ {
		deep += "("
	}
	deep += "python_version == '3'"
	for i := 0; i < 65; i++ {
		deep += ")"
	}
	if _, err := wheelRequirementDistributionName(deep); err == nil {
		t.Error("deeply nested marker unexpectedly accepted")
	}
}
