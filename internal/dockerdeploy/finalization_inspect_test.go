package dockerdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func finalizedInspectionJSON(t *testing.T, request FinalizationBuildRequest, imageID string, mutateLabels func(map[string]string), rootDiffID string, user string) string {
	t.Helper()
	labels := map[string]string{}
	for name, value := range request.Source.Labels {
		labels[name] = value
	}
	validationLabels, err := deploy.PrefixValidationLabels(request.Source.Image.RootFSSubject, request.ValidationReference)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range validationLabels {
		labels[label.Name] = label.Value
	}
	if mutateLabels != nil {
		mutateLabels(labels)
	}
	encodedLabels, err := json.Marshal(labels)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`[{"Id":%q,"RepoDigests":[],"Os":"linux","Architecture":"amd64","RootFS":{"Layers":[%q]},"Config":{"Env":[],"User":%q,"Entrypoint":[],"Cmd":[],"OnBuild":[],"Volumes":{},"Labels":%s}}]`, imageID, rootDiffID, user, encodedLabels)
}

func TestInspectFinalizedImageCandidateAcceptsOnlyValidationLabelChange(t *testing.T) {
	_, request := finalizationBuildFixture(t)
	built := BuiltImageCandidate{ImageID: rendererDigest("5")}
	inspection := finalizedInspectionJSON(t, request, string(built.ImageID), nil, string(request.Source.Descriptor.RootFSDiffIDs[0]), "")
	var gotArgs []string
	result, err := inspectFinalizedImageCandidate(context.Background(), built, request, func(_ context.Context, args ...string) (string, error) {
		gotArgs = append([]string{}, args...)
		return inspection, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotArgs, []string{"image", "inspect", string(built.ImageID)}) {
		t.Fatalf("Docker args = %v", gotArgs)
	}
	if result.Image.RootFSSubject != request.Source.Image.RootFSSubject {
		t.Fatalf("rootfs subject = %s, want %s", result.Image.RootFSSubject, request.Source.Image.RootFSSubject)
	}
}

func TestInspectFinalizedImageCandidateRejectsFilesystemOrLabelDrift(t *testing.T) {
	_, request := finalizationBuildFixture(t)
	built := BuiltImageCandidate{ImageID: rendererDigest("6")}
	tests := []struct {
		name         string
		rootDiffID   string
		mutateLabels func(map[string]string)
		user         string
		want         string
	}{
		{name: "rootfs", rootDiffID: string(rendererDigest("9")), want: "rootfs subject"},
		{name: "config", rootDiffID: string(request.Source.Descriptor.RootFSDiffIDs[0]), user: "unexpected", want: "non-label image configuration"},
		{name: "missing validation label", rootDiffID: string(request.Source.Descriptor.RootFSDiffIDs[0]), mutateLabels: func(labels map[string]string) {
			delete(labels, deploy.ValidationRecordLabel)
		}, want: "missing"},
		{name: "extra ordinary label", rootDiffID: string(request.Source.Descriptor.RootFSDiffIDs[0]), mutateLabels: func(labels map[string]string) {
			labels["org.example.extra"] = "unexpected"
		}, want: "outside the validation contract"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inspection := finalizedInspectionJSON(t, request, string(built.ImageID), test.mutateLabels, test.rootDiffID, test.user)
			result, err := inspectFinalizedImageCandidate(context.Background(), built, request, func(context.Context, ...string) (string, error) {
				return inspection, nil
			})
			if err == nil || !reflect.DeepEqual(result, InspectedImageCandidate{}) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("result = %#v, error = %v, want %q", result, err, test.want)
			}
		})
	}
}
