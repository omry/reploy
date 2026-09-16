package portabletool_test

import (
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/portabletool"
)

func TestValidateWheelTagV1AcceptsCanonicalTags(t *testing.T) {
	t.Parallel()
	for _, tag := range []string{
		"py3-none-any",
		"custom_python-abi_123-platform_x86_64",
		"a-b-c",
	} {
		if err := portabletool.ValidateWheelTagV1(tag); err != nil {
			t.Errorf("canonical tag %q rejected: %v", tag, err)
		}
	}
}

func TestValidateWheelTagV1RejectsMalformedAndCompressedTags(t *testing.T) {
	t.Parallel()
	for _, tag := range []string{
		"",
		"py3-none",
		"py3-none-any-extra",
		"py3..none-any",
		"py3.cp313-none-any",
		"Py3-none-any",
		"py3-none-any-é",
	} {
		if err := portabletool.ValidateWheelTagV1(tag); err == nil {
			t.Errorf("malformed tag %q was accepted", tag)
		}
	}
}

func TestProjectWheelFilenameV1ExpandsCompressedTagsBeforeValidation(t *testing.T) {
	t.Parallel()
	projection, err := portabletool.ProjectWheelFilenameV1(
		"demo_pkg-1.2.0-py3.cp313-none-manylinux1_x86_64.manylinux2014_x86_64.whl",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"cp313-none-manylinux1_x86_64",
		"cp313-none-manylinux2014_x86_64",
		"py3-none-manylinux1_x86_64",
		"py3-none-manylinux2014_x86_64",
	}
	if !reflect.DeepEqual(projection.Tags, want) {
		t.Fatalf("expanded tags = %#v, want %#v", projection.Tags, want)
	}
}
