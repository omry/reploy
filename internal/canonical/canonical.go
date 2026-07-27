// Package canonical provides the single canonical JSON and identity service
// used by Reploy's content-addressed records.
package canonical

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const JSONSchema = "canonical-json-v1"

// Digest is a lowercase SHA-256 digest in the canonical sha256:<hex> form.
type Digest string

// Object is a canonical JSON object. Marshal validates the complete value tree
// before emitting it; numbers and provider-defined serialization hooks are not
// part of canonical-json-v1.
type Object map[string]any

// Envelope carries provider-owned canonical data across package boundaries.
// The owner of Schema remains responsible for validating the exact Value shape.
type Envelope struct {
	Schema string `json:"schema"`
	Value  Object `json:"value"`
}

// Marshal returns the canonical-json-v1 representation of value.
//
// Identity values may contain objects, arrays, strings, booleans, and null.
// Numeric Go values are rejected: schema-normalized records represent integers
// as canonical decimal strings and never use floating-point values.
func Marshal(value any) ([]byte, error) {
	var output bytes.Buffer
	encoder := encoder{output: &output, active: make(map[visit]bool)}
	if err := encoder.write(reflect.ValueOf(value), "$"); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// ParseDigest validates and returns a canonical lowercase SHA-256 digest.
func ParseDigest(value string) (Digest, error) {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+sha256.Size*2 {
		return "", fmt.Errorf("canonical digest %q must use sha256 followed by 64 lowercase hexadecimal characters", value)
	}
	encoded := value[len(prefix):]
	for _, char := range encoded {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return "", fmt.Errorf("canonical digest %q must use sha256 followed by 64 lowercase hexadecimal characters", value)
		}
	}
	return Digest(value), nil
}

// Validate reports whether digest uses the canonical digest grammar.
func (digest Digest) Validate() error {
	_, err := ParseDigest(string(digest))
	return err
}

// Sum returns the domain-separated SHA-256 identity of value.
func Sum(kind string, schema string, value any) (Digest, error) {
	if err := validateIdentityToken("kind", kind); err != nil {
		return "", err
	}
	if err := validateIdentityToken("schema", schema); err != nil {
		return "", err
	}
	payload, err := Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("reploy:" + kind + ":" + schema))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return Digest("sha256:" + hex.EncodeToString(hash.Sum(nil))), nil
}

func validateIdentityToken(field string, value string) error {
	if value == "" {
		return fmt.Errorf("canonical identity %s must not be empty", field)
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return fmt.Errorf("canonical identity %s %q must contain only lowercase ASCII letters, digits, and hyphens", field, value)
		}
	}
	return nil
}

type visit struct {
	kind reflect.Kind
	ptr  uintptr
}

type encoder struct {
	output *bytes.Buffer
	active map[visit]bool
}

func (encoder *encoder) write(value reflect.Value, path string) error {
	if !value.IsValid() {
		encoder.output.WriteString("null")
		return nil
	}
	for value.Kind() == reflect.Interface {
		if value.IsNil() {
			encoder.output.WriteString("null")
			return nil
		}
		value = value.Elem()
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			encoder.output.WriteString("null")
			return nil
		}
		if err := encoder.enter(value, path); err != nil {
			return err
		}
		defer encoder.leave(value)
		return encoder.write(value.Elem(), path)
	}

	switch value.Kind() {
	case reflect.Bool:
		if value.Bool() {
			encoder.output.WriteString("true")
		} else {
			encoder.output.WriteString("false")
		}
		return nil
	case reflect.String:
		return writeString(encoder.output, value.String(), path)
	case reflect.Slice:
		if value.IsNil() {
			encoder.output.WriteString("null")
			return nil
		}
		if err := encoder.enter(value, path); err != nil {
			return err
		}
		defer encoder.leave(value)
		return encoder.writeArray(value, path)
	case reflect.Array:
		return encoder.writeArray(value, path)
	case reflect.Map:
		if value.IsNil() {
			encoder.output.WriteString("null")
			return nil
		}
		if value.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("canonical JSON %s has unsupported map key type %s", path, value.Type().Key())
		}
		if err := encoder.enter(value, path); err != nil {
			return err
		}
		defer encoder.leave(value)
		return encoder.writeMap(value, path)
	case reflect.Struct:
		return encoder.writeStruct(value, path)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Uintptr, reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return fmt.Errorf("canonical JSON %s has unsupported numeric type %s; encode schema integers as decimal strings", path, value.Type())
	default:
		return fmt.Errorf("canonical JSON %s has unsupported type %s", path, value.Type())
	}
}

func (encoder *encoder) enter(value reflect.Value, path string) error {
	pointer := value.Pointer()
	if pointer == 0 {
		return nil
	}
	key := visit{kind: value.Kind(), ptr: pointer}
	if encoder.active[key] {
		return fmt.Errorf("canonical JSON %s contains a cycle", path)
	}
	encoder.active[key] = true
	return nil
}

func (encoder *encoder) leave(value reflect.Value) {
	pointer := value.Pointer()
	if pointer != 0 {
		delete(encoder.active, visit{kind: value.Kind(), ptr: pointer})
	}
}

func (encoder *encoder) writeArray(value reflect.Value, path string) error {
	encoder.output.WriteByte('[')
	for index := 0; index < value.Len(); index++ {
		if index > 0 {
			encoder.output.WriteByte(',')
		}
		if err := encoder.write(value.Index(index), fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return err
		}
	}
	encoder.output.WriteByte(']')
	return nil
}

func (encoder *encoder) writeMap(value reflect.Value, path string) error {
	keys := value.MapKeys()
	sort.Slice(keys, func(left int, right int) bool {
		return compareUTF16(keys[left].String(), keys[right].String()) < 0
	})
	encoder.output.WriteByte('{')
	for index, key := range keys {
		name := key.String()
		if !utf8.ValidString(name) {
			return fmt.Errorf("canonical JSON %s contains an invalid UTF-8 object key", path)
		}
		if index > 0 {
			encoder.output.WriteByte(',')
		}
		if err := writeString(encoder.output, name, path); err != nil {
			return err
		}
		encoder.output.WriteByte(':')
		if err := encoder.write(value.MapIndex(key), path+"."+name); err != nil {
			return err
		}
	}
	encoder.output.WriteByte('}')
	return nil
}

type structField struct {
	name  string
	value reflect.Value
}

func (encoder *encoder) writeStruct(value reflect.Value, path string) error {
	fields := make([]structField, 0, value.NumField())
	seen := make(map[string]bool)
	typeInfo := value.Type()
	for index := 0; index < value.NumField(); index++ {
		fieldInfo := typeInfo.Field(index)
		if fieldInfo.PkgPath != "" {
			continue
		}
		name, omitEmpty, err := parseJSONTag(fieldInfo.Tag.Get("json"))
		if err != nil {
			return fmt.Errorf("canonical JSON %s field %s: %w", path, fieldInfo.Name, err)
		}
		if name == "-" {
			continue
		}
		if name == "" {
			name = fieldInfo.Name
		}
		if !utf8.ValidString(name) {
			return fmt.Errorf("canonical JSON %s contains an invalid UTF-8 field name", path)
		}
		fieldValue := value.Field(index)
		if omitEmpty && isEmptyValue(fieldValue) {
			continue
		}
		if seen[name] {
			return fmt.Errorf("canonical JSON %s contains duplicate field %q", path, name)
		}
		seen[name] = true
		fields = append(fields, structField{name: name, value: fieldValue})
	}
	sort.Slice(fields, func(left int, right int) bool {
		return compareUTF16(fields[left].name, fields[right].name) < 0
	})
	encoder.output.WriteByte('{')
	for index, field := range fields {
		if index > 0 {
			encoder.output.WriteByte(',')
		}
		if err := writeString(encoder.output, field.name, path); err != nil {
			return err
		}
		encoder.output.WriteByte(':')
		if err := encoder.write(field.value, path+"."+field.name); err != nil {
			return err
		}
	}
	encoder.output.WriteByte('}')
	return nil
}

func parseJSONTag(tag string) (string, bool, error) {
	parts := strings.Split(tag, ",")
	omitEmpty := false
	for _, option := range parts[1:] {
		switch option {
		case "omitempty":
			if omitEmpty {
				return "", false, fmt.Errorf("duplicate json tag option %q", option)
			}
			omitEmpty = true
		default:
			return "", false, fmt.Errorf("unsupported json tag option %q", option)
		}
	}
	return parts[0], omitEmpty, nil
}

func isEmptyValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return value.Len() == 0
	case reflect.Bool:
		return !value.Bool()
	case reflect.Interface, reflect.Pointer:
		return value.IsNil()
	default:
		return false
	}
}

func compareUTF16(left string, right string) int {
	leftUnits := utf16.Encode([]rune(left))
	rightUnits := utf16.Encode([]rune(right))
	limit := len(leftUnits)
	if len(rightUnits) < limit {
		limit = len(rightUnits)
	}
	for index := 0; index < limit; index++ {
		if leftUnits[index] < rightUnits[index] {
			return -1
		}
		if leftUnits[index] > rightUnits[index] {
			return 1
		}
	}
	if len(leftUnits) < len(rightUnits) {
		return -1
	}
	if len(leftUnits) > len(rightUnits) {
		return 1
	}
	return 0
}

func writeString(output *bytes.Buffer, value string, path string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("canonical JSON %s contains invalid UTF-8", path)
	}
	output.WriteByte('"')
	for _, char := range value {
		switch char {
		case '"', '\\':
			output.WriteByte('\\')
			output.WriteRune(char)
		case '\b':
			output.WriteString(`\b`)
		case '\t':
			output.WriteString(`\t`)
		case '\n':
			output.WriteString(`\n`)
		case '\f':
			output.WriteString(`\f`)
		case '\r':
			output.WriteString(`\r`)
		default:
			if char < 0x20 {
				fmt.Fprintf(output, `\u%04x`, char)
			} else {
				output.WriteRune(char)
			}
		}
	}
	output.WriteByte('"')
	return nil
}
