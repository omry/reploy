package toolcatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/canonical"
	"gopkg.in/yaml.v3"
)

var (
	authoringAliasPatternV1 = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	jsonNumberPatternV1     = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
	windowsVolumePatternV1  = regexp.MustCompile(`^[A-Za-z]:`)
)

// PortableToolAuthoringEntryV1 maps one authoring source to its exact
// catalog-relative JSON output path. SourcePath is relative to the trusted
// authoring boundary.
type PortableToolAuthoringEntryV1 struct {
	SourcePath string
	OutputPath string
}

// PortableToolAuthoringSourceV1 records the exact bytes consumed for one
// reachable source. Path is normalized relative to the trusted boundary.
type PortableToolAuthoringSourceV1 struct {
	Path   string
	SHA256 canonical.Digest
}

// PortableToolAuthoringResultV1 contains canonical composer output and the
// sorted transitive source manifest that produced it.
type PortableToolAuthoringResultV1 struct {
	Records []CanonicalCatalogRecordV1
	Sources []PortableToolAuthoringSourceV1
}

type portableToolAuthoringEnvelopeV1 struct {
	kind       string
	root       string
	extends    string
	importPath string
	fields     map[string]any
}

type portableToolAuthoringLoadedSourceV1 struct {
	envelope portableToolAuthoringEnvelopeV1
	digest   canonical.Digest
}

type portableToolAuthoringResolvedV1 struct {
	kind   string
	fields map[string]any
	owners map[string]string
	chain  []string
}

type portableToolAuthoringLoaderV1 struct {
	root    *os.Root
	sources map[string]portableToolAuthoringLoadedSourceV1
}

type normalizedPortableToolAuthoringEntryV1 struct {
	source string
	output string
}

type portableToolAuthoringDefinitionSourceV1 struct {
	output string
	id     string
	chain  []string
}

// LoadPortableToolAuthoringV1 resolves strict localized authoring sources
// beneath boundary and delegates canonical record production to the PTD-11
// composer. It performs no runtime inheritance or overlay processing.
func LoadPortableToolAuthoringV1(boundary string, entries []PortableToolAuthoringEntryV1) (PortableToolAuthoringResultV1, error) {
	loader, err := newPortableToolAuthoringLoaderV1(boundary)
	if err != nil {
		return PortableToolAuthoringResultV1{}, err
	}
	defer loader.root.Close()

	normalized, err := normalizePortableToolAuthoringEntriesV1(entries)
	if err != nil {
		return PortableToolAuthoringResultV1{}, err
	}
	definitions := make([]PortableToolDefinitionRecordV1, 0, len(normalized))
	definitionSources := make([]portableToolAuthoringDefinitionSourceV1, 0, len(normalized))
	for _, entry := range normalized {
		resolved, resolveErr := loader.resolve(entry.source, "", nil, 0)
		if resolveErr != nil {
			return PortableToolAuthoringResultV1{}, resolveErr
		}
		value := cloneAuthoringObjectV1(resolved.fields)
		value["schema"] = resolved.kind
		payload, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return PortableToolAuthoringResultV1{}, authoringChainErrorV1(resolved.chain, "encode resolved record: %v", marshalErr)
		}
		typed, schema, decodeErr := decodePortableToolAuthoringRecordV1(entry.output, payload)
		if decodeErr != nil {
			return PortableToolAuthoringResultV1{}, authoringChainErrorV1(resolved.chain, "resolved entry for %q is incomplete or invalid: %v", entry.output, decodeErr)
		}
		if schema != resolved.kind {
			return PortableToolAuthoringResultV1{}, authoringChainErrorV1(resolved.chain, "resolved entry for %q changed schema from %q to %q", entry.output, resolved.kind, schema)
		}
		if err := validatePortableToolAuthoringRecordV1(entry.output, typed); err != nil {
			return PortableToolAuthoringResultV1{}, authoringChainErrorV1(resolved.chain, "resolved entry for %q is invalid: %v", entry.output, err)
		}
		id, _, _, cloneErr := clonePortableToolRecordV1(typed)
		if cloneErr != nil {
			return PortableToolAuthoringResultV1{}, authoringChainErrorV1(resolved.chain, "inspect resolved entry for %q: %v", entry.output, cloneErr)
		}
		definitions = append(definitions, PortableToolDefinitionRecordV1{Path: entry.output, Record: typed})
		definitionSources = append(definitionSources, portableToolAuthoringDefinitionSourceV1{
			output: entry.output,
			id:     id,
			chain:  append([]string(nil), resolved.chain...),
		})
	}

	records, err := ComposePortableToolCatalogV1(definitions)
	if err != nil {
		return PortableToolAuthoringResultV1{}, authoringCompositionErrorV1(definitionSources, err)
	}
	manifest := make([]PortableToolAuthoringSourceV1, 0, len(loader.sources))
	for filename, source := range loader.sources {
		manifest = append(manifest, PortableToolAuthoringSourceV1{Path: filename, SHA256: source.digest})
	}
	sort.Slice(manifest, func(left, right int) bool { return manifest[left].Path < manifest[right].Path })
	return PortableToolAuthoringResultV1{Records: records, Sources: manifest}, nil
}

func validatePortableToolAuthoringRecordV1(filename string, record PortableToolRecordV1) error {
	id, schema, value, err := clonePortableToolRecordV1(record)
	if err != nil {
		return err
	}
	placeholder := canonical.Digest("sha256:" + strings.Repeat("0", 64))
	for _, edge := range mutableCatalogReferencesV1(value) {
		if edge.reference.Digest == "" {
			edge.reference.Digest = placeholder
		}
	}
	return validateLoadedRecordV1(loadedRecordV1{ID: id, Schema: schema, Path: filename, Value: value})
}

func decodePortableToolAuthoringRecordV1(filename string, payload []byte) (PortableToolRecordV1, string, error) {
	if err := validateStrictJSONV1(payload); err != nil {
		return nil, "", fmt.Errorf("decode %s: %w", filename, err)
	}
	var header recordHeaderV1
	if err := json.Unmarshal(payload, &header); err != nil {
		return nil, "", fmt.Errorf("decode %s header: %w", filename, err)
	}
	if header.Schema == "" {
		return nil, "", fmt.Errorf("decode %s: record schema is required", filename)
	}
	if header.ID == "" {
		return nil, "", fmt.Errorf("decode %s: record ID is required", filename)
	}
	value, supported := newPortableToolRecordValueV1(header.Schema)
	if !supported {
		return nil, "", fmt.Errorf("decode %s: unsupported schema %q", filename, header.Schema)
	}
	if err := decodeExactJSONV1(payload, value); err != nil {
		return nil, "", fmt.Errorf("decode %s: %w", filename, err)
	}
	return value, header.Schema, nil
}

func newPortableToolAuthoringLoaderV1(boundary string) (*portableToolAuthoringLoaderV1, error) {
	if boundary == "" {
		return nil, fmt.Errorf("portable tool authoring boundary is required")
	}
	absolute, err := filepath.Abs(boundary)
	if err != nil {
		return nil, fmt.Errorf("resolve portable tool authoring boundary: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("inspect portable tool authoring boundary %q: %w", absolute, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("portable tool authoring boundary %q must be a non-symlink directory", absolute)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, fmt.Errorf("open portable tool authoring boundary %q: %w", absolute, err)
	}
	return &portableToolAuthoringLoaderV1{
		root:    root,
		sources: map[string]portableToolAuthoringLoadedSourceV1{},
	}, nil
}

func normalizePortableToolAuthoringEntriesV1(entries []PortableToolAuthoringEntryV1) ([]normalizedPortableToolAuthoringEntryV1, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("portable tool authoring must contain entries")
	}
	result := make([]normalizedPortableToolAuthoringEntryV1, 0, len(entries))
	seenSources := map[string]struct{}{}
	seenOutputs := map[string]struct{}{}
	for index, entry := range entries {
		source, err := normalizeAuthoringSourcePathV1(entry.SourcePath)
		if err != nil {
			return nil, fmt.Errorf("authoring entry %d source: %w", index, err)
		}
		if _, exists := seenSources[source]; exists {
			return nil, fmt.Errorf("authoring entries contain duplicate normalized source %q", source)
		}
		seenSources[source] = struct{}{}
		if err := validateRecordPathV1(entry.OutputPath, false); err != nil {
			return nil, fmt.Errorf("authoring entry %d output path: %w", index, err)
		}
		if path.Ext(entry.OutputPath) != ".json" {
			return nil, fmt.Errorf("authoring entry %d output path %q must be a JSON file", index, entry.OutputPath)
		}
		if _, exists := seenOutputs[entry.OutputPath]; exists {
			return nil, fmt.Errorf("authoring entries contain duplicate output path %q", entry.OutputPath)
		}
		seenOutputs[entry.OutputPath] = struct{}{}
		result = append(result, normalizedPortableToolAuthoringEntryV1{source: source, output: entry.OutputPath})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].output == result[right].output {
			return result[left].source < result[right].source
		}
		return result[left].output < result[right].output
	})
	return result, nil
}

func (loader *portableToolAuthoringLoaderV1) resolve(filename string, inheritedRoot string, ancestors []string, depth int) (portableToolAuthoringResolvedV1, error) {
	chain := append(append([]string(nil), ancestors...), filename)
	if depth > maxCatalogGraphDepthV1 {
		return portableToolAuthoringResolvedV1{}, authoringChainErrorV1(chain, "extension chain exceeds depth %d", maxCatalogGraphDepthV1)
	}
	for _, ancestor := range ancestors {
		if ancestor == filename {
			return portableToolAuthoringResolvedV1{}, authoringChainErrorV1(chain, "extension cycle repeats %q", filename)
		}
	}
	source, err := loader.loadSource(filename, chain)
	if err != nil {
		return portableToolAuthoringResolvedV1{}, err
	}

	logicalRoot := inheritedRoot
	if source.envelope.root != "" {
		if depth != 0 {
			return portableToolAuthoringResolvedV1{}, authoringChainErrorV1(chain, "imported source %q cannot redefine imports.root", filename)
		}
		logicalRoot, err = resolveAuthoringRootV1(filename, source.envelope.root)
		if err != nil {
			return portableToolAuthoringResolvedV1{}, authoringChainErrorV1(chain, "%v", err)
		}
		if !authoringPathWithinV1(logicalRoot, filename) {
			return portableToolAuthoringResolvedV1{}, authoringChainErrorV1(chain, "entry source %q escapes logical root %q", filename, logicalRoot)
		}
		if err := loader.validateDirectory(logicalRoot); err != nil {
			return portableToolAuthoringResolvedV1{}, authoringChainErrorV1(chain, "logical root %q: %v", logicalRoot, err)
		}
	}

	resolved := portableToolAuthoringResolvedV1{
		kind:   source.envelope.kind,
		fields: cloneAuthoringObjectV1(source.envelope.fields),
		owners: make(map[string]string, len(source.envelope.fields)),
		chain:  chain,
	}
	for name := range source.envelope.fields {
		resolved.owners[name] = filename
	}
	if source.envelope.extends == "" {
		return resolved, nil
	}
	parentPath, err := resolveAuthoringImportV1(filename, source.envelope.importPath, logicalRoot)
	if err != nil {
		return portableToolAuthoringResolvedV1{}, authoringChainErrorV1(chain, "import %q: %v", source.envelope.extends, err)
	}
	parent, err := loader.resolve(parentPath, logicalRoot, chain, depth+1)
	if err != nil {
		return portableToolAuthoringResolvedV1{}, err
	}
	if parent.kind != resolved.kind {
		return portableToolAuthoringResolvedV1{}, authoringChainErrorV1(parent.chain, "kind mismatch: child %q declares %q but parent %q declares %q", filename, resolved.kind, parentPath, parent.kind)
	}
	merged := cloneAuthoringObjectV1(parent.fields)
	owners := make(map[string]string, len(parent.owners)+len(resolved.owners))
	for name, owner := range parent.owners {
		owners[name] = owner
	}
	fieldNames := make([]string, 0, len(resolved.fields))
	for name := range resolved.fields {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)
	for _, name := range fieldNames {
		value := resolved.fields[name]
		if parentOwner, exists := owners[name]; exists {
			return portableToolAuthoringResolvedV1{}, authoringChainErrorV1(parent.chain, "field %q is owned by both parent source %q and child source %q", name, parentOwner, filename)
		}
		merged[name] = cloneAuthoringValueV1(value)
		owners[name] = filename
	}
	resolved.fields = merged
	resolved.owners = owners
	resolved.chain = parent.chain
	return resolved, nil
}

func (loader *portableToolAuthoringLoaderV1) loadSource(filename string, chain []string) (portableToolAuthoringLoadedSourceV1, error) {
	if source, exists := loader.sources[filename]; exists {
		return source, nil
	}
	if err := loader.validateRegularFile(filename); err != nil {
		return portableToolAuthoringLoadedSourceV1{}, authoringChainErrorV1(chain, "source %q: %v", filename, err)
	}
	file, err := loader.root.Open(filepath.FromSlash(filename))
	if err != nil {
		return portableToolAuthoringLoadedSourceV1{}, authoringChainErrorV1(chain, "open source %q: %v", filename, err)
	}
	openedInfo, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return portableToolAuthoringLoadedSourceV1{}, authoringChainErrorV1(chain, "inspect opened source %q: %v", filename, statErr)
	}
	if err := loader.validateOpenedRegularFileV1(filename, openedInfo); err != nil {
		_ = file.Close()
		return portableToolAuthoringLoadedSourceV1{}, authoringChainErrorV1(chain, "source %q: %v", filename, err)
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, maxDefinitionFileBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return portableToolAuthoringLoadedSourceV1{}, authoringChainErrorV1(chain, "read source %q: %v", filename, readErr)
	}
	if closeErr != nil {
		return portableToolAuthoringLoadedSourceV1{}, authoringChainErrorV1(chain, "close source %q: %v", filename, closeErr)
	}
	if len(payload) == 0 || len(payload) > maxDefinitionFileBytes {
		return portableToolAuthoringLoadedSourceV1{}, authoringChainErrorV1(chain, "source %q size must be between 1 and %d bytes", filename, maxDefinitionFileBytes)
	}
	if !utf8.Valid(payload) {
		return portableToolAuthoringLoadedSourceV1{}, authoringChainErrorV1(chain, "source %q must be valid UTF-8", filename)
	}
	envelope, err := decodePortableToolAuthoringEnvelopeV1(payload)
	if err != nil {
		return portableToolAuthoringLoadedSourceV1{}, authoringChainErrorV1(chain, "decode source %q: %v", filename, err)
	}
	digest := sha256.Sum256(payload)
	source := portableToolAuthoringLoadedSourceV1{
		envelope: envelope,
		digest:   canonical.Digest(fmt.Sprintf("sha256:%x", digest)),
	}
	loader.sources[filename] = source
	return source, nil
}

func (loader *portableToolAuthoringLoaderV1) validateDirectory(filename string) error {
	if filename == "." {
		return nil
	}
	return loader.validatePathComponents(filename, true)
}

func (loader *portableToolAuthoringLoaderV1) validateRegularFile(filename string) error {
	return loader.validatePathComponents(filename, false)
}

func (loader *portableToolAuthoringLoaderV1) validateOpenedRegularFileV1(filename string, openedInfo os.FileInfo) error {
	if !openedInfo.Mode().IsRegular() {
		return fmt.Errorf("opened source %q must be a regular file", filename)
	}
	if err := loader.validateRegularFile(filename); err != nil {
		return err
	}
	currentInfo, err := loader.root.Lstat(filepath.FromSlash(filename))
	if err != nil {
		return err
	}
	if !os.SameFile(openedInfo, currentInfo) {
		return fmt.Errorf("source %q changed while opening", filename)
	}
	return nil
}

func (loader *portableToolAuthoringLoaderV1) validatePathComponents(filename string, wantDirectory bool) error {
	segments := strings.Split(filename, "/")
	for index := range segments {
		candidate := strings.Join(segments[:index+1], "/")
		info, err := loader.root.Lstat(filepath.FromSlash(candidate))
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component %q must not be a symbolic link", candidate)
		}
		last := index == len(segments)-1
		if !last || wantDirectory {
			if !info.IsDir() {
				return fmt.Errorf("path component %q must be a directory", candidate)
			}
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("source %q must be a regular file", candidate)
		}
	}
	return nil
}

func decodePortableToolAuthoringEnvelopeV1(payload []byte) (portableToolAuthoringEnvelopeV1, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(payload))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return portableToolAuthoringEnvelopeV1{}, err
	}
	if len(document.Content) != 1 {
		return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("source must contain one nonempty YAML document")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("multiple YAML documents are not allowed")
		}
		return portableToolAuthoringEnvelopeV1{}, err
	}
	members := 0
	value, err := authoringYAMLValueV1(document.Content[0], 0, &members)
	if err != nil {
		return portableToolAuthoringEnvelopeV1{}, err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("authoring document must be a mapping")
	}
	for name := range root {
		switch name {
		case "kind", "imports", "extends", "fields":
		default:
			return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("unknown authoring field %q", name)
		}
	}
	kind, err := requiredAuthoringStringV1(root, "kind")
	if err != nil {
		return portableToolAuthoringEnvelopeV1{}, err
	}
	fieldsValue, exists := root["fields"]
	if !exists {
		return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("required authoring field %q is missing", "fields")
	}
	fields, ok := fieldsValue.(map[string]any)
	if !ok {
		return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("authoring fields must be a mapping")
	}
	if _, exists := fields["schema"]; exists {
		return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("authoring fields must not contain reserved field %q", "schema")
	}

	envelope := portableToolAuthoringEnvelopeV1{kind: kind, fields: fields}
	extendsValue, hasExtends := root["extends"]
	importsValue, hasImports := root["imports"]
	if !hasExtends && hasImports {
		return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("imports require extends")
	}
	if !hasExtends {
		return envelope, nil
	}
	extends, ok := extendsValue.(string)
	if !ok || !authoringAliasPatternV1.MatchString(extends) || extends == "root" {
		return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("extends must name one canonical import alias")
	}
	imports, ok := importsValue.(map[string]any)
	if !ok {
		return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("extends requires an imports mapping")
	}
	for name := range imports {
		if name != "root" && name != extends {
			return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("unused or extra import alias %q", name)
		}
	}
	if rootValue, exists := imports["root"]; exists {
		rootPath, ok := rootValue.(string)
		if !ok || rootPath == "" || strings.HasPrefix(rootPath, "/") {
			return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("imports.root must be one nonempty file-relative directory path")
		}
		envelope.root = rootPath
	}
	importValue, exists := imports[extends]
	if !exists {
		return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("extends alias %q has no matching import", extends)
	}
	importDescriptor, ok := importValue.(map[string]any)
	if !ok || len(importDescriptor) != 1 {
		return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("import alias %q must contain exactly path", extends)
	}
	importPathValue, exists := importDescriptor["path"]
	importPath, ok := importPathValue.(string)
	if !exists || !ok || importPath == "" {
		return portableToolAuthoringEnvelopeV1{}, fmt.Errorf("import alias %q path must be a nonempty string", extends)
	}
	envelope.extends = extends
	envelope.importPath = importPath
	return envelope, nil
}

func authoringYAMLValueV1(node *yaml.Node, depth int, members *int) (any, error) {
	if node.Anchor != "" || node.Kind == yaml.AliasNode {
		return nil, fmt.Errorf("YAML anchors and aliases are not allowed")
	}
	if node.Style&yaml.TaggedStyle != 0 {
		return nil, fmt.Errorf("explicit YAML tags are not allowed")
	}
	if depth > maxDefinitionJSONDepth {
		return nil, fmt.Errorf("YAML nesting exceeds %d", maxDefinitionJSONDepth)
	}
	switch node.Kind {
	case yaml.MappingNode:
		if depth >= maxDefinitionJSONDepth {
			return nil, fmt.Errorf("YAML nesting exceeds %d", maxDefinitionJSONDepth)
		}
		result := make(map[string]any, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if key.Anchor != "" || key.Style&yaml.TaggedStyle != 0 || key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return nil, fmt.Errorf("YAML mapping keys must be untagged strings")
			}
			if key.Value == "<<" || key.Tag == "!!merge" {
				return nil, fmt.Errorf("YAML merge keys are not allowed")
			}
			if len(key.Value) > maxDefinitionJSONStringBytes {
				return nil, fmt.Errorf("YAML mapping key exceeds %d bytes", maxDefinitionJSONStringBytes)
			}
			if _, exists := result[key.Value]; exists {
				return nil, fmt.Errorf("duplicate YAML mapping key %q", key.Value)
			}
			*members++
			if *members > maxDefinitionJSONMembers {
				return nil, fmt.Errorf("YAML member count exceeds %d", maxDefinitionJSONMembers)
			}
			value, err := authoringYAMLValueV1(node.Content[index+1], depth+1, members)
			if err != nil {
				return nil, err
			}
			result[key.Value] = value
		}
		return result, nil
	case yaml.SequenceNode:
		if depth >= maxDefinitionJSONDepth {
			return nil, fmt.Errorf("YAML nesting exceeds %d", maxDefinitionJSONDepth)
		}
		result := make([]any, 0, len(node.Content))
		for _, child := range node.Content {
			*members++
			if *members > maxDefinitionJSONMembers {
				return nil, fmt.Errorf("YAML member count exceeds %d", maxDefinitionJSONMembers)
			}
			value, err := authoringYAMLValueV1(child, depth+1, members)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		return result, nil
	case yaml.ScalarNode:
		if len(node.Value) > maxDefinitionJSONStringBytes {
			return nil, fmt.Errorf("YAML scalar exceeds %d bytes", maxDefinitionJSONStringBytes)
		}
		switch node.Tag {
		case "!!str":
			return node.Value, nil
		case "!!null":
			if node.Value != "null" {
				return nil, fmt.Errorf("YAML-specific null scalar %q is not allowed", node.Value)
			}
			return nil, nil
		case "!!bool":
			if node.Value == "true" {
				return true, nil
			}
			if node.Value == "false" {
				return false, nil
			}
			return nil, fmt.Errorf("YAML-specific boolean scalar %q is not allowed", node.Value)
		case "!!int", "!!float":
			if !jsonNumberPatternV1.MatchString(node.Value) {
				return nil, fmt.Errorf("YAML-specific number scalar %q is not allowed", node.Value)
			}
			return json.Number(node.Value), nil
		default:
			return nil, fmt.Errorf("YAML-specific scalar tag %q is not allowed", node.Tag)
		}
	default:
		return nil, fmt.Errorf("unsupported YAML node kind %d", node.Kind)
	}
}

func requiredAuthoringStringV1(values map[string]any, name string) (string, error) {
	value, exists := values[name]
	text, ok := value.(string)
	if !exists || !ok || text == "" || strings.TrimSpace(text) != text || containsControlV1(text) {
		return "", fmt.Errorf("required authoring field %q must be a nonempty canonical string", name)
	}
	return text, nil
}

func normalizeAuthoringSourcePathV1(value string) (string, error) {
	if err := validateAuthoringLexicalPathV1(value, false, false); err != nil {
		return "", err
	}
	normalized := path.Clean(value)
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") {
		return "", fmt.Errorf("source path %q escapes the trusted boundary", value)
	}
	return normalized, nil
}

func resolveAuthoringRootV1(entrySource string, value string) (string, error) {
	if err := validateAuthoringLexicalPathV1(value, false, true); err != nil {
		return "", fmt.Errorf("imports.root: %w", err)
	}
	resolved := path.Clean(path.Join(path.Dir(entrySource), value))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", fmt.Errorf("imports.root %q escapes the trusted boundary", value)
	}
	return resolved, nil
}

func resolveAuthoringImportV1(importer string, value string, logicalRoot string) (string, error) {
	rootRelative := strings.HasPrefix(value, "/")
	if err := validateAuthoringLexicalPathV1(value, rootRelative, false); err != nil {
		return "", err
	}
	var resolved string
	if rootRelative {
		if logicalRoot == "" {
			return "", fmt.Errorf("root-relative path %q requires imports.root", value)
		}
		resolved = path.Join(logicalRoot, strings.TrimPrefix(value, "/"))
	} else {
		resolved = path.Join(path.Dir(importer), value)
	}
	resolved = path.Clean(resolved)
	if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", fmt.Errorf("import path %q escapes the trusted boundary", value)
	}
	if logicalRoot != "" && !authoringPathWithinV1(logicalRoot, resolved) {
		return "", fmt.Errorf("import path %q escapes logical root %q", value, logicalRoot)
	}
	return resolved, nil
}

func validateAuthoringLexicalPathV1(value string, rootRelative bool, allowDirectorySuffix bool) error {
	if value == "" || containsControlV1(value) || strings.Contains(value, `\`) || strings.Contains(value, "://") || strings.HasPrefix(value, "//") || windowsVolumePatternV1.MatchString(value) || strings.Contains(value, ":") {
		return fmt.Errorf("authoring path %q is not a local slash path", value)
	}
	if rootRelative != strings.HasPrefix(value, "/") {
		return fmt.Errorf("authoring path %q has invalid absolute-path form", value)
	}
	check := value
	if rootRelative {
		check = strings.TrimPrefix(check, "/")
	}
	if allowDirectorySuffix {
		check = strings.TrimSuffix(check, "/")
	}
	if check == "" {
		return fmt.Errorf("authoring path %q is empty", value)
	}
	for _, segment := range strings.Split(check, "/") {
		if segment == "" {
			return fmt.Errorf("authoring path %q contains an empty segment", value)
		}
		if rootRelative && (segment == "." || segment == "..") {
			return fmt.Errorf("root-relative path %q contains a dot segment", value)
		}
	}
	return nil
}

func authoringPathWithinV1(root string, filename string) bool {
	return root == "." || filename == root || strings.HasPrefix(filename, root+"/")
}

func cloneAuthoringObjectV1(value map[string]any) map[string]any {
	clone := make(map[string]any, len(value))
	for name, child := range value {
		clone[name] = cloneAuthoringValueV1(child)
	}
	return clone
}

func cloneAuthoringValueV1(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAuthoringObjectV1(typed)
	case []any:
		clone := make([]any, len(typed))
		for index, child := range typed {
			clone[index] = cloneAuthoringValueV1(child)
		}
		return clone
	default:
		return typed
	}
}

func authoringChainErrorV1(chain []string, format string, arguments ...any) error {
	return fmt.Errorf("portable tool authoring chain %s: %s", strings.Join(chain, " -> "), fmt.Sprintf(format, arguments...))
}

func authoringCompositionErrorV1(sources []portableToolAuthoringDefinitionSourceV1, cause error) error {
	message := cause.Error()
	relevant := make([]portableToolAuthoringDefinitionSourceV1, 0, len(sources))
	for _, source := range sources {
		if strings.Contains(message, strconv.Quote(source.id)) || strings.Contains(message, strconv.Quote(source.output)) {
			relevant = append(relevant, source)
		}
	}
	if len(relevant) == 0 {
		relevant = sources
	}
	details := make([]string, 0, len(relevant))
	for _, source := range relevant {
		details = append(details, fmt.Sprintf("%q <= %s", source.output, strings.Join(source.chain, " -> ")))
	}
	return fmt.Errorf("compose portable tool authoring entries from %s: %w", strings.Join(details, "; "), cause)
}
