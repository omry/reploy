package probe

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func validRequest() RequestV1 {
	return RequestV1{
		Schema: RequestSchemaV1,
		Inspections: []ExecutableInspectionV1{
			{ID: "python", InvocationPath: "/usr/bin/python3"},
		},
	}
}

func TestRequiredAccessPathsCoverInvocationAndTargetTrees(t *testing.T) {
	links := []LinkObservationV1{{
		Path: "/entry/bin/tool", Target: "/target/bin/tool", ResolvedPath: "/target/bin/tool",
	}}
	want := map[string]string{
		"/":                "directory",
		"/entry":           "directory",
		"/entry/bin":       "directory",
		"/target":          "directory",
		"/target/bin":      "directory",
		"/target/bin/tool": "regular",
	}
	if got := requiredAccessPaths("/entry/bin/tool", links, "/target/bin/tool"); !reflect.DeepEqual(got, want) {
		t.Fatalf("access paths = %#v, want %#v", got, want)
	}
}

func TestDecodeRequestV1RequiresCanonicalClosedJSON(t *testing.T) {
	request := validRequest()
	content, err := canonical.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRequestV1(content)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != request.Schema || len(decoded.Inspections) != 1 {
		t.Fatalf("decoded request = %#v", decoded)
	}
	for _, invalid := range [][]byte{
		[]byte(`{"inspections":[],"schema":"reploy-probe-request-v1","unknown":true}`),
		[]byte(`{"schema":"reploy-probe-request-v1","inspections":[]}`),
		append(append([]byte{}, content...), []byte(` {}`)...),
	} {
		if _, err := DecodeRequestV1(invalid); err == nil {
			t.Fatalf("invalid request accepted: %s", invalid)
		}
	}
}

func TestDecodeResponseV1RequiresCanonicalClosedRequestBoundJSON(t *testing.T) {
	request := RequestV1{Schema: RequestSchemaV1, Inspections: []ExecutableInspectionV1{}}
	response := ResponseV1{Schema: ResponseSchemaV1, Observations: []ExecutableObservationV1{}}
	content, err := canonical.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeResponseV1(request, content); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{
		[]byte(`{"observations":[],"schema":"reploy-probe-response-v1","unknown":true}`),
		[]byte(`{"schema":"reploy-probe-response-v1","observations":[]}`),
		append(append([]byte{}, content...), []byte(` {}`)...),
	} {
		if _, err := DecodeResponseV1(request, invalid); err == nil {
			t.Fatalf("invalid response accepted: %s", invalid)
		}
	}
	mismatched := validRequest()
	if _, err := DecodeResponseV1(mismatched, content); err == nil {
		t.Fatal("response for a different request was accepted")
	}
}

func TestValidateRequestV1RejectsAmbiguousInspections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RequestV1)
		want   string
	}{
		{name: "nil", mutate: func(value *RequestV1) { value.Inspections = nil }, want: "array"},
		{name: "id", mutate: func(value *RequestV1) { value.Inspections[0].ID = "Python" }, want: "ID"},
		{name: "path", mutate: func(value *RequestV1) { value.Inspections[0].InvocationPath = "python" }, want: "absolute"},
		{name: "order", mutate: func(value *RequestV1) {
			value.Inspections = append(value.Inspections, ExecutableInspectionV1{ID: "apt", InvocationPath: "/usr/bin/apt"})
		}, want: "sorted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest()
			test.mutate(&request)
			if err := ValidateRequestV1(request); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestMainUsesCanonicalStdinStdoutProtocol(t *testing.T) {
	request := RequestV1{Schema: RequestSchemaV1, Inspections: []ExecutableInspectionV1{}}
	content, err := canonical.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Main(nil, bytes.NewReader(content), &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d; stderr = %s", code, stderr.String())
	}
	want, err := canonical.Marshal(ResponseV1{Schema: ResponseSchemaV1, Observations: []ExecutableObservationV1{}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stdout.Bytes(), want) || stderr.Len() != 0 {
		t.Fatalf("stdout = %s; stderr = %s", stdout.String(), stderr.String())
	}
	if code := Main([]string{"inspect"}, bytes.NewReader(content), &stdout, &stderr); code != 2 {
		t.Fatalf("argument error code = %d", code)
	}
}

func TestMainHoldIsFixedLifecycleOnly(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	waits := 0
	code := mainWithHold([]string{"hold"}, strings.NewReader("ignored"), &stdout, &stderr, func() error {
		waits++
		return nil
	})
	if code != 0 || waits != 1 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("hold code=%d waits=%d stdout=%q stderr=%q", code, waits, stdout.String(), stderr.String())
	}
	code = mainWithHold([]string{"hold", "anything"}, strings.NewReader(""), &stdout, &stderr, func() error {
		t.Fatal("invalid hold arguments reached waiter")
		return nil
	})
	if code != 2 || !strings.Contains(stderr.String(), "fixed hold mode") {
		t.Fatalf("invalid hold code=%d stderr=%q", code, stderr.String())
	}
}

func TestMainCopyVolumeTreeIsFixedLifecycleOnly(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	copies := 0
	code := mainWithActions(
		[]string{"copy-volume-tree"}, strings.NewReader("ignored"), &stdout, &stderr,
		func() error { t.Fatal("copy mode reached hold"); return nil },
		func() error { copies++; return nil },
		func([]string) error { t.Fatal("copy mode reached transient runner"); return nil },
	)
	if code != 0 || copies != 1 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("copy code=%d copies=%d stdout=%q stderr=%q", code, copies, stdout.String(), stderr.String())
	}
	code = mainWithActions(
		[]string{"copy-volume-tree", "anything"}, strings.NewReader(""), &stdout, &stderr,
		func() error { return nil }, func() error { t.Fatal("invalid copy arguments reached copier"); return nil },
		func([]string) error { t.Fatal("invalid copy arguments reached transient runner"); return nil },
	)
	if code != 2 || !strings.Contains(stderr.String(), "fixed copy-volume-tree mode") {
		t.Fatalf("invalid copy code=%d stderr=%q", code, stderr.String())
	}
}

func TestMainRunTransientForwardsExactArguments(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var got []string
	code := mainWithActions(
		[]string{"run-transient", "501", "20", "/opt/demo", "--flag", "value"},
		strings.NewReader("ignored"), &stdout, &stderr,
		func() error { t.Fatal("transient mode reached hold"); return nil },
		func() error { t.Fatal("transient mode reached copy"); return nil },
		func(args []string) error { got = append([]string{}, args...); return nil },
	)
	want := []string{"501", "20", "/opt/demo", "--flag", "value"}
	if code != 0 || !reflect.DeepEqual(got, want) || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d args=%#v stdout=%q stderr=%q", code, got, stdout.String(), stderr.String())
	}
}

func TestRunFixedTransientRejectsInvalidIdentityAndCommand(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"01", "20", "/opt/demo"},
		{"501", "-1", "/opt/demo"},
		{"501", "20", "demo"},
	} {
		if err := runFixedTransient(args); err == nil {
			t.Fatalf("arguments accepted: %#v", args)
		}
	}
}
