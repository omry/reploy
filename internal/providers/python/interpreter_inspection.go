package python

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/portabletool"
)

const (
	InterpreterFactsSchemaV2       = "python-interpreter-facts-v2"
	interpreterInspectionProgramV2 = `import json,platform,sys
from pip._vendor.packaging import tags
tested=json.loads(sys.argv[1])
target=sys.argv[2]
if target not in ("x86_64","aarch64","armv7l"): raise RuntimeError("unsupported target architecture")
machine=platform.machine().lower().replace("amd64","x86_64").replace("arm64","aarch64")
if machine != target: raise RuntimeError("interpreter architecture does not match selected target")
all_tags=list(tags.sys_tags())
if not all_tags: raise RuntimeError("pip returned no interpreter tags")
tested_set=set(tested)
compatible=sorted({str(item) for item in all_tags if str(item) in tested_set})
implementation=sys.implementation.name.lower()
interpreter=tags.interpreter_name()+tags.interpreter_version()
abi=next((item.abi for item in all_tags if item.interpreter == interpreter), "none")
libc,release=platform.libc_ver()
libc=libc.lower() or "unknown"
parts=release.split(".") if release else []
major=parts[0] if parts and parts[0].isdigit() else "0"
minor=parts[1] if len(parts)>1 and parts[1].isdigit() else "0"
version=".".join(map(str,sys.version_info[:3]))
print(json.dumps({"version":version,"implementation":implementation,"abi":abi,"libc":libc,"libc_major":major,"libc_minor":minor,"tested_tags":sorted(tested),"compatible_tags":compatible},separators=(",",":")))`
)

// IsolatedInterpreterCommandPrefixV2 is the one command prefix shared by
// interpreter inspection and ordinary pip resolution.
func IsolatedInterpreterCommandPrefixV2(executable string) ([]string, error) {
	if executable == "" || !path.IsAbs(executable) || path.Clean(executable) != executable || strings.Contains(executable, `\`) {
		return nil, fmt.Errorf("Python interpreter executable %q must be a normalized absolute Linux path", executable)
	}
	return []string{executable, "-I"}, nil
}

// InterpreterInspectionArgv returns the complete fixed V2 invocation used by
// a Python consumer to inspect the selected absolute interpreter. The tested
// tags and target architecture are validated argv data, never Python source.
func InterpreterInspectionArgv(executable string, testedTags []string, targetArchitecture string) ([]string, error) {
	prefix, err := IsolatedInterpreterCommandPrefixV2(executable)
	if err != nil {
		return nil, err
	}
	if err := validateInspectionTagsV2(testedTags); err != nil {
		return nil, fmt.Errorf("Python inspection tested tags: %w", err)
	}
	if targetArchitecture != "x86_64" && targetArchitecture != "aarch64" && targetArchitecture != "armv7l" {
		return nil, fmt.Errorf("Python inspection target architecture %q is unsupported", targetArchitecture)
	}
	tagJSON, err := json.Marshal(testedTags)
	if err != nil {
		return nil, fmt.Errorf("encode Python inspection tested tags: %w", err)
	}
	return append(prefix, "-c", interpreterInspectionProgramV2, string(tagJSON), targetArchitecture), nil
}

// InterpreterInspectionFactsV2 is the strict wire representation emitted by
// the fixed probe. Decimal release components are strings because canonical
// provider data does not contain numeric values.
type InterpreterInspectionFactsV2 struct {
	Version        string   `json:"version"`
	Implementation string   `json:"implementation"`
	ABI            string   `json:"abi"`
	Libc           string   `json:"libc"`
	LibcMajor      string   `json:"libc_major"`
	LibcMinor      string   `json:"libc_minor"`
	TestedTags     []string `json:"tested_tags"`
	CompatibleTags []string `json:"compatible_tags"`
}

// ParseInterpreterInspectionOutput strictly parses one complete probe JSON
// document and requires its tested set to equal the validated request.
func ParseInterpreterInspectionOutput(output []byte, expectedTestedTags []string) (InterpreterInspectionFactsV2, error) {
	if err := validateInspectionTagsV2(expectedTestedTags); err != nil {
		return InterpreterInspectionFactsV2{}, fmt.Errorf("expected tested tags: %w", err)
	}
	value, err := decodeInspectionObjectV2(output)
	if err != nil {
		return InterpreterInspectionFactsV2{}, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return InterpreterInspectionFactsV2{}, err
	}
	var facts InterpreterInspectionFactsV2
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&facts); err != nil {
		return InterpreterInspectionFactsV2{}, fmt.Errorf("decode Python interpreter inspection: %w", err)
	}
	if err := validateInspectionFactsV2(facts, expectedTestedTags); err != nil {
		return InterpreterInspectionFactsV2{}, err
	}
	return facts, nil
}

func decodeInspectionObjectV2(output []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(output))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode Python interpreter inspection: %w", err)
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("Python interpreter inspection must be a JSON object")
	}
	value := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode Python interpreter inspection key: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("Python interpreter inspection object key is not a string")
		}
		if _, exists := value[key]; exists {
			return nil, fmt.Errorf("Python interpreter inspection contains duplicate field %q", key)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decode Python interpreter inspection field %q: %w", key, err)
		}
		value[key] = raw
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, fmt.Errorf("Python interpreter inspection has an unterminated object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("Python interpreter inspection contains trailing JSON")
		}
		return nil, fmt.Errorf("Python interpreter inspection contains trailing data: %w", err)
	}
	return value, nil
}

func validateInspectionFactsV2(facts InterpreterInspectionFactsV2, expected []string) error {
	if facts.Version == "" || ValidateInterpreterVersionV1(facts.Version) != nil || len(strings.Split(facts.Version, ".")) != 3 {
		return fmt.Errorf("Python interpreter inspection version %q is not canonical", facts.Version)
	}
	if !canonicalInspectionComponentV2(facts.Implementation, false) || !canonicalInspectionComponentV2(facts.ABI, true) {
		return fmt.Errorf("Python interpreter inspection implementation or ABI is not canonical")
	}
	if !canonicalInspectionComponentV2(facts.Libc, false) {
		return fmt.Errorf("Python interpreter inspection libc is not canonical")
	}
	if err := validateInspectionDecimalV2(facts.LibcMajor); err != nil {
		return fmt.Errorf("Python interpreter inspection libc major: %w", err)
	}
	if err := validateInspectionDecimalV2(facts.LibcMinor); err != nil {
		return fmt.Errorf("Python interpreter inspection libc minor: %w", err)
	}
	if err := validateInspectionTagsV2(facts.TestedTags); err != nil {
		return fmt.Errorf("Python interpreter inspection tested tags: %w", err)
	}
	if err := validateInspectionTagsV2(facts.CompatibleTags); err != nil {
		return fmt.Errorf("Python interpreter inspection compatible tags: %w", err)
	}
	if !equalInspectionStringsV2(facts.TestedTags, expected) {
		return fmt.Errorf("Python interpreter inspection tested tags do not equal requested tags")
	}
	tested := make(map[string]struct{}, len(facts.TestedTags))
	for _, tag := range facts.TestedTags {
		tested[tag] = struct{}{}
	}
	for _, tag := range facts.CompatibleTags {
		if _, ok := tested[tag]; !ok {
			return fmt.Errorf("Python interpreter inspection compatible tag %q is outside tested tags", tag)
		}
	}
	return nil
}

func validateInspectionTagsV2(tags []string) error {
	if tags == nil {
		return fmt.Errorf("tags must use a canonical array")
	}
	if len(tags) > portabletool.RecordArrayMaxEntriesV1 {
		return fmt.Errorf("tags exceed the bounded record limit of %d", portabletool.RecordArrayMaxEntriesV1)
	}
	for index, tag := range tags {
		parts := strings.Split(tag, "-")
		if len(parts) != 3 || !inspectionTagComponentV2(parts[0]) || !inspectionTagComponentV2(parts[1]) || !inspectionTagComponentV2(parts[2]) {
			return fmt.Errorf("tag %q is not a canonical three-part tag", tag)
		}
		if index > 0 && tags[index-1] >= tag {
			return fmt.Errorf("tags must be unique and sorted")
		}
	}
	return nil
}

func inspectionTagComponentV2(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character != '_' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func canonicalInspectionComponentV2(value string, allowNone bool) bool {
	return allowNone && value == "none" || inspectionTagComponentV2(value)
}

func validateInspectionDecimalV2(value string) error {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return fmt.Errorf("must be a canonical nonnegative decimal")
	}
	parsed, err := strconv.ParseUint(value, 10, 31)
	if err != nil || parsed > 1<<31-1 {
		return fmt.Errorf("must be a bounded nonnegative decimal")
	}
	return nil
}

func equalInspectionStringsV2(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
