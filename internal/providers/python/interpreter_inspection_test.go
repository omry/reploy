package python

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestInterpreterInspectionArgvIsFixedAndAbsolute(t *testing.T) {
	tested := []string{"cp313-cp313-manylinux2014_x86_64", "py3-none-any"}
	argv, err := InterpreterInspectionArgv("/usr/bin/python3", tested, "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/python3", "-I", "-c", interpreterInspectionProgramV2, `["cp313-cp313-manylinux2014_x86_64","py3-none-any"]`, "x86_64"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("inspection argv = %#v, want %#v", argv, want)
	}
	for _, executable := range []string{"", "python3", "/usr/bin/../bin/python3", `/usr/bin\python3`} {
		if _, err := InterpreterInspectionArgv(executable, tested, "x86_64"); err == nil {
			t.Fatalf("inspection accepted executable %q", executable)
		}
	}
	for _, invalid := range []struct {
		tags         []string
		architecture string
	}{
		{nil, "x86_64"},
		{[]string{"py3-none-any", "py3-none-any"}, "x86_64"},
		{[]string{"py3-none-any"}, "amd64"},
	} {
		if _, err := InterpreterInspectionArgv("/usr/bin/python3", invalid.tags, invalid.architecture); err == nil {
			t.Fatalf("inspection accepted tags %#v and architecture %q", invalid.tags, invalid.architecture)
		}
	}
	if !strings.Contains(argv[3], "sys.argv[1]") || strings.Contains(argv[3], tested[0]) {
		t.Fatalf("tested tags were interpolated into fixed source: %#v", argv)
	}
}

func TestParseInterpreterInspectionOutput(t *testing.T) {
	tested := []string{"py3-none-any"}
	valid := `{"version":"3.13.2","implementation":"cpython","abi":"cp313","libc":"glibc","libc_major":"2","libc_minor":"35","tested_tags":["py3-none-any"],"compatible_tags":["py3-none-any"]}`
	for _, output := range []string{valid + "\n", valid} {
		facts, err := ParseInterpreterInspectionOutput([]byte(output), tested)
		if err != nil {
			t.Fatal(err)
		}
		if facts.Version != "3.13.2" || !reflect.DeepEqual(facts.TestedTags, tested) || !reflect.DeepEqual(facts.CompatibleTags, tested) {
			t.Fatalf("facts = %#v", facts)
		}
	}
	for _, output := range []string{
		"", "3.13.2", "[]", valid + " {}",
		`{"version":"3.13.2","implementation":"cpython","abi":"cp313","libc":"glibc","libc_major":"2","libc_minor":"35","tested_tags":null,"compatible_tags":[]}`,
		`{"version":"3.13.2","implementation":"cpython","abi":"cp313","libc":"glibc","libc_major":"2","libc_minor":"35","tested_tags":["py3-none-any"],"compatible_tags":[],"extra":true}`,
	} {
		t.Run(strings.ReplaceAll(output, "\n", "_"), func(t *testing.T) {
			if _, err := ParseInterpreterInspectionOutput([]byte(output), tested); err == nil {
				t.Fatalf("inspection accepted output %q", output)
			}
		})
	}
}

func TestParseInterpreterInspectionOutputRejectsIncompleteOrDishonestTagEvidence(t *testing.T) {
	tested := []string{"cp313-cp313-manylinux2014_x86_64", "py3-none-any"}
	for _, output := range []string{
		`{"version":"3.13.2","implementation":"cpython","abi":"cp313","libc":"glibc","libc_major":"2","libc_minor":"35","tested_tags":["py3-none-any"],"compatible_tags":["py3-none-any"]}`,
		`{"version":"3.13.2","implementation":"cpython","abi":"cp313","libc":"glibc","libc_major":"2","libc_minor":"35","tested_tags":["cp313-cp313-manylinux2014_x86_64","py3-none-any"],"compatible_tags":["outside-none-any"]}`,
		`{"version":"3.13.2","implementation":"cpython","abi":"cp313","libc":"glibc","libc_major":"2","libc_minor":"35","tested_tags":["cp313-cp313-manylinux2014_x86_64","py3-none-any"],"compatible_tags":["py3-none-any","py3-none-any"]}`,
		`{"version":"3.13.2","version":"3.13.3","implementation":"cpython","abi":"cp313","libc":"glibc","libc_major":"2","libc_minor":"35","tested_tags":["cp313-cp313-manylinux2014_x86_64","py3-none-any"],"compatible_tags":[]}`,
	} {
		if _, err := ParseInterpreterInspectionOutput([]byte(output), tested); err == nil {
			t.Errorf("inspection accepted invalid evidence %s", output)
		}
	}
}

func TestParseInterpreterInspectionOutputRejectsMalformedScalarFacts(t *testing.T) {
	valid := `{"version":"3.13.2","implementation":"cpython","abi":"cp313","libc":"glibc","libc_major":"2","libc_minor":"35","tested_tags":[],"compatible_tags":[]}`
	for _, test := range []struct {
		name         string
		oldValue     string
		invalidValue string
	}{
		{name: "incomplete version", oldValue: `"version":"3.13.2"`, invalidValue: `"version":"3.13"`},
		{name: "implementation", oldValue: `"implementation":"cpython"`, invalidValue: `"implementation":"CPython"`},
		{name: "ABI", oldValue: `"abi":"cp313"`, invalidValue: `"abi":"cp313-d"`},
		{name: "libc", oldValue: `"libc":"glibc"`, invalidValue: `"libc":"glibc!"`},
		{name: "leading-zero major", oldValue: `"libc_major":"2"`, invalidValue: `"libc_major":"02"`},
		{name: "negative minor", oldValue: `"libc_minor":"35"`, invalidValue: `"libc_minor":"-1"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := strings.Replace(valid, test.oldValue, test.invalidValue, 1)
			if _, err := ParseInterpreterInspectionOutput([]byte(output), []string{}); err == nil {
				t.Fatalf("inspection accepted malformed %s: %s", test.name, output)
			}
		})
	}
}

func TestInterpreterInspectionAndResolverShareInstalledPipIsolation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live isolation probe requires a host Python at a Linux-style absolute path")
	}
	python := isolatedPythonForTest(t)
	targetArchitecture := "x86_64"
	otherArchitecture := "aarch64"
	if runtime.GOARCH == "arm64" {
		targetArchitecture, otherArchitecture = "aarch64", "x86_64"
	} else if runtime.GOARCH != "amd64" {
		t.Fatalf("pip inspection test does not support host architecture %q", runtime.GOARCH)
	}
	shadow := t.TempDir()
	if err := os.Mkdir(filepath.Join(shadow, "pip"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shadow, "pip", "__init__.py"), []byte(`raise RuntimeError("current-directory pip shadow imported")`), 0o644); err != nil {
		t.Fatal(err)
	}
	argv, err := InterpreterInspectionArgv(python, []string{"py3-none-any"}, targetArchitecture)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(argv[0], argv[1:]...)
	command.Dir = shadow
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated inspection imported a shadow or failed: %v: %s", err, output)
	}
	facts, err := ParseInterpreterInspectionOutput(output, []string{"py3-none-any"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(facts.CompatibleTags, []string{"py3-none-any"}) {
		t.Fatalf("compatible tags = %#v", facts.CompatibleTags)
	}
	mismatchArgv, err := InterpreterInspectionArgv(python, []string{"py3-none-any"}, otherArchitecture)
	if err != nil {
		t.Fatal(err)
	}
	mismatch := exec.Command(mismatchArgv[0], mismatchArgv[1:]...)
	mismatch.Dir = shadow
	if output, err := mismatch.CombinedOutput(); err == nil || !strings.Contains(string(output), "architecture does not match selected target") {
		t.Fatalf("architecture mismatch error = %v: %s", err, output)
	}
	prefix, err := IsolatedInterpreterCommandPrefixV2(python)
	if err != nil {
		t.Fatal(err)
	}
	resolver := exec.Command(prefix[0], append(prefix[1:], "-m", "pip", "--version")...)
	resolver.Dir = shadow
	resolverOutput, err := resolver.CombinedOutput()
	if err != nil || !strings.Contains(strings.ToLower(string(resolverOutput)), "pip") {
		t.Fatalf("isolated resolver did not execute installed pip: %v: %s", err, resolverOutput)
	}
}

func TestInterpreterInspectionUsesGeneratedTagArchitecture(t *testing.T) {
	python := isolatedPythonForTest(t)
	run := func(t *testing.T, platformTag, target string) ([]byte, error) {
		t.Helper()
		const generatedTags = "all_tags=list(tags.sys_tags())"
		replacement := `all_tags=[tags.Tag("cp313","cp313","` + platformTag + `"),tags.Tag("py3","none","any")]`
		program := strings.Replace(interpreterInspectionProgramV2, generatedTags, replacement, 1)
		if program == interpreterInspectionProgramV2 {
			t.Fatal("inspection fixture did not replace generated tags")
		}
		return exec.Command(python, "-I", "-c", program, "[]", target).CombinedOutput()
	}
	for _, test := range []struct {
		name        string
		platformTag string
		target      string
	}{
		{name: "amd64", platformTag: "linux_x86_64", target: "x86_64"},
		{name: "arm64", platformTag: "linux_aarch64", target: "aarch64"},
		{name: "armv7", platformTag: "linux_armv7l", target: "armv7l"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, err := run(t, test.platformTag, test.target)
			if err != nil {
				t.Fatalf("matching generated architecture failed: %v: %s", err, output)
			}
			if _, err := ParseInterpreterInspectionOutput(output, []string{}); err != nil {
				t.Fatalf("parse matching generated architecture: %v: %s", err, output)
			}
		})
	}

	target, wrongPlatform := "x86_64", "linux_i686"
	if runtime.GOARCH == "arm64" {
		target, wrongPlatform = "aarch64", "linux_x86_64"
	} else if runtime.GOARCH != "amd64" {
		t.Skipf("wrong-bitness regression does not support host architecture %q", runtime.GOARCH)
	}
	output, err := run(t, wrongPlatform, target)
	if err == nil || !strings.Contains(string(output), "architecture does not match selected target") {
		t.Fatalf("generated architecture mismatch error = %v: %s", err, output)
	}
}

func testInterpreterFactsV2(version string, testedTags, compatibleTags []string) InterpreterInspectionFactsV2 {
	if testedTags == nil {
		testedTags = []string{}
	}
	if compatibleTags == nil {
		compatibleTags = []string{}
	}
	return InterpreterInspectionFactsV2{
		Version: version, Implementation: "cpython", ABI: "cp313",
		Libc: "glibc", LibcMajor: "2", LibcMinor: "35",
		TestedTags: append([]string{}, testedTags...), CompatibleTags: append([]string{}, compatibleTags...),
	}
}

func isolatedPythonForTest(t *testing.T) string {
	t.Helper()
	executable, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("python3 is required for pip inspection tests")
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(executable)
}

func TestSelectedPipManylinuxPolicyHooksRemainAuthoritative(t *testing.T) {
	python := isolatedPythonForTest(t)
	program := `import inspect,json,sys,types
from pip._vendor.packaging import _manylinux
_manylinux._have_compatible_abi=lambda *args:True
def generated(arch,release,module):
    _manylinux._get_glibc_version=lambda:release
    if module is None: sys.modules.pop("_manylinux",None)
    else: sys.modules["_manylinux"]=module
    parameters=inspect.signature(_manylinux.platform_tags).parameters
    if len(parameters)==1: return set(_manylinux.platform_tags([arch]))
    return set(_manylinux.platform_tags("linux_"+arch,arch))
def module(mode):
    if mode=="absent": return None
    if mode=="true": return types.SimpleNamespace(manylinux_compatible=lambda *args:True)
    if mode=="false": return types.SimpleNamespace(manylinux_compatible=lambda *args:False)
    if mode=="none": return types.SimpleNamespace(manylinux_compatible=lambda *args:None)
    if mode=="non_monotonic": return types.SimpleNamespace(manylinux_compatible=lambda major,minor,arch:minor!=16)
    if mode=="raising": return types.SimpleNamespace(manylinux_compatible=lambda *args:(_ for _ in ()).throw(RuntimeError("hook raised")))
    attribute,value=mode.rsplit("_",1)
    return types.SimpleNamespace(**{attribute:value=="true"})
cases=[
    ("absent","x86_64",(2,17),"absent","manylinux_2_17_x86_64"),
    ("true","x86_64",(2,17),"true","manylinux_2_17_x86_64"),
    ("false","x86_64",(2,17),"false","manylinux_2_17_x86_64"),
    ("none","x86_64",(2,17),"none","manylinux_2_17_x86_64"),
    ("non_monotonic_keep","x86_64",(2,17),"non_monotonic","manylinux_2_17_x86_64"),
    ("non_monotonic_drop","x86_64",(2,17),"non_monotonic","manylinux_2_16_x86_64"),
    ("legacy1_true","x86_64",(2,17),"manylinux1_compatible_true","manylinux1_x86_64"),
    ("legacy1_false","x86_64",(2,17),"manylinux1_compatible_false","manylinux1_x86_64"),
    ("legacy2010_true","x86_64",(2,17),"manylinux2010_compatible_true","manylinux2010_x86_64"),
    ("legacy2010_false","x86_64",(2,17),"manylinux2010_compatible_false","manylinux2010_x86_64"),
    ("legacy2014_true","x86_64",(2,17),"manylinux2014_compatible_true","manylinux2014_x86_64"),
    ("legacy2014_false","x86_64",(2,17),"manylinux2014_compatible_false","manylinux2014_x86_64"),
    ("raising","x86_64",(2,17),"raising","manylinux_2_17_x86_64")]
result={}
for name,arch,release,mode,tag in cases:
    try: result[name]={"member":tag in generated(arch,release,module(mode)),"raised":False}
    except RuntimeError: result[name]={"member":False,"raised":True}
print(json.dumps(result,sort_keys=True,separators=(",",":")))`
	output, err := exec.Command(python, "-I", "-c", program).CombinedOutput()
	if err != nil {
		t.Fatalf("selected pip manylinux generator failed: %v: %s", err, output)
	}
	var results map[string]struct {
		Member bool `json:"member"`
		Raised bool `json:"raised"`
	}
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatalf("decode hook results: %v: %s", err, output)
	}
	want := map[string]bool{
		"absent": true, "true": true, "false": false, "none": true,
		"non_monotonic_keep": true, "non_monotonic_drop": false,
		"legacy1_true": true, "legacy1_false": false,
		"legacy2010_true": true, "legacy2010_false": false,
		"legacy2014_true": true, "legacy2014_false": false,
	}
	for name, member := range want {
		if result, ok := results[name]; !ok || result.Raised || result.Member != member {
			t.Errorf("%s = %#v, found %v, want member %v", name, result, ok, member)
		}
	}
	if result, ok := results["raising"]; !ok || !result.Raised {
		t.Fatalf("raising hook = %#v, found %v", result, ok)
	}
}
