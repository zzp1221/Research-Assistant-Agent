package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// FieldError is one actionable schema violation.
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (e FieldError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

// ValidateArguments validates a decoded JSON object against a JSON Schema.
// It supports the subset the tool ecosystem uses (object/array/string/number/
// integer/boolean/null, properties, required, additionalProperties, enum,
// const, numeric bounds, string length/pattern, array items/bounds).
func ValidateArguments(schemaJSON string, value any) []FieldError {
	trimmed := strings.TrimSpace(schemaJSON)
	if trimmed == "" {
		return nil
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(trimmed), &schema); err != nil {
		return []FieldError{{Message: "tool input schema is not valid JSON"}}
	}
	var errors []FieldError
	validateValue(schema, value, "", &errors)
	return errors
}

func validateValue(schema map[string]any, value any, path string, errors *[]FieldError) {
	if schema == nil {
		return
	}
	if raw, ok := schema["type"]; ok {
		types := schemaTypes(raw)
		if len(types) > 0 && !matchesAnyType(types, value) {
			*errors = append(*errors, FieldError{Path: path, Message: fmt.Sprintf("expected %s", strings.Join(types, " or "))})
			return
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		matched := false
		for _, candidate := range enum {
			if jsonEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			*errors = append(*errors, FieldError{Path: path, Message: "value is not one of the allowed enum values"})
		}
	}
	if constant, ok := schema["const"]; ok && !jsonEqual(constant, value) {
		*errors = append(*errors, FieldError{Path: path, Message: "value does not match const"})
	}

	switch typed := value.(type) {
	case map[string]any:
		validateObject(schema, typed, path, errors)
	case []any:
		validateArray(schema, typed, path, errors)
	case string:
		validateString(schema, typed, path, errors)
	case float64:
		validateNumber(schema, typed, path, errors)
	case int:
		validateNumber(schema, float64(typed), path, errors)
	case int64:
		validateNumber(schema, float64(typed), path, errors)
	case json.Number:
		if parsed, err := typed.Float64(); err == nil {
			validateNumber(schema, parsed, path, errors)
		}
	}
}

func validateObject(schema map[string]any, value map[string]any, path string, errors *[]FieldError) {
	if required, ok := schema["required"].([]any); ok {
		for _, raw := range required {
			name, _ := raw.(string)
			if name == "" {
				continue
			}
			if _, exists := value[name]; !exists {
				*errors = append(*errors, FieldError{Path: joinPath(path, name), Message: "is required"})
			}
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	for name, propertySchema := range properties {
		property, exists := value[name]
		if !exists {
			continue
		}
		if typed, ok := propertySchema.(map[string]any); ok {
			validateValue(typed, property, joinPath(path, name), errors)
		}
	}
	if additional, ok := schema["additionalProperties"]; ok {
		switch typed := additional.(type) {
		case bool:
			if !typed {
				names := make([]string, 0, len(value))
				for name := range value {
					if _, defined := properties[name]; !defined {
						names = append(names, name)
					}
				}
				sort.Strings(names)
				for _, name := range names {
					*errors = append(*errors, FieldError{Path: joinPath(path, name), Message: "is not an allowed property"})
				}
			}
		case map[string]any:
			for name, property := range value {
				if _, defined := properties[name]; defined {
					continue
				}
				validateValue(typed, property, joinPath(path, name), errors)
			}
		}
	}
}

func validateArray(schema map[string]any, value []any, path string, errors *[]FieldError) {
	if items, ok := schema["items"].(map[string]any); ok {
		for index, item := range value {
			validateValue(items, item, fmt.Sprintf("%s[%d]", path, index), errors)
		}
	}
	if minItems, ok := numberField(schema, "minItems"); ok && float64(len(value)) < minItems {
		*errors = append(*errors, FieldError{Path: path, Message: fmt.Sprintf("must contain at least %d items", int(minItems))})
	}
	if maxItems, ok := numberField(schema, "maxItems"); ok && float64(len(value)) > maxItems {
		*errors = append(*errors, FieldError{Path: path, Message: fmt.Sprintf("must contain at most %d items", int(maxItems))})
	}
}

func validateString(schema map[string]any, value string, path string, errors *[]FieldError) {
	if minLength, ok := numberField(schema, "minLength"); ok && float64(len([]rune(value))) < minLength {
		*errors = append(*errors, FieldError{Path: path, Message: fmt.Sprintf("must be at least %d characters", int(minLength))})
	}
	if maxLength, ok := numberField(schema, "maxLength"); ok && float64(len([]rune(value))) > maxLength {
		*errors = append(*errors, FieldError{Path: path, Message: fmt.Sprintf("must be at most %d characters", int(maxLength))})
	}
	if pattern, ok := schema["pattern"].(string); ok && pattern != "" {
		compiled, err := regexp.Compile(pattern)
		if err == nil && !compiled.MatchString(value) {
			*errors = append(*errors, FieldError{Path: path, Message: "does not match the required pattern"})
		}
	}
}

func validateNumber(schema map[string]any, value float64, path string, errors *[]FieldError) {
	if minimum, ok := numberField(schema, "minimum"); ok && value < minimum {
		*errors = append(*errors, FieldError{Path: path, Message: fmt.Sprintf("must be >= %v", minimum)})
	}
	if maximum, ok := numberField(schema, "maximum"); ok && value > maximum {
		*errors = append(*errors, FieldError{Path: path, Message: fmt.Sprintf("must be <= %v", maximum)})
	}
	if exclusive, ok := numberField(schema, "exclusiveMinimum"); ok && value <= exclusive {
		*errors = append(*errors, FieldError{Path: path, Message: fmt.Sprintf("must be > %v", exclusive)})
	}
	if exclusive, ok := numberField(schema, "exclusiveMaximum"); ok && value >= exclusive {
		*errors = append(*errors, FieldError{Path: path, Message: fmt.Sprintf("must be < %v", exclusive)})
	}
	if multiple, ok := numberField(schema, "multipleOf"); ok && multiple > 0 {
		quotient := value / multiple
		if math.Abs(quotient-math.Round(quotient)) > 1e-9 {
			*errors = append(*errors, FieldError{Path: path, Message: fmt.Sprintf("must be a multiple of %v", multiple)})
		}
	}
}

func schemaTypes(raw any) []string {
	switch typed := raw.(type) {
	case string:
		return []string{typed}
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if name, ok := item.(string); ok {
				out = append(out, name)
			}
		}
		return out
	default:
		return nil
	}
}

func matchesAnyType(types []string, value any) bool {
	for _, expected := range types {
		switch expected {
		case "object":
			if _, ok := value.(map[string]any); ok {
				return true
			}
		case "array":
			if _, ok := value.([]any); ok {
				return true
			}
		case "string":
			if _, ok := value.(string); ok {
				return true
			}
		case "boolean":
			if _, ok := value.(bool); ok {
				return true
			}
		case "number":
			switch value.(type) {
			case float64, int, int64, json.Number:
				return true
			}
		case "integer":
			switch typed := value.(type) {
			case int, int64:
				return true
			case float64:
				return typed == math.Trunc(typed)
			case json.Number:
				parsed, err := typed.Float64()
				return err == nil && parsed == math.Trunc(parsed)
			}
		case "null":
			if value == nil {
				return true
			}
		}
	}
	return false
}

func numberField(schema map[string]any, key string) (float64, bool) {
	raw, ok := schema[key]
	if !ok {
		return 0, false
	}
	switch typed := raw.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func jsonEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return reflect.DeepEqual(left, right)
	}
	return string(leftJSON) == string(rightJSON)
}
