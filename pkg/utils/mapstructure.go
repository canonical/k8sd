package utils

import (
	"fmt"
	"reflect"
	"strings"
	"unicode"

	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v2"
)

// YAMLToStringSliceHookFunc returns a mapstructure.DecodeHookFunc that converts string to []string by parsing YAML.
func YAMLToStringSliceHookFunc(f reflect.Kind, t reflect.Kind, data interface{}) (interface{}, error) {
	if f != reflect.String || t != reflect.Slice {
		return data, nil
	}

	strData, ok := data.(string)
	if !ok {
		return nil, fmt.Errorf("expected string but got %T", data)
	}
	if strData == "" {
		return data, nil
	}

	var result []string
	if err := yaml.Unmarshal([]byte(strData), &result); err != nil {
		return data, nil
	}

	return result, nil
}

// YAMLToMapSliceHookFunc returns a mapstructure.DecodeHookFunc that converts a YAML string
// describing a list of objects (e.g. "- target: {...}\n  strategic-merge: ...") into
// []map[string]any, so that mapstructure can subsequently decode it into a []struct field
// (e.g. []types.Patch). It only fires if the string was not already consumed by an earlier
// hook (e.g. YAMLToStringSliceHookFunc, which handles plain []string fields).
func YAMLToMapSliceHookFunc(f reflect.Kind, t reflect.Kind, data interface{}) (interface{}, error) {
	if f != reflect.String || t != reflect.Slice {
		return data, nil
	}

	strData, ok := data.(string)
	if !ok {
		return nil, fmt.Errorf("expected string but got %T", data)
	}
	if strData == "" {
		return data, nil
	}

	var result []map[string]any
	if err := yaml.Unmarshal([]byte(strData), &result); err != nil {
		return data, nil
	}

	return result, nil
}

// StringToFieldsSliceHookFunc is like mapstructure.StringToSliceHookFunc() but uses strings.Fields() and filters whitespace.
func StringToFieldsSliceHookFunc(r rune) mapstructure.DecodeHookFunc {
	return func(f reflect.Kind, t reflect.Kind, data interface{}) (interface{}, error) {
		if f != reflect.String || t != reflect.Slice {
			return data, nil
		}

		raw, ok := data.(string)
		if !ok {
			return nil, fmt.Errorf("expected string but got %T", data)
		}
		if raw == "" {
			return []string{}, nil
		}

		return strings.FieldsFunc(raw, func(this rune) bool { return this == r || unicode.IsSpace(this) }), nil
	}
}

// YAMLToStringMapHookFunc returns a mapstructure.DecodeHookFunc that converts string to map[string]string by parsing YAML.
func YAMLToStringMapHookFunc(f reflect.Kind, t reflect.Kind, data interface{}) (interface{}, error) {
	if f != reflect.String || t != reflect.Map {
		return data, nil
	}

	strData, ok := data.(string)
	if !ok {
		return nil, fmt.Errorf("expected string but got %T", data)
	}
	if strData == "" {
		return map[string]string{}, nil
	}

	var result map[string]string
	if err := yaml.Unmarshal([]byte(strData), &result); err != nil {
		return data, nil
	}

	return result, nil
}

// StringToStringMapHookFunc is like StringToFieldsSliceHookFunc(). It splits map entries on ',' and then key-value pairs on '='.
func StringToStringMapHookFunc(f reflect.Kind, t reflect.Kind, data interface{}) (interface{}, error) {
	if f != reflect.String || t != reflect.Map {
		return data, nil
	}

	raw, ok := data.(string)
	if !ok {
		return nil, fmt.Errorf("expected string but got %T", data)
	}
	if raw == "" {
		return map[string]string{}, nil
	}

	fields := strings.FieldsFunc(raw, func(this rune) bool { return this == ',' || unicode.IsSpace(this) })
	result := make(map[string]string, len(fields))
	for _, kv := range fields {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) < 2 {
			return data, nil
		}
		result[parts[0]] = parts[1]
	}

	return result, nil
}
