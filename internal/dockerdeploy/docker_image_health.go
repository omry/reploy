package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/omry/reploy/internal/canonical"
)

// Export is streamed, never extracted. Podman prepares a full server-side archive;
// these limits bound the client, not that server-side temporary disk allocation.
const imageHealthExportTimeout = 5 * time.Minute
const imageHealthArchiveLimit int64 = 64 << 30
const imageHealthConfigLimit int64 = 4 << 20
const imageHealthManifestLimit int64 = 1 << 20
const imageHealthEntryLimit = 10000
const imageHealthNameLimit = 8 << 20
const imageHealthCacheLimit = 256
const imageHealthStderrLimit = 8192

func restoreDockerInspectionHealthchecks(ctx context.Context, endpoint string, args []string, output string) (string, error) {
	if len(args) != 3 || args[0] != "image" || args[1] != "inspect" {
		return output, nil
	}
	var records []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &records); err != nil || len(records) != 1 {
		return output, nil
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(records[0]["Config"], &config); err != nil || config == nil {
		return output, nil
	}
	// A non-null projection is usable without an export. Never infer NONE from null.
	health := bytes.TrimSpace(config["Healthcheck"])
	if len(health) != 0 && !bytes.Equal(health, []byte("null")) {
		return output, nil
	}
	builder := currentGeneratedImageBuilder(ctx)
	session, _ := ctx.Value(generatedBuilderSessionKey{}).(*generatedBuilderSession)
	if session != nil {
		session.healthMu.Lock()
		defer session.healthMu.Unlock()
	}
	if builder == nil && session != nil {
		builder = session.healthEngine
	}
	if builder == nil {
		probeCtx, cancel := context.WithTimeout(ctx, defaultDockerPreflightTimeout)
		defer cancel()
		response, err := executeDockerOutputAtEndpoint(probeCtx, endpoint, "version", "--format", "{{json .}}")
		if err != nil {
			return "", fmt.Errorf("identify image healthcheck metadata source: %w", err)
		}
		builder, err = parseGeneratedImageBuilder(response)
		if err != nil {
			return "", fmt.Errorf("identify image healthcheck metadata source: %w", err)
		}
		builder.endpoint = endpoint
		if session != nil {
			session.healthEngine = builder
		}
	}
	if builder.endpoint != endpoint {
		return "", fmt.Errorf("image healthcheck endpoint differs from selected engine")
	}
	if builder.mode != generatedPodmanAPI {
		return output, nil
	}
	var imageID string
	if err := json.Unmarshal(records[0]["Id"], &imageID); err != nil {
		return "", fmt.Errorf("decode image healthcheck config ID: %w", err)
	}
	if err := canonical.Digest(imageID).Validate(); err != nil {
		return "", fmt.Errorf("image healthcheck config ID: %w", err)
	}
	cacheKey := endpoint + "\x00" + imageID
	var actual *dockerHealthcheck
	cached := false
	if session != nil {
		actual, cached = session.healthchecks[cacheKey]
	}
	if !cached {
		var err error
		actual, err = exportDockerImageHealthcheck(ctx, endpoint, imageID)
		if err != nil {
			return "", fmt.Errorf("read image %s healthcheck metadata: %w", imageID, err)
		}
		if session != nil && len(session.healthchecks) < imageHealthCacheLimit {
			if session.healthchecks == nil {
				session.healthchecks = map[string]*dockerHealthcheck{}
			}
			session.healthchecks[cacheKey] = actual
		}
	}
	encoded, err := json.Marshal(actual)
	if err != nil {
		return "", err
	}
	config["Healthcheck"] = encoded
	records[0]["Config"], err = json.Marshal(config)
	if err != nil {
		return "", err
	}
	result, err := json.Marshal(records)
	return string(result), err
}

func exportDockerImageHealthcheck(ctx context.Context, endpoint, imageID string) (*dockerHealthcheck, error) {
	ctx, cancel := context.WithTimeout(ctx, imageHealthExportTimeout)
	defer cancel()
	spec := pinDockerEndpointV1(CommandSpec{Name: "docker", Args: []string{"image", "save", imageID}, Env: []string{"DOCKER_BUILDKIT=1"}}, endpoint)
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Env = append(os.Environ(), spec.Env...)
	var stderr boundedImageHealthStderr
	cmd.Stderr = &stderr
	stream, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start image export: %w", err)
	}
	actual, parseErr := readDockerImageHealthcheckArchive(ctx, stream, imageID)
	if parseErr != nil {
		cancel()
	}
	// The parser consumes the complete stream before accepting any evidence.
	// On errors, cancellation kills the CLI and Wait reaps it and closes pipes.
	waitErr := cmd.Wait()
	if parseErr != nil {
		return nil, parseErr
	}
	if waitErr != nil {
		return nil, fmt.Errorf("image export failed: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return actual, nil
}

type boundedImageHealthStderr struct{ buffer bytes.Buffer }

func (b *boundedImageHealthStderr) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := imageHealthStderrLimit - b.buffer.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.buffer.Write(p)
	}
	return n, nil
}

func (b *boundedImageHealthStderr) Len() int { return b.buffer.Len() }

func (b *boundedImageHealthStderr) String() string { return b.buffer.String() }

func readDockerImageHealthcheckArchive(ctx context.Context, input io.Reader, imageID string) (*dockerHealthcheck, error) {
	if err := canonical.Digest(imageID).Validate(); err != nil {
		return nil, err
	}
	tracked := &imageHealthArchiveReader{ctx: ctx, input: input}
	limited := &io.LimitedReader{R: tracked, N: imageHealthArchiveLimit + 1}
	reader := tar.NewReader(limited)
	expectedName := strings.TrimPrefix(imageID, "sha256:") + ".json"
	names := map[string]bool{}
	nameBytes := 0
	var manifest, config []byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		before := tracked.count
		header, err := reader.Next()
		if err == io.EOF {
			if tracked.count-before < 1024 {
				return nil, fmt.Errorf("image config archive lacks complete terminator")
			}
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read image config archive: %w", err)
		}
		if len(names) >= imageHealthEntryLimit || limited.N <= 0 {
			return nil, fmt.Errorf("image config archive exceeds resource limits")
		}
		if isSparseImageHealthHeader(header) {
			return nil, fmt.Errorf("image config archive sparse member %q is unsupported", header.Name)
		}
		name := header.Name
		if len(name) > 4096 || name == "" || path.IsAbs(name) || path.Clean(name) != strings.TrimSuffix(name, "/") || name == ".." || strings.HasPrefix(name, "../") || names[name] {
			return nil, fmt.Errorf("image config archive has invalid or duplicate member %q", name)
		}
		nameBytes += len(name)
		if nameBytes > imageHealthNameLimit {
			return nil, fmt.Errorf("image config archive member names exceed limit")
		}
		names[name] = true
		var destination *[]byte
		var sizeLimit int64
		switch name {
		case "manifest.json":
			destination, sizeLimit = &manifest, imageHealthManifestLimit
		case expectedName:
			destination, sizeLimit = &config, imageHealthConfigLimit
		}
		if destination != nil {
			if header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > sizeLimit {
				return nil, fmt.Errorf("image config archive metadata %q is invalid or too large", name)
			}
			*destination, err = io.ReadAll(reader)
			if err != nil {
				return nil, fmt.Errorf("read image config metadata: %w", err)
			}
		} else {
			if _, err := io.Copy(io.Discard, reader); err != nil {
				return nil, fmt.Errorf("read image config archive member: %w", err)
			}
		}
	}
	// A tar terminator alone does not prove command success or absence of trailing
	// duplicate evidence. Require any remaining bytes to be zero archive padding.
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := limited.Read(buffer)
		for _, value := range buffer[:n] {
			if value != 0 {
				return nil, fmt.Errorf("image config archive contains data after its terminator")
			}
		}
		if limited.N <= 0 {
			return nil, fmt.Errorf("image config archive exceeds resource limits")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if tracked.count < 1024 || tracked.count%512 != 0 || !bytes.Equal(tracked.tail, make([]byte, 1024)) {
		return nil, fmt.Errorf("image config archive is truncated or lacks complete terminator")
	}
	if len(manifest) == 0 || len(config) == 0 {
		return nil, fmt.Errorf("image config archive lacks manifest or requested config")
	}
	if err := rejectDuplicateJSONKeys(manifest); err != nil {
		return nil, err
	}
	if err := validateImageHealthManifest(manifest, expectedName); err != nil {
		return nil, err
	}
	if digest := fmt.Sprintf("sha256:%x", sha256.Sum256(config)); digest != imageID {
		return nil, fmt.Errorf("image config archive digest %s does not match requested %s", digest, imageID)
	}
	if err := rejectDuplicateJSONKeys(config); err != nil {
		return nil, err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(config, &document); err != nil || document == nil {
		return nil, fmt.Errorf("image config archive config is not an object")
	}
	var actual map[string]json.RawMessage
	if err := json.Unmarshal(document["config"], &actual); err != nil || actual == nil {
		return nil, fmt.Errorf("image config archive lacks actual config object")
	}
	if err := rejectHealthFieldAliases(actual, []string{"Healthcheck"}); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if raw := actual["Healthcheck"]; len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("actual image healthcheck is not an object")
		}
		if err := rejectHealthFieldAliases(fields, []string{"Test", "Interval", "Timeout", "StartPeriod", "StartInterval", "Retries"}); err != nil {
			return nil, err
		}
	}
	var health *dockerHealthcheck
	if err := json.Unmarshal(valueOrNull(actual["Healthcheck"]), &health); err != nil {
		return nil, fmt.Errorf("decode actual image healthcheck: %w", err)
	}
	return health, nil
}

func validateImageHealthManifest(data []byte, expectedName string) error {
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil || len(entries) != 1 || entries[0] == nil {
		return fmt.Errorf("image config archive manifest does not uniquely identify requested config")
	}
	var configRaw json.RawMessage
	for key, raw := range entries[0] {
		if strings.EqualFold(key, "Config") && key != "Config" {
			return fmt.Errorf("image config archive manifest has noncanonical config selector %q", key)
		}
		if key == "Config" {
			configRaw = raw
		}
	}
	var configName string
	if len(configRaw) == 0 || json.Unmarshal(configRaw, &configName) != nil || configName != expectedName {
		return fmt.Errorf("image config archive manifest does not uniquely identify requested config")
	}
	return nil
}

func isSparseImageHealthHeader(header *tar.Header) bool {
	if header.Typeflag == tar.TypeGNUSparse {
		return true
	}
	for key := range header.PAXRecords {
		if strings.HasPrefix(key, "GNU.sparse.") {
			return true
		}
	}
	return false
}

func valueOrNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

// json.Unmarshal accepts duplicate keys. Reject them before consuming metadata.
func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("image config metadata nesting exceeds limit")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		keys := map[string]bool{}
		for decoder.More() {
			if delimiter == '{' {
				token, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := token.(string)
				if !ok || keys[key] {
					return fmt.Errorf("image config metadata has duplicate or invalid key %q", key)
				}
				keys[key] = true
			}
			if err := visit(depth + 1); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	}
	if err := visit(0); err != nil {
		return fmt.Errorf("invalid image config metadata: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("image config metadata has trailing JSON")
	}
	return nil
}

// Keep only the final two tar blocks, including bytes read while skipping layers.
type imageHealthArchiveReader struct {
	ctx   context.Context
	input io.Reader
	count int64
	tail  []byte
}

func (r *imageHealthArchiveReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.input.Read(p)
	r.count += int64(n)
	if n >= 1024 {
		r.tail = append(r.tail[:0], p[n-1024:n]...)
	} else if n > 0 {
		if len(r.tail)+n > 1024 {
			r.tail = r.tail[len(r.tail)+n-1024:]
		}
		r.tail = append(r.tail, p[:n]...)
	}
	return n, err
}

func rejectHealthFieldAliases(fields map[string]json.RawMessage, canonicalNames []string) error {
	for key := range fields {
		for _, name := range canonicalNames {
			if strings.EqualFold(key, name) && key != name {
				return fmt.Errorf("image config metadata has noncanonical healthcheck field %q", key)
			}
		}
	}
	return nil
}
