package probe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/canonical"
)

const (
	RequestSchemaV1  = "reploy-probe-request-v1"
	ResponseSchemaV1 = "reploy-probe-response-v1"
)

type RequestV1 struct {
	Schema      string                   `json:"schema"`
	Inspections []ExecutableInspectionV1 `json:"inspections"`
}

type ExecutableInspectionV1 struct {
	ID             string `json:"id"`
	InvocationPath string `json:"invocation_path"`
}

type ResponseV1 struct {
	Schema       string                    `json:"schema"`
	Observations []ExecutableObservationV1 `json:"observations"`
}

type ExecutableObservationV1 struct {
	ID             string                `json:"id"`
	InvocationPath string                `json:"invocation_path"`
	Links          []LinkObservationV1   `json:"links"`
	Terminal       FileObservationV1     `json:"terminal"`
	Access         []AccessObservationV1 `json:"access"`
}

type LinkObservationV1 struct {
	Path         string `json:"path"`
	Target       string `json:"target"`
	ResolvedPath string `json:"resolved_path"`
	Mode         string `json:"mode"`
	UID          string `json:"uid"`
	GID          string `json:"gid"`
}

type FileObservationV1 struct {
	Path   string           `json:"path"`
	Kind   string           `json:"kind"`
	Mode   string           `json:"mode"`
	Size   string           `json:"size"`
	SHA256 canonical.Digest `json:"sha256"`
	UID    string           `json:"uid"`
	GID    string           `json:"gid"`
}

type AccessObservationV1 struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Mode string `json:"mode"`
	UID  string `json:"uid"`
	GID  string `json:"gid"`
}

func DecodeRequestV1(content []byte) (RequestV1, error) {
	var request RequestV1
	if err := decodeCanonicalJSON("probe request", content, &request); err != nil {
		return RequestV1{}, err
	}
	if err := ValidateRequestV1(request); err != nil {
		return RequestV1{}, err
	}
	return request, nil
}

// DecodeResponseV1 accepts only the closed canonical response schema and binds
// every observation to the request that caused it.
func DecodeResponseV1(request RequestV1, content []byte) (ResponseV1, error) {
	var response ResponseV1
	if err := decodeCanonicalJSON("probe response", content, &response); err != nil {
		return ResponseV1{}, err
	}
	if err := ValidateResponseV1(request, response); err != nil {
		return ResponseV1{}, err
	}
	return response, nil
}

func ValidateRequestV1(request RequestV1) error {
	if request.Schema != RequestSchemaV1 {
		return fmt.Errorf("probe request schema must be %q", RequestSchemaV1)
	}
	if request.Inspections == nil {
		return fmt.Errorf("probe request inspections must use an array")
	}
	for index, inspection := range request.Inspections {
		if !validID(inspection.ID) {
			return fmt.Errorf("probe inspection ID %q is invalid", inspection.ID)
		}
		if index > 0 && request.Inspections[index-1].ID >= inspection.ID {
			return fmt.Errorf("probe inspections must be unique and sorted by ID")
		}
		if err := validateAbsolutePath("probe invocation path", inspection.InvocationPath); err != nil {
			return err
		}
	}
	return nil
}

func ValidateResponseV1(request RequestV1, response ResponseV1) error {
	if err := ValidateRequestV1(request); err != nil {
		return err
	}
	if response.Schema != ResponseSchemaV1 {
		return fmt.Errorf("probe response schema must be %q", ResponseSchemaV1)
	}
	if response.Observations == nil || len(response.Observations) != len(request.Inspections) {
		return fmt.Errorf("probe response observations do not match the request")
	}
	for index, observation := range response.Observations {
		expected := request.Inspections[index]
		if observation.ID != expected.ID || observation.InvocationPath != expected.InvocationPath {
			return fmt.Errorf("probe observation %d does not match inspection %q", index, expected.ID)
		}
		if observation.Links == nil || observation.Access == nil {
			return fmt.Errorf("probe observation %q collections must use arrays", observation.ID)
		}
		if err := validateAbsolutePath("probe terminal path", observation.Terminal.Path); err != nil {
			return err
		}
		if observation.Terminal.Kind != "regular" || !validMode(observation.Terminal.Mode) || !canonicalUnsigned(observation.Terminal.Size) {
			return fmt.Errorf("probe observation %q has invalid terminal metadata", observation.ID)
		}
		if err := observation.Terminal.SHA256.Validate(); err != nil {
			return fmt.Errorf("probe observation %q terminal digest: %w", observation.ID, err)
		}
		if !canonicalUnsigned(observation.Terminal.UID) || !canonicalUnsigned(observation.Terminal.GID) {
			return fmt.Errorf("probe observation %q has invalid terminal ownership", observation.ID)
		}
		seenLinks := map[string]bool{}
		for linkIndex, link := range observation.Links {
			if err := validateAbsolutePath("probe link path", link.Path); err != nil {
				return err
			}
			if link.Target == "" || !utf8.ValidString(link.Target) {
				return fmt.Errorf("probe observation %q link %d has invalid target", observation.ID, linkIndex)
			}
			if err := validateAbsolutePath("probe resolved link path", link.ResolvedPath); err != nil {
				return err
			}
			resolvedTarget := link.Target
			if !path.IsAbs(resolvedTarget) {
				resolvedTarget = path.Join(path.Dir(link.Path), resolvedTarget)
			}
			if path.Clean(resolvedTarget) != link.ResolvedPath {
				return fmt.Errorf("probe observation %q link %s target does not match its resolved path", observation.ID, link.Path)
			}
			if !validMode(link.Mode) || !canonicalUnsigned(link.UID) || !canonicalUnsigned(link.GID) {
				return fmt.Errorf("probe observation %q link %d has invalid metadata", observation.ID, linkIndex)
			}
			if seenLinks[link.Path] {
				return fmt.Errorf("probe observation %q repeats link path %s", observation.ID, link.Path)
			}
			seenLinks[link.Path] = true
			expectedResolved := observation.Terminal.Path
			if linkIndex+1 < len(observation.Links) {
				expectedResolved = observation.Links[linkIndex+1].Path
			}
			if link.ResolvedPath != expectedResolved {
				return fmt.Errorf("probe observation %q link %s does not continue its chain", observation.ID, link.Path)
			}
		}
		expectedInvocation := observation.Terminal.Path
		if len(observation.Links) != 0 {
			expectedInvocation = observation.Links[0].Path
		}
		if observation.InvocationPath != expectedInvocation {
			return fmt.Errorf("probe observation %q does not begin at its invocation path", observation.ID)
		}
		expectedAccess := requiredAccessPaths(observation.InvocationPath, observation.Links, observation.Terminal.Path)
		if len(observation.Access) != len(expectedAccess) {
			return fmt.Errorf("probe observation %q access paths do not cover its link chain and terminal", observation.ID)
		}
		for accessIndex, access := range observation.Access {
			if accessIndex > 0 && observation.Access[accessIndex-1].Path >= access.Path {
				return fmt.Errorf("probe observation %q access paths must be unique and sorted", observation.ID)
			}
			if err := validateAbsolutePath("probe access path", access.Path); err != nil {
				return err
			}
			expectedKind, exists := expectedAccess[access.Path]
			if !exists || access.Kind != expectedKind {
				return fmt.Errorf("probe observation %q access path %s is unexpected", observation.ID, access.Path)
			}
			if (access.Kind != "directory" && access.Kind != "regular") || !validMode(access.Mode) || !canonicalUnsigned(access.UID) || !canonicalUnsigned(access.GID) {
				return fmt.Errorf("probe observation %q access path %d has invalid metadata", observation.ID, accessIndex)
			}
		}
	}
	return nil
}

func decodeCanonicalJSON(subject string, content []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode %s: %w", subject, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%s contains trailing JSON", subject)
		}
		return fmt.Errorf("decode %s trailer: %w", subject, err)
	}
	canonicalContent, err := canonical.Marshal(value)
	if err != nil {
		return err
	}
	if !bytes.Equal(content, canonicalContent) {
		return fmt.Errorf("%s is not canonical JSON", subject)
	}
	return nil
}

func requiredAccessPaths(invocationPath string, links []LinkObservationV1, terminalPath string) map[string]string {
	result := map[string]string{}
	addParentDirectories(result, invocationPath)
	for _, link := range links {
		addParentDirectories(result, link.Path)
		addParentDirectories(result, link.ResolvedPath)
	}
	addParentDirectories(result, terminalPath)
	result[terminalPath] = "regular"
	return result
}

func addParentDirectories(result map[string]string, filePath string) {
	result["/"] = "directory"
	for parent := path.Dir(filePath); parent != "/"; parent = path.Dir(parent) {
		result[parent] = "directory"
	}
}

func validateAbsolutePath(field string, value string) error {
	if value == "" || !path.IsAbs(value) || path.Clean(value) != value || strings.Contains(value, `\`) || !utf8.ValidString(value) {
		return fmt.Errorf("%s %q must be a normalized absolute Linux path", field, value)
	}
	return nil
}

func validID(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func validMode(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '7' {
			return false
		}
	}
	return true
}

func canonicalUnsigned(value string) bool {
	if value == "0" {
		return true
	}
	if value == "" || value[0] < '1' || value[0] > '9' {
		return false
	}
	for _, char := range value[1:] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
