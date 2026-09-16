package python

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"golang.org/x/text/unicode/norm"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	pep440 "github.com/aquasecurity/go-pep440-version"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/wheelinventory"
)

const maxWheelMetadataMemberBytes int64 = 1 << 20
const maxWheelMetadataTotalBytes int64 = 4 << 20

type WheelConsoleScriptV1 struct{ Name, Target string }
type WheelInventoryPathV1 struct {
	Path             string
	Kind             wheelinventory.Kind
	UncompressedSize uint64
}

// WheelInspectionV1 contains only descriptor-authenticated observed wheel data.
// It makes no runtime eligibility or selected binding-contract decision.
type WheelInspectionV1 struct {
	Artifact                                        providerstore.ArtifactDescriptor
	Distribution, Version, RequiresPython, Filename string
	FilenameTags, InternalTags                      []string
	RootIsPurelib                                   bool
	ConsoleScripts                                  []WheelConsoleScriptV1
	Inventory                                       []WheelInventoryPathV1
	DeclaredDependencies                            []string
}

func inspectWheelPath(ctx context.Context, filename, logicalPath string, expected *providerstore.ArtifactDescriptor) (WheelInspectionV1, error) {
	before, err := os.Lstat(filename)
	if err != nil {
		return WheelInspectionV1{}, err
	}
	if !before.Mode().IsRegular() {
		return WheelInspectionV1{}, fmt.Errorf("Python wheel must be a regular file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return WheelInspectionV1{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return WheelInspectionV1{}, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return WheelInspectionV1{}, fmt.Errorf("Python wheel file identity changed")
	}
	return inspectWheelReader(ctx, file, info.Size(), filepath.Base(filename), logicalPath, expected)
}

func wheelReaderDigest(ctx context.Context, reader io.ReaderAt, size int64) (canonical.Digest, error) {
	if ctx == nil || reader == nil || size < 0 {
		return "", fmt.Errorf("wheel digest requires context, reader, and nonnegative size")
	}
	hash := sha256.New()
	section := io.NewSectionReader(reader, 0, size)
	buffer := make([]byte, 32*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := section.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
			total += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	if total != size {
		return "", fmt.Errorf("wheel size does not match descriptor")
	}
	return canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil))), nil
}

// InspectWheelReaderV1 verifies the exact descriptor before opening any member.
// The caller owns the descriptor-stable reader for the duration of inspection.
func InspectWheelReaderV1(ctx context.Context, reader io.ReaderAt, size int64, filename string, descriptor providerstore.ArtifactDescriptor) (WheelInspectionV1, error) {
	return inspectWheelReader(ctx, reader, size, filename, descriptor.LogicalPath, &descriptor)
}

// The owned path adapter may derive its descriptor from the single digest pass;
// descriptor-bound callers instead compare that same pass with their authority.
func inspectWheelReader(ctx context.Context, reader io.ReaderAt, size int64, filename, logicalPath string, expected *providerstore.ArtifactDescriptor) (WheelInspectionV1, error) {
	var result WheelInspectionV1
	if ctx == nil || reader == nil || size < 0 {
		return result, fmt.Errorf("wheel inspection requires context, reader, and nonnegative size")
	}
	descriptor := providerstore.ArtifactDescriptor{LogicalPath: logicalPath, Kind: "wheel", Size: strconv.FormatInt(size, 10)}
	if expected != nil {
		descriptor = *expected
		if err := descriptor.Validate(); err != nil {
			return result, fmt.Errorf("wheel descriptor: %w", err)
		}
	}
	if descriptor.Kind != "wheel" || filename != path.Base(filename) || path.Base(descriptor.LogicalPath) != filename {
		return result, fmt.Errorf("wheel descriptor kind or filename does not match")
	}
	if descriptor.Size != strconv.FormatInt(size, 10) {
		return result, fmt.Errorf("wheel size does not match descriptor")
	}
	if file, ok := reader.(*os.File); ok {
		if file == nil {
			return result, fmt.Errorf("wheel requires an open regular file")
		}
		info, err := file.Stat()
		if err != nil {
			return result, err
		}
		if !info.Mode().IsRegular() || info.Size() != size {
			return result, fmt.Errorf("wheel file size or kind does not match descriptor")
		}
	}
	// Ordinary wheels may preserve distribution and version spelling. Project
	// their normalized identity through the shared canonical parser, while
	// retaining the original filename for descriptor binding and observed data.
	distribution, suffix, found := strings.Cut(filename, "-")
	if !found || !wheelCoreNamePattern.MatchString(distribution) {
		return result, fmt.Errorf("wheel filename contains an invalid distribution")
	}
	canonicalDistribution := strings.ReplaceAll(NormalizeDistributionName(distribution), "-", "_")
	versionText, tagSuffix, found := strings.Cut(suffix, "-")
	version, err := pep440.Parse(versionText)
	if !found || versionText != strings.TrimSpace(versionText) || err != nil {
		return result, fmt.Errorf("wheel filename contains an invalid version")
	}
	projection, err := portabletool.ProjectWheelFilenameV1(canonicalDistribution + "-" + version.String() + "-" + tagSuffix)
	if err != nil {
		return result, err
	}
	digest, err := wheelReaderDigest(ctx, reader, size)
	if err != nil {
		return result, err
	}
	if expected != nil && digest != descriptor.SHA256 {
		return result, fmt.Errorf("wheel digest does not match descriptor")
	}
	descriptor.SHA256 = digest
	if err := descriptor.Validate(); err != nil {
		return result, fmt.Errorf("wheel descriptor: %w", err)
	}
	inventory, err := wheelinventory.Read(ctx, reader, size)
	if err != nil {
		return result, err
	}
	root := ""
	selected := map[string]*zip.File{}
	result.Inventory = make([]WheelInventoryPathV1, 0, len(inventory.Entries))
	for _, entry := range inventory.Entries {
		result.Inventory = append(result.Inventory, WheelInventoryPathV1{entry.Path, entry.Kind, entry.UncompressedSize})
		first, rest, nested := strings.Cut(entry.Path, "/")
		if strings.HasSuffix(first, ".dist-info") {
			identity := strings.TrimSuffix(first, ".dist-info")
			split := strings.LastIndexByte(identity, '-')
			if split <= 0 {
				return WheelInspectionV1{}, fmt.Errorf("wheel has malformed .dist-info root %q", first)
			}
			version, versionErr := pep440.Parse(identity[split+1:])
			if !wheelCoreNamePattern.MatchString(identity[:split]) || NormalizeDistributionName(identity[:split]) != projection.Distribution || versionErr != nil || version.String() != projection.EcosystemVersion || root != "" && root != first {
				return WheelInspectionV1{}, fmt.Errorf("wheel has ambiguous or mismatched .dist-info root %q", first)
			}
			root = first
		}

		if first != root || !nested {
			continue
		}
		if rest != "METADATA" && rest != "WHEEL" && rest != "entry_points.txt" {
			continue
		}
		if entry.Kind != wheelinventory.Regular {
			return WheelInspectionV1{}, fmt.Errorf("wheel metadata member %q must be regular", entry.Path)
		}
		selected[rest] = entry.File
	}
	if selected["METADATA"] == nil || selected["WHEEL"] == nil {
		return WheelInspectionV1{}, fmt.Errorf("wheel must contain exactly one .dist-info/METADATA and .dist-info/WHEEL")
	}
	var declared int64
	for _, name := range []string{"METADATA", "WHEEL", "entry_points.txt"} {
		member := selected[name]
		if member == nil {
			continue
		}
		if member.UncompressedSize64 > uint64(maxWheelMetadataMemberBytes) {
			return WheelInspectionV1{}, fmt.Errorf("wheel %s metadata exceeds %d bytes", name, maxWheelMetadataMemberBytes)
		}
		declared += int64(member.UncompressedSize64)
	}
	if declared > maxWheelMetadataTotalBytes {
		return WheelInspectionV1{}, fmt.Errorf("wheel aggregate metadata exceeds %d bytes", maxWheelMetadataTotalBytes)
	}
	var consumed int64
	seen := map[string]bool{}
	dependencies := map[string]bool{}
	err = readBoundedWheelMember(ctx, selected["METADATA"], &consumed, func(r io.Reader) error {
		return readMetadataFields(r, "wheel metadata", func(name string) bool {
			return name == "name" || name == "version" || name == "requires-python" || name == "requires-dist"
		}, func(name, value string) error {
			if name != "requires-dist" && seen[name] {
				return fmt.Errorf("wheel metadata contains duplicate %s fields", name)
			}
			seen[name] = true
			switch name {
			case "name":
				if !wheelCoreNamePattern.MatchString(value) {
					return fmt.Errorf("invalid wheel metadata Name")
				}
				result.Distribution = NormalizeDistributionName(value)
			case "version":
				version, err := pep440.Parse(value)
				if err != nil {
					return fmt.Errorf("invalid wheel metadata Version: %w", err)
				}
				result.Version = version.String()
			case "requires-python":
				_, err := pep440.NewSpecifiers(value)
				if value == "" || err != nil {
					return fmt.Errorf("invalid wheel Requires-Python")
				}
				result.RequiresPython = strings.Join(strings.Fields(value), "")
				if err := portabletool.ValidatePythonRequiresPythonV1(result.RequiresPython); err != nil {
					return fmt.Errorf("wheel Requires-Python: %w", err)
				}
			case "requires-dist":
				name, err := wheelRequirementDistributionName(value)
				if err != nil {
					return fmt.Errorf("wheel Requires-Dist: %w", err)
				}
				dependencies[name] = true
			}
			return nil
		})
	})
	if err != nil {
		return WheelInspectionV1{}, err
	}
	if result.Distribution == "" || result.Version == "" {
		return WheelInspectionV1{}, fmt.Errorf("wheel metadata requires Name and Version")
	}
	if result.Distribution != projection.Distribution || result.Version != projection.EcosystemVersion {
		return WheelInspectionV1{}, fmt.Errorf("wheel filename and core metadata identity mismatch")
	}
	seen = map[string]bool{}
	tags := map[string]bool{}
	err = readBoundedWheelMember(ctx, selected["WHEEL"], &consumed, func(r io.Reader) error {
		return readMetadataFields(r, "WHEEL metadata", func(name string) bool { return name == "wheel-version" || name == "root-is-purelib" || name == "tag" }, func(name, value string) error {
			if name != "tag" && seen[name] {
				return fmt.Errorf("WHEEL contains duplicate %s fields", name)
			}
			seen[name] = true
			switch name {
			case "wheel-version":
				if value != "1.0" {
					return fmt.Errorf("unsupported Wheel-Version %q", value)
				}
			case "root-is-purelib":
				if value != "true" && value != "false" {
					return fmt.Errorf("invalid Root-Is-Purelib %q", value)
				}
				result.RootIsPurelib = value == "true"
			case "tag":
				if err := portabletool.ValidateWheelTagV1(value); err != nil {
					return err
				}
				if tags[value] {
					return fmt.Errorf("duplicate WHEEL Tag %q", value)
				}
				tags[value] = true
			}
			return nil
		})
	})
	if err != nil {
		return WheelInspectionV1{}, err
	}
	if !seen["wheel-version"] || !seen["root-is-purelib"] || len(tags) == 0 {
		return WheelInspectionV1{}, fmt.Errorf("WHEEL requires Wheel-Version, Root-Is-Purelib and Tag")
	}
	result.ConsoleScripts = []WheelConsoleScriptV1{}
	if member := selected["entry_points.txt"]; member != nil {
		err = readBoundedWheelMember(ctx, member, &consumed, func(r io.Reader) error { var e error; result.ConsoleScripts, e = readWheelConsoleScripts(r); return e })
		if err != nil {
			return WheelInspectionV1{}, err
		}
	}
	result.Artifact = descriptor
	result.Filename = filename
	result.FilenameTags = projection.Tags
	result.InternalTags = sortedWheelKeys(tags)
	result.DeclaredDependencies = sortedWheelKeys(dependencies)
	sort.Slice(result.Inventory, func(i, j int) bool { return result.Inventory[i].Path < result.Inventory[j].Path })
	return result, nil
}

var wheelCoreNamePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// ValidateWheelConsoleScriptTargetV1 checks the PyPA module:object [extras]
// grammar without importing or executing the referenced object. Whitespace
// around the colon and extras delimiters is accepted by the entry-point format.
func ValidateWheelConsoleScriptTargetV1(value string) error {
	object := strings.TrimSpace(value)
	if before, extras, found := strings.Cut(object, "["); found {
		if !strings.HasSuffix(extras, "]") {
			return fmt.Errorf("invalid console script target %q", value)
		}
		extras = strings.TrimSpace(strings.TrimSuffix(extras, "]"))
		if extras != "" {
			for _, extra := range strings.Split(extras, ",") {
				if !wheelCoreNamePattern.MatchString(strings.TrimSpace(extra)) {
					return fmt.Errorf("invalid console script extras %q", value)
				}
			}
		}
		object = strings.TrimSpace(before)
	}
	module, attribute, found := strings.Cut(object, ":")
	if !found {
		return fmt.Errorf("invalid console script target %q", value)
	}
	for _, dotted := range []string{strings.TrimSpace(module), strings.TrimSpace(attribute)} {
		for _, component := range strings.Split(dotted, ".") {
			if !wheelPythonIdentifier(component) {
				return fmt.Errorf("invalid console script target %q", value)
			}
		}
	}
	return nil
}

func wheelPythonIdentifier(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	// Python identifiers use the NFKC-closed Unicode identifier categories.
	for _, spelling := range []string{value, norm.NFKC.String(value)} {
		for index, ch := range spelling {
			start := ch == '_' || unicode.IsLetter(ch) || unicode.Is(unicode.Nl, ch) || unicode.Is(unicode.Other_ID_Start, ch)
			if start {
				continue
			}
			if index == 0 || !(unicode.Is(unicode.Mn, ch) || unicode.Is(unicode.Mc, ch) || unicode.Is(unicode.Nd, ch) || unicode.Is(unicode.Pc, ch) || unicode.Is(unicode.Other_ID_Continue, ch)) {
				return false
			}
		}
	}
	return true
}

func sortedWheelKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for v := range values {
		result = append(result, v)
	}
	sort.Strings(result)
	return result
}

func readBoundedWheelMember(ctx context.Context, file *zip.File, total *int64, parse func(io.Reader) error) error {
	reader, err := file.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	limit := maxWheelMetadataMemberBytes
	if remaining := maxWheelMetadataTotalBytes - *total; remaining < limit {
		limit = remaining
	}
	bounded := &io.LimitedReader{R: reader, N: limit + 1}
	checked := wheelMetadataContextReader{ctx: ctx, reader: bounded}
	if err := parse(checked); err != nil {
		return err
	}
	if _, err := io.Copy(io.Discard, checked); err != nil {
		return err
	}
	consumed := limit + 1 - bounded.N
	*total += consumed
	if consumed > limit {
		return fmt.Errorf("wheel metadata exceeds fixed byte limit")
	}
	return nil
}

type wheelMetadataContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r wheelMetadataContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func readWheelConsoleScripts(reader io.Reader) ([]WheelConsoleScriptV1, error) {
	scripts := map[string]string{}
	selected := false
	seenSection := false
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), int(maxWheelMetadataMemberBytes)+1)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			selected = line == "[console_scripts]"
			if selected && seenSection {
				return nil, fmt.Errorf("duplicate console_scripts section")
			}
			if selected {
				seenSection = true
			}
			continue
		}
		if !selected || line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		name, target, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		target = strings.TrimSpace(target)
		if !ok || name == "" || !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 || strings.ContainsAny(name, "/\\=\x00") || strings.HasPrefix(name, "[") || name == "." || name == ".." {
			return nil, fmt.Errorf("invalid console script entry %q", line)
		}
		if _, found := scripts[name]; found {
			return nil, fmt.Errorf("duplicate console script %q", name)
		}
		if err := ValidateWheelConsoleScriptTargetV1(target); err != nil {
			return nil, err
		}
		scripts[name] = target
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	result := make([]WheelConsoleScriptV1, 0, len(scripts))
	for name, target := range scripts {
		result = append(result, WheelConsoleScriptV1{name, target})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
