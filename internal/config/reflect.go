package config

import (
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
)

// applyDefaults reads the "default" struct tag for each exported field and sets
// it only if the field is still at its zero-value. This runs before YAML loading
// so that YAML values always take precedence over defaults.
func applyDefaults(v interface{}) {
	processFields(v, func(field reflect.Value, tag reflect.StructTag) {
		defaultVal := tag.Get("default")
		if defaultVal == "" {
			return
		}
		if field.IsZero() {
			setValueFromString(field, defaultVal)
		}
	})
}

// applyEnvVars reads the "env" struct tag for each exported field and overrides
// the field value if that environment variable is set. Env vars always win over
// both defaults and YAML config.
func applyEnvVars(v interface{}) {
	processFields(v, func(field reflect.Value, tag reflect.StructTag) {
		envKey := tag.Get("env")
		if envKey == "" {
			return
		}
		if envVal := os.Getenv(envKey); envVal != "" {
			setValueFromString(field, envVal)
		}
	})
}

// processFields recursively iterates over exported struct fields, skipping
// unexported fields and sync.RWMutex. For nested structs it recursures;
// for leaf fields it calls fn.
func processFields(v interface{}, fn func(reflect.Value, reflect.StructTag)) {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return
	}

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		typeField := val.Type().Field(i)

		// Skip unexported fields
		if !typeField.IsExported() {
			continue
		}

		// Skip sync.RWMutex and other internal fields
		typeName := typeField.Type.String()
		if strings.HasPrefix(typeName, "sync.") {
			continue
		}

		// Recurse into nested structs (but not time.Time etc.)
		if field.Kind() == reflect.Struct && !isPassthroughType(typeField.Type) {
			if field.CanAddr() {
				processFields(field.Addr().Interface(), fn)
			}
			continue
		}

		if field.CanSet() {
			fn(field, typeField.Tag)
		}
	}
}

// isPassthroughType returns true for struct types that should be treated as
// leaf values rather than recursed into (e.g., time.Time).
func isPassthroughType(t reflect.Type) bool {
	// Add any struct types here that should not be recursed into.
	switch t.PkgPath() {
	case "time":
		return true
	}
	return false
}

// setValueFromString coerces a string value (from a default tag or env var)
// into the appropriate Go type and sets the field.
func setValueFromString(field reflect.Value, valStr string) {
	switch field.Kind() {
	case reflect.String:
		field.SetString(valStr)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if i, err := strconv.ParseInt(valStr, 10, 64); err == nil {
			field.SetInt(i)
		}

	case reflect.Float32, reflect.Float64:
		if f, err := strconv.ParseFloat(valStr, 64); err == nil {
			field.SetFloat(f)
		}

	case reflect.Bool:
		if b, err := strconv.ParseBool(valStr); err == nil {
			field.SetBool(b)
		}

	case reflect.Slice:
		if field.Type().Elem().Kind() == reflect.String {
			// Default tags use JSON array syntax: ["*"] or []
			if strings.HasPrefix(valStr, "[") && strings.HasSuffix(valStr, "]") {
				var slice []string
				if err := json.Unmarshal([]byte(valStr), &slice); err == nil {
					field.Set(reflect.ValueOf(slice))
				}
			} else if valStr != "" {
				// Env vars use comma-separated format
				parts := strings.Split(valStr, ",")
				slice := make([]string, 0, len(parts))
				for _, p := range parts {
					if p = strings.TrimSpace(p); p != "" {
						slice = append(slice, p)
					}
				}
				field.Set(reflect.ValueOf(slice))
			}
		}
	}
}
