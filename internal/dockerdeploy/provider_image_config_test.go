package dockerdeploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

func TestProviderFinalImageConfigV1PreservesOnlyBaseEnvironment(t *testing.T) {
	base := deploy.BaseConfig{
		Schema: deploy.BaseConfigSchemaV1,
		Environment: []deploy.ConfigEnvironmentVariable{
			{Name: "HOME", Value: "/custom"},
			{Name: "PATH", Value: "/custom/bin"},
		},
		User:        "1234:5678",
		WorkingDir:  "/base-work",
		Entrypoint:  []string{"/base-entry"},
		Command:     []string{"base-argument"},
		Healthcheck: `{"test":["CMD","false"]}`,
		StopSignal:  "SIGKILL",
		OnBuild:     []string{},
		Volumes:     []string{},
	}

	got, err := ProviderFinalImageConfigV1(base)
	if err != nil {
		t.Fatal(err)
	}
	want := providers.ImageConfigPolicy{
		User: "0:0", WorkingDir: "/",
		Environment: []providers.EnvironmentVariable{
			{Name: "HOME", Value: "/custom"},
			{Name: "PATH", Value: "/custom/bin"},
		},
		Entrypoint: []string{}, Command: []string{}, Healthcheck: providers.ImageHealthcheckNone,
		StopSignal: "SIGTERM", Labels: []providers.ImageLabel{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("policy = %#v, want %#v", got, want)
	}
}

func TestProviderFinalImageConfigV1RejectsUnsupportedBaseBuildBehavior(t *testing.T) {
	valid := deploy.BaseConfig{
		Schema: deploy.BaseConfigSchemaV1, Environment: []deploy.ConfigEnvironmentVariable{},
		Entrypoint: []string{}, Command: []string{}, OnBuild: []string{}, Volumes: []string{},
	}
	for _, test := range []struct {
		name   string
		mutate func(*deploy.BaseConfig)
		want   string
	}{
		{name: "OnBuild", mutate: func(value *deploy.BaseConfig) { value.OnBuild = []string{"RUN hidden"} }, want: "OnBuild"},
		{name: "volume", mutate: func(value *deploy.BaseConfig) { value.Volumes = []string{"/data"} }, want: "volumes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := valid
			test.mutate(&base)
			_, err := ProviderFinalImageConfigV1(base)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestProviderFinalImageConfigV1RejectsMalformedBaseBeforeProjection(t *testing.T) {
	_, err := ProviderFinalImageConfigV1(deploy.BaseConfig{})
	if err == nil || !strings.Contains(err.Error(), "base config schema") {
		t.Fatalf("error = %v", err)
	}
}
