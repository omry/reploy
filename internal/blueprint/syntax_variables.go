package blueprint

import (
	"fmt"
	"reflect"
	"strings"
)

// resolveSyntaxVariables expands global blueprint variables across every
// string-valued schema field before field-specific validation. Namespaced
// expressions remain lazy for their operation-time consumers.
func resolveSyntaxVariables(source Syntax, variables map[string]any) (Syntax, error) {
	source = cloneSyntaxValue(reflect.ValueOf(source)).Interface().(Syntax)
	saved := source.Environment.Vars
	source.Environment.Vars = nil
	value := reflect.ValueOf(&source).Elem()
	if err := resolveSyntaxVariableValue(value, variables, "blueprint"); err != nil {
		return Syntax{}, err
	}
	source.Environment.Vars = saved
	return source, nil
}

func cloneSyntaxValue(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		clone := reflect.New(value.Type().Elem())
		clone.Elem().Set(cloneSyntaxValue(value.Elem()))
		return clone
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		clone := cloneSyntaxValue(value.Elem())
		wrapped := reflect.New(value.Type()).Elem()
		wrapped.Set(clone)
		return wrapped
	case reflect.Struct:
		clone := reflect.New(value.Type()).Elem()
		for index := 0; index < value.NumField(); index++ {
			clone.Field(index).Set(cloneSyntaxValue(value.Field(index)))
		}
		return clone
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		clone := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			clone.Index(index).Set(cloneSyntaxValue(value.Index(index)))
		}
		return clone
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		clone := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			clone.SetMapIndex(iterator.Key(), cloneSyntaxValue(iterator.Value()))
		}
		return clone
	default:
		return value
	}
}

func resolveSyntaxVariableValue(value reflect.Value, variables map[string]any, field string) error {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return nil
		}
		if text, ok := value.Interface().(string); ok {
			resolved, err := resolveGlobalVariableAny(text, variables)
			if err != nil {
				return fmt.Errorf("%s: %w", field, err)
			}
			if resolved == nil {
				value.SetZero()
				return nil
			}
			value.Set(reflect.ValueOf(resolved))
			return nil
		}
		item := reflect.New(value.Elem().Type()).Elem()
		item.Set(value.Elem())
		if err := resolveSyntaxVariableValue(item, variables, field); err != nil {
			return err
		}
		value.Set(item)
	case reflect.Pointer:
		if !value.IsNil() {
			return resolveSyntaxVariableValue(value.Elem(), variables, field)
		}
	case reflect.Struct:
		typeOf := value.Type()
		for index := 0; index < value.NumField(); index++ {
			name := typeOf.Field(index).Tag.Get("yaml")
			if name == "" {
				name = typeOf.Field(index).Name
			}
			if err := resolveSyntaxVariableValue(value.Field(index), variables, field+"."+name); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for index := 0; index < value.Len(); index++ {
			if err := resolveSyntaxVariableValue(value.Index(index), variables, fmt.Sprintf("%s[%d]", field, index)); err != nil {
				return err
			}
		}
	case reflect.Map:
		if value.IsNil() {
			return nil
		}
		iterator := value.MapRange()
		for iterator.Next() {
			key := iterator.Key()
			item := reflect.New(value.Type().Elem()).Elem()
			item.Set(iterator.Value())
			if err := resolveSyntaxVariableValue(item, variables, fmt.Sprintf("%s.%v", field, key.Interface())); err != nil {
				return err
			}
			value.SetMapIndex(key, item)
		}
	case reflect.String:
		resolved, err := resolveGlobalVariableString(value.String(), variables)
		if err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
		value.SetString(resolved)
	}
	return nil
}

func resolveGlobalVariableString(value string, variables map[string]any) (string, error) {
	resolved, err := resolveGlobalVariableAny(value, variables)
	if err != nil {
		return "", err
	}
	text, ok := resolved.(string)
	if !ok {
		return fmt.Sprint(resolved), nil
	}
	return text, nil
}

func resolveGlobalVariableAny(value string, variables map[string]any) (any, error) {
	if match := wholeInterpolationPattern.FindStringSubmatch(value); match != nil && !strings.Contains(match[1], ".") {
		item, ok := variables[match[1]]
		if !ok {
			return nil, fmt.Errorf("unknown blueprint variable %q", match[1])
		}
		return item, nil
	}
	var interpolationErr error
	resolved := interpolationPattern.ReplaceAllStringFunc(value, func(token string) string {
		if interpolationErr != nil {
			return token
		}
		match := interpolationPattern.FindStringSubmatch(token)
		name := match[1]
		if strings.Contains(name, ".") {
			return token
		}
		item, ok := variables[name]
		if !ok {
			interpolationErr = fmt.Errorf("unknown blueprint variable %q", name)
			return token
		}
		switch item.(type) {
		case []any, []string, map[string]any:
			interpolationErr = fmt.Errorf("variable %q is not scalar", name)
			return token
		default:
			return fmt.Sprint(item)
		}
	})
	return resolved, interpolationErr
}
