package dockerdeploy

import (
	"errors"
	"reflect"
	"testing"
)

func TestConfigureProviderInstallHostV1RunsOnlyPlannedConfiguration(t *testing.T) {
	commands := providerInstallHostCommandsV1{
		Configure: []CommandSpec{
			{Name: "/usr/bin/systemctl", Args: []string{"daemon-reload"}},
			{Name: "/usr/bin/systemctl", Args: []string{"enable", "demo.service"}},
		},
		Start: CommandSpec{Name: "/usr/bin/systemctl", Args: []string{"restart", "demo.service"}},
	}
	var ran []CommandSpec
	err := configureProviderInstallHostWithV1(t.Context(), commands, RunOptions{}, func(spec CommandSpec, options RunOptions) error {
		if options.Context != t.Context() || options.DockerPreflightTimeout != 0 {
			t.Fatalf("run options = %#v", options)
		}
		ran = append(ran, spec)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ran, commands.Configure) {
		t.Fatalf("commands = %#v, want %#v", ran, commands.Configure)
	}
}

func TestStartProviderInstallHostV1RunsOneCommandWithoutPreflight(t *testing.T) {
	commands := providerInstallHostCommandsV1{
		Configure: []CommandSpec{},
		Start:     CommandSpec{Name: "/usr/bin/docker", Args: []string{"compose", "up", "-d"}},
	}
	called := 0
	err := startProviderInstallHostWithV1(t.Context(), commands, RunOptions{}, func(spec CommandSpec, options RunOptions) error {
		called++
		if options.Context != t.Context() || !reflect.DeepEqual(spec, commands.Start) {
			t.Fatalf("spec=%#v options=%#v", spec, options)
		}
		return nil
	})
	if err != nil || called != 1 {
		t.Fatalf("called=%d error=%v", called, err)
	}
}

func TestConfigureProviderInstallHostV1StopsAtFirstFailure(t *testing.T) {
	want := errors.New("enable failed")
	commands := providerInstallHostCommandsV1{Configure: []CommandSpec{{Name: "one"}, {Name: "two"}, {Name: "three"}}}
	called := 0
	err := configureProviderInstallHostWithV1(t.Context(), commands, RunOptions{}, func(CommandSpec, RunOptions) error {
		called++
		if called == 2 {
			return want
		}
		return nil
	})
	if !errors.Is(err, want) || called != 2 {
		t.Fatalf("called=%d error=%v", called, err)
	}
}
