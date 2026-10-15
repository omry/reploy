package dockerdeploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

type podmanPullTransport func(*http.Request) (*http.Response, error)

func (f podmanPullTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func podmanBaseInspection(id, arch string) string {
	return fmt.Sprintf(`[{"Id":%q,"RepoDigests":[%q],"Os":"linux","Architecture":%q,"RootFS":{"Layers":[%q]},"Config":{"Env":["HOME=/root"]}}]`, id, "debian@sha256:"+strings.Repeat("2", 64), arch, "sha256:"+strings.Repeat("3", 64))
}
func podmanPullClient(t *testing.T, body string, check func(*http.Request)) *http.Client {
	t.Helper()
	return &http.Client{Transport: podmanPullTransport(func(r *http.Request) (*http.Response, error) {
		if check != nil {
			check(r)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
}
func podmanPullBody(id string) string { return fmt.Sprintf(`{"id":%q,"images":[%q]}`, id, id) }
func isolatePodmanBaseAuth(t *testing.T) {
	t.Helper()
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	// Prevent auto-detected host credential helpers from entering fixture tests.
	t.Setenv("PATH", t.TempDir())
}

func TestNativePodmanBasePullUsesOperationID(t *testing.T) {
	isolatePodmanBaseAuth(t)
	id := "sha256:" + strings.Repeat("1", 64)
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	for _, author := range []string{"debian:13", "debian"} {
		t.Run(author, func(t *testing.T) {
			calls := 0
			client := podmanPullClient(t, podmanPullBody(strings.TrimPrefix(id, "sha256:")), func(r *http.Request) {
				calls++
				want := "docker.io/library/" + author
				if author == "debian" {
					want += ":latest"
				}
				q := r.URL.Query()
				if r.Method != "POST" || r.URL.Path != podmanBasePullPath || q.Get("reference") != want || q.Get("OS") != "linux" || q.Get("Arch") != "amd64" || q.Get("Variant") != "" || q.Get("quiet") != "true" || q.Get("policy") != "always" || q.Get("tlsVerify") != "" || q.Get("retry") != "2" {
					t.Fatalf("unexpected pull: %s %s", r.Method, r.URL)
				}
			})
			var inspected [][]string
			inspect := func(_ context.Context, args ...string) (string, error) {
				inspected = append(inspected, args)
				// A concurrently moved tag must not affect the operation's returned ID.
				if !reflect.DeepEqual(args, []string{"image", "inspect", id}) {
					t.Fatalf("mutable inspection: %v", args)
				}
				return podmanBaseInspection(id, "amd64"), nil
			}
			d, c, err := resolvePodmanBaseFromPull(t.Context(), author, platform, client, inspect)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || len(inspected) != 1 || d.AuthorReference != author || d.ImmutableReference != id || string(d.ConfigDigest) != id || d.ManifestDigest != "" || d.Platform != platform {
				t.Fatalf("wrong descriptor %#v", d)
			}
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
			image, err := realizedImageFromDescriptor(d)
			if err != nil || string(image.Digest) != id {
				t.Fatalf("build identity = %#v %v", image, err)
			}
		})
	}
}

func TestNativePodmanBasePullPinsAndVariants(t *testing.T) {
	isolatePodmanBaseAuth(t)
	id := "sha256:" + strings.Repeat("1", 64)
	digest := "sha256:" + strings.Repeat("2", 64)
	for _, author := range []string{"debian@" + digest, "debian:13@" + digest, "debian:13"} {
		for _, selected := range []string{"linux/arm", "linux/arm/v7"} {
			t.Run(author+selected, func(t *testing.T) {
				platform, _ := blueprint.ParsePlatform(selected)
				client := podmanPullClient(t, podmanPullBody(strings.TrimPrefix(id, "sha256:")), func(r *http.Request) {
					q := r.URL.Query()
					if q.Get("reference") != "docker.io/library/"+author || q.Get("Arch") != "arm" || q.Get("Variant") != platform.Variant {
						t.Fatalf("query=%v", q)
					}
				})
				inspection := strings.Replace(podmanBaseInspection(id, "arm"), `"Architecture":"arm"`, `"Architecture":"arm","Variant":"v7"`, 1)
				d, _, err := resolvePodmanBaseFromPull(t.Context(), author, platform, client, func(context.Context, ...string) (string, error) { return inspection, nil })
				if err != nil || d.AuthorReference != author || d.Platform != platform || string(d.ConfigDigest) != id {
					t.Fatalf("descriptor=%#v err=%v", d, err)
				}
				if strings.Contains(author, "@") && d.ManifestDigest != "" && string(d.ManifestDigest) != digest {
					t.Fatalf("lost author pin: %#v", d)
				}
			})
		}
	}
}

func TestNativePodmanBasePullRejectsInvalidEvidenceBeforeInspection(t *testing.T) {
	isolatePodmanBaseAuth(t)
	raw := strings.Repeat("1", 64)
	for name, body := range map[string]string{
		"malformed": "{", "empty": "{}", "short ID": podmanPullBody("1234"),
		"missing images":  fmt.Sprintf(`{"id":%q}`, raw),
		"different ID":    fmt.Sprintf(`{"id":%q,"images":[%q]}`, raw, strings.Repeat("2", 64)),
		"multiple images": fmt.Sprintf(`{"id":%q,"images":[%q,%q]}`, raw, raw, raw),
		"error":           fmt.Sprintf(`{"id":%q,"images":[%q],"error":"sensitive token"}`, raw, raw),
		"failed status":   fmt.Sprintf(`{"id":%q,"images":[%q],"status":"failure"}`, raw, raw),
		"oversized":       strings.Repeat(" ", podmanBasePullResponseLimit+1),
		"concatenated":    podmanPullBody(raw) + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			platform, _ := blueprint.ParsePlatform("linux/amd64")
			_, _, err := resolvePodmanBaseFromPull(t.Context(), "debian:13", platform, podmanPullClient(t, body, nil), func(context.Context, ...string) (string, error) { t.Fatal("inspected invalid pull"); return "", nil })
			if err == nil || strings.Contains(err.Error(), "sensitive token") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestNativePodmanBasePullRetainsImageSafetyValidation(t *testing.T) {
	isolatePodmanBaseAuth(t)
	id := "sha256:" + strings.Repeat("1", 64)
	for _, failure := range []string{"ID", "platform", "OnBuild", "Volumes", "rootfs", "inspection"} {
		t.Run(failure, func(t *testing.T) {
			inspection := podmanBaseInspection(id, "amd64")
			switch failure {
			case "ID":
				inspection = strings.Replace(inspection, id, "sha256:"+strings.Repeat("f", 64), 1)
			case "platform":
				inspection = strings.ReplaceAll(inspection, "amd64", "arm64")
			case "OnBuild":
				inspection = strings.Replace(inspection, `"Env":`, `"OnBuild":["RUN false"],"Env":`, 1)
			case "Volumes":
				inspection = strings.Replace(inspection, `"Env":`, `"Volumes":{"/data":{}},"Env":`, 1)
			case "rootfs":
				inspection = strings.ReplaceAll(inspection, "sha256:"+strings.Repeat("3", 64), "invalid")
			}
			platform, _ := blueprint.ParsePlatform("linux/amd64")
			_, _, err := resolvePodmanBaseFromPull(t.Context(), "debian:13", platform, podmanPullClient(t, podmanPullBody(strings.TrimPrefix(id, "sha256:")), nil), func(context.Context, ...string) (string, error) {
				if failure == "inspection" {
					return "", errors.New("unavailable")
				}
				return inspection, nil
			})
			if err == nil {
				t.Fatal("accepted unsafe or mismatched image")
			}
		})
	}
}

func TestNativePodmanBasePullHTTPFailureAndCancellation(t *testing.T) {
	isolatePodmanBaseAuth(t)
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	for _, code := range []int{302, 400, 401, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			client := &http.Client{Transport: podmanPullTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("secret password")), Header: make(http.Header)}, nil
			})}
			_, _, err := resolvePodmanBaseFromPull(t.Context(), "debian:13", platform, client, nil)
			if err == nil || !strings.Contains(err.Error(), fmt.Sprint(code)) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error=%v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := &http.Client{Transport: podmanPullTransport(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })}
	_, _, err := resolvePodmanBaseFromPull(ctx, "debian:13", platform, client, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestNativePodmanBaseCacheReusesExactConfigIDAndRejectsChange(t *testing.T) {
	id := "sha256:" + strings.Repeat("1", 64)
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	inspection := podmanBaseInspection(id, "amd64")
	locked, _, err := parseResolvedDockerBase(id, platform, []byte(inspection), "")
	if err != nil {
		t.Fatal(err)
	}
	locked.AuthorReference = "debian:13"
	for _, change := range []string{"none", "ID", "platform", "rootfs"} {
		t.Run(change, func(t *testing.T) {
			output := inspection
			switch change {
			case "ID":
				output = strings.ReplaceAll(output, id, "sha256:"+strings.Repeat("f", 64))
			case "platform":
				output = strings.ReplaceAll(output, "amd64", "arm64")
			case "rootfs":
				output = strings.ReplaceAll(output, strings.Repeat("3", 64), strings.Repeat("4", 64))
			}
			run := func(_ context.Context, args ...string) (string, error) {
				if !reflect.DeepEqual(args, []string{"image", "inspect", "debian:13"}) {
					t.Fatalf("registry access or immutable cache lookup: %v", args)
				}
				return output, nil
			}
			cached, _, found, err := inspectCachedBase(t.Context(), "debian:13", platform, run, &locked)
			if err != nil {
				t.Fatal(err)
			}
			exact := found && reflect.DeepEqual(cached, locked)
			if exact != (change == "none") {
				t.Fatalf("found=%v descriptor=%#v", found, cached)
			}
		})
	}
}

func TestResolveBaseNativeDispatchPreservesDockerAndPinnedCache(t *testing.T) {
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	id := "sha256:" + strings.Repeat("1", 64)
	for _, author := range []string{id, "debian@sha256:" + strings.Repeat("2", 64)} {
		calls := 0
		_, _, err := resolveBaseWithNativePull(t.Context(), author, platform, func(context.Context, ...string) (string, error) {
			calls++
			return podmanBaseInspection(id, "amd64"), nil
		}, func(context.Context, string, blueprint.Platform) (deploy.ImageDescriptor, deploy.BaseConfig, bool, error) {
			t.Fatal("native pull for local immutable input")
			return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, nil
		})
		if err != nil || calls != 1 {
			t.Fatalf("calls=%d error=%v", calls, err)
		}
	}
	sentinel := errors.New("native failed")
	_, _, err := resolveBaseWithNativePull(t.Context(), "debian:13", platform, func(context.Context, ...string) (string, error) {
		t.Fatal("Docker fallback after native failure")
		return "", nil
	}, func(context.Context, string, blueprint.Platform) (deploy.ImageDescriptor, deploy.BaseConfig, bool, error) {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, true, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v", err)
	}
	// Docker's missing-digest guard must remain strict, without registry lookup.
	calls := 0
	_, _, err = resolveBaseWithNativePull(t.Context(), "debian:13", platform, func(_ context.Context, args ...string) (string, error) {
		calls++
		if args[0] != "pull" {
			t.Fatalf("unexpected %v", args)
		}
		return "downloaded", nil
	}, func(context.Context, string, blueprint.Platform) (deploy.ImageDescriptor, deploy.BaseConfig, bool, error) {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, nil
	})
	if err == nil || calls != 1 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}

func TestNativePodmanBaseVersionFloor(t *testing.T) {
	for _, version := range []string{"6.1.4", "6.1.4-dev", "6.2.0-dev", "7.0.0"} {
		if !supportsNativePodmanBasePull(version) {
			t.Errorf("rejected %s", version)
		}
	}
	for _, version := range []string{"", "6.1.3", "6.0.99", "5.9.9", "6.1", "6.1.x", "bogus", "6.-1.4"} {
		if supportsNativePodmanBasePull(version) {
			t.Errorf("accepted %s", version)
		}
	}
}

func TestNativePodmanBaseAuthHeaderSelectsOneRegistry(t *testing.T) {
	isolatePodmanBaseAuth(t)
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"https://index.docker.io/v1/":{"auth":"`+base64.StdEncoding.EncodeToString([]byte("selected:fixture"))+`"},"unrelated.example":{"auth":"`+base64.StdEncoding.EncodeToString([]byte("other:do-not-forward"))+`"}}}`)
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	id := "sha256:" + strings.Repeat("1", 64)
	client := podmanPullClient(t, podmanPullBody(strings.TrimPrefix(id, "sha256:")), func(r *http.Request) {
		data, err := base64.URLEncoding.DecodeString(r.Header.Get("X-Registry-Auth"))
		if err != nil {
			t.Fatal(err)
		}
		var auth map[string]any
		if err := json.Unmarshal(data, &auth); err != nil {
			t.Fatal(err)
		}
		if auth["username"] != "selected" || auth["password"] != "fixture" || strings.Contains(string(data), "do-not-forward") {
			t.Fatal("selected credentials were not preserved")
		}
	})
	_, _, err := resolvePodmanBaseFromPull(t.Context(), "debian:13", platform, client, func(context.Context, ...string) (string, error) { return podmanBaseInspection(id, "amd64"), nil })
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativePodmanBaseCacheWithoutRepoDigests(t *testing.T) {
	id := "sha256:" + strings.Repeat("1", 64)
	platform, _ := blueprint.ParsePlatform("linux/amd64")
	output := strings.Replace(podmanBaseInspection(id, "amd64"), `"RepoDigests":["debian@sha256:`+strings.Repeat("2", 64)+`"]`, `"RepoDigests":[]`, 1)
	locked, _, err := parseResolvedDockerBase(id, platform, []byte(output), "")
	if err != nil {
		t.Fatal(err)
	}
	locked.AuthorReference = "debian:13"
	cached, _, found, err := inspectCachedBase(t.Context(), "debian:13", platform, func(context.Context, ...string) (string, error) { return output, nil }, &locked)
	if err != nil || !found || !reflect.DeepEqual(cached, locked) {
		t.Fatalf("unchanged config-ID cache missed: found=%v err=%v", found, err)
	}
}
