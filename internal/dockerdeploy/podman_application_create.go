package dockerdeploy

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const podmanApplicationAttemptLabel = "io.reploy.application-create-attempt"

type podmanApplicationCreate struct {
	Image, User  string
	Entrypoint   []string
	Cmd          []string
	Env          []string
	Labels       map[string]string
	OpenStdin    bool
	StdinOnce    bool
	AttachStdin  bool
	AttachStdout bool
	AttachStderr bool
	Tty          bool
	HostConfig   struct {
		UsernsMode, NetworkMode           string
		ReadonlyRootfs, AutoRemove        bool
		CapDrop, CapAdd, SecurityOpt, Dns []string
		Tmpfs                             map[string]string
		Mounts                            []podmanApplicationMount
	}
}

type podmanApplicationMount struct {
	Type, Source, Target string
	ReadOnly             bool
}

// Parse only the engine-option prefix emitted by our application renderers.
// Docker CLI rejects keep-id before transport, so send this one operation
// through the selected engine's compatible API. Lifecycle/attach still use the
// existing command runner. This is not an arbitrary Docker command parser.
func podmanApplicationCreateRequest(spec CommandSpec, namespace string) (podmanApplicationCreate, string, error) {
	var body podmanApplicationCreate
	body.Labels = map[string]string{}
	body.HostConfig.UsernsMode = namespace
	body.HostConfig.Tmpfs = map[string]string{}
	body.AttachStdout, body.AttachStderr = true, true
	entry := slices.Index(spec.Args, "--entrypoint")
	if spec.builtinSeccompIndex <= 0 || entry < 1 || entry+4 >= len(spec.Args) || spec.Args[entry+1] != "/reploy-probe" {
		return body, "", fmt.Errorf("same-ID create requires a generated Reploy application request")
	}
	body.Image, body.User = spec.Args[entry+2], "0:0"
	body.Entrypoint = []string{"/reploy-probe"}
	body.Cmd = slices.Clone(spec.Args[entry+3:])
	name := ""
	seccomp, noNewPrivileges := false, false
	seen := map[string]bool{}
	for i := 1; i < entry; i++ {
		key := spec.Args[i]
		switch key {
		case "--rm", "--read-only", "--interactive", "--tty":
			if seen[key] {
				return body, "", fmt.Errorf("duplicate generated application option %q", key)
			}
			seen[key] = true
			switch key {
			case "--rm":
				body.HostConfig.AutoRemove = true
			case "--read-only":
				body.HostConfig.ReadonlyRootfs = true
			case "--interactive":
				body.OpenStdin, body.StdinOnce, body.AttachStdin = true, true, true
			case "--tty":
				body.Tty = true
			}
			continue
		}
		if i+1 >= entry {
			return body, "", fmt.Errorf("incomplete generated application option %q", key)
		}
		value := spec.Args[i+1]
		i++
		switch key {
		case "--name", "--user", "--pull", "--network":
			if seen[key] {
				return body, "", fmt.Errorf("duplicate generated application option %q", key)
			}
			seen[key] = true
			switch key {
			case "--name":
				name = value
			case "--user":
				if value != "0:0" {
					return body, "", fmt.Errorf("application setup must use container root")
				}
			case "--pull":
				if value != "never" {
					return body, "", fmt.Errorf("application image must already exist locally")
				}
			case "--network":
				body.HostConfig.NetworkMode = value
			}
		case "--cap-drop":
			body.HostConfig.CapDrop = append(body.HostConfig.CapDrop, value)
		case "--cap-add":
			if !slices.Contains([]string{"CHOWN", "NET_ADMIN", "SETGID", "SETPCAP", "SETUID"}, value) || slices.Contains(body.HostConfig.CapAdd, value) {
				return body, "", fmt.Errorf("unsupported or duplicate application setup capability")
			}
			body.HostConfig.CapAdd = append(body.HostConfig.CapAdd, value)
		case "--security-opt":
			if strings.HasPrefix(value, "seccomp=") {
				profile, err := materializePodmanDockerSeccomp()
				if err != nil {
					return body, "", err
				}
				if seccomp || value != "seccomp="+profile {
					return body, "", fmt.Errorf("same-ID create requires one pinned Docker seccomp profile")
				}
				seccomp = true
				// As Docker CLI does, transport profile contents, not a client
				// filesystem path. This retains the HF8 #29726 requirement.
				value = "seccomp=" + string(podmanDockerSeccompProfile)
			} else if value == "no-new-privileges=true" && !noNewPrivileges {
				noNewPrivileges = true
			} else {
				return body, "", fmt.Errorf("unsupported or duplicate application security option")
			}
			body.HostConfig.SecurityOpt = append(body.HostConfig.SecurityOpt, value)
		case "--tmpfs":
			target, options, ok := strings.Cut(value, ":")
			if _, duplicate := body.HostConfig.Tmpfs[target]; !ok || target == "" || duplicate {
				return body, "", fmt.Errorf("invalid generated tmpfs mount")
			}
			body.HostConfig.Tmpfs[target] = options
		case "--env":
			body.Env = append(body.Env, value)
		case "--dns":
			body.HostConfig.Dns = append(body.HostConfig.Dns, value)
		case "--add-host":
			return body, "", fmt.Errorf("network-enabled controlled sessions are not supported by this Podman compatibility path")
		case "--label":
			key, value, ok := strings.Cut(value, "=")
			if _, duplicate := body.Labels[key]; !ok || key == "" || duplicate || key == podmanApplicationAttemptLabel {
				return body, "", fmt.Errorf("invalid generated application label")
			}
			body.Labels[key] = value
		case "--mount":
			mount, err := parsePodmanApplicationMount(value)
			if err != nil {
				return body, "", err
			}
			body.HostConfig.Mounts = append(body.HostConfig.Mounts, mount)
		case "--userns":
			return body, "", podmanNamespaceConflict(value)
		default:
			return body, "", fmt.Errorf("unsupported generated application option %q", key)
		}
	}
	if name == "" || !seen["--user"] || !seen["--pull"] || !seccomp || !noNewPrivileges || !body.HostConfig.ReadonlyRootfs || !slices.Equal(body.HostConfig.CapDrop, []string{"ALL"}) {
		return body, "", fmt.Errorf("incomplete generated application creation policy")
	}
	return body, name, nil
}

func parsePodmanApplicationMount(value string) (podmanApplicationMount, error) {
	var mount podmanApplicationMount
	records, err := csv.NewReader(strings.NewReader(value)).ReadAll()
	if err != nil || len(records) != 1 {
		return mount, fmt.Errorf("invalid generated application mount")
	}
	seen := map[string]bool{}
	for _, field := range records[0] {
		key, value, _ := strings.Cut(field, "=")
		if seen[key] {
			return mount, fmt.Errorf("duplicate generated mount field %q", key)
		}
		seen[key] = true
		switch key {
		case "type":
			mount.Type = value
		case "source":
			mount.Source = value
		case "target":
			mount.Target = value
		case "readonly":
			if field != "readonly" {
				return mount, fmt.Errorf("invalid generated readonly mount flag")
			}
			mount.ReadOnly = true
		default:
			return mount, fmt.Errorf("unsupported generated mount field %q", key)
		}
	}
	if !slices.Contains([]string{"bind", "volume", "tmpfs"}, mount.Type) || !strings.HasPrefix(mount.Target, "/") || mount.Type == "bind" && mount.Source == "" {
		return mount, fmt.Errorf("incomplete generated application mount")
	}
	return mount, nil
}

func (client *podmanHealthcheckClient) createSameIDApplication(ctx context.Context, spec CommandSpec, namespace string, raw commandRunner) (string, error) {
	body, name, err := podmanApplicationCreateRequest(spec, namespace)
	if err != nil {
		return "", err
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	attempt := hex.EncodeToString(token[:])
	body.Labels[podmanApplicationAttemptLabel] = attempt
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker/v1.44/containers/create?name="+url.QueryEscape(name), strings.NewReader(string(data)))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.http.Do(request)
	status := 0
	var result struct{ ID string }
	if err == nil {
		status = response.StatusCode
		data, readErr := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
		_ = response.Body.Close()
		if status != http.StatusCreated || readErr != nil || len(data) > 64<<10 || json.Unmarshal(data, &result) != nil || !validPodmanHealthcheckContainerID(result.ID) {
			err = fmt.Errorf("Podman application create did not return one exact container ID (HTTP %d)", status)
		}
	}
	if err == nil {
		return result.ID, nil
	}
	// A timeout or malformed response may follow a successful creation. Only
	// reconcile the exact attempted name carrying this operation's random label.
	// A name collision is never ours. Never expose request/response bodies that
	// can contain application environment values.
	if status != http.StatusConflict {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		data, inspectErr := client.request(cleanup, http.MethodGet, "/v1.44/containers/"+url.PathEscape(name)+"/json", nil)
		var object struct {
			ID     string
			Config struct{ Labels map[string]string }
		}
		if inspectErr == nil && json.Unmarshal(data, &object) == nil && validPodmanHealthcheckContainerID(object.ID) && object.Config.Labels[podmanApplicationAttemptLabel] == attempt {
			err = errors.Join(err, removeCreatedPodmanContainer(cleanup, spec, object.ID, raw))
		}
	}
	return "", fmt.Errorf("create same-ID Podman application %q failed; reconcile resources bearing attempt %s: %w", name, attempt, err)
}
