package config

import (
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/pflag"
	"github.com/valentin-kaiser/go-core/apperror"
)

func (m *manager) setDefault(key string, value interface{}) {
	mutex.Lock()
	defer mutex.Unlock()
	lowerKey := strings.ToLower(key)
	m.defaults[lowerKey] = value
}

func (m *manager) bind(key string, flag *pflag.Flag) error {
	mutex.Lock()
	defer mutex.Unlock()
	m.flags[strings.ToLower(key)] = flag
	return nil
}

func (m *manager) getFlagKey(key string) string {
	key = strings.ReplaceAll(key, ".", "_")
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ToUpper(key)

	if m.prefix == "" {
		return key
	}
	return m.prefix + "_" + key
}

func (m *manager) getFlagValue(flag *pflag.Flag) interface{} {
	switch flag.Value.Type() {
	case "string":
		return flag.Value.String()
	case "int":
		val, err := strconv.Atoi(flag.Value.String())
		if err != nil {
			return flag.Value.String()
		}
		return val
	case "int8":
		val, err := strconv.ParseInt(flag.Value.String(), 10, 8)
		if err != nil {
			return flag.Value.String()
		}
		return int8(val)
	case "int16":
		val, err := strconv.ParseInt(flag.Value.String(), 10, 16)
		if err != nil {
			return flag.Value.String()
		}
		return int16(val)
	case "int32":
		val, err := strconv.ParseInt(flag.Value.String(), 10, 32)
		if err != nil {
			return flag.Value.String()
		}
		return int32(val)
	case "int64":
		val, err := strconv.ParseInt(flag.Value.String(), 10, 64)
		if err != nil {
			return flag.Value.String()
		}
		return val
	case "uint":
		val, err := strconv.ParseUint(flag.Value.String(), 10, 0)
		if err != nil {
			return flag.Value.String()
		}
		return uint(val)
	case "uint8":
		val, err := strconv.ParseUint(flag.Value.String(), 10, 8)
		if err != nil {
			return flag.Value.String()
		}
		return uint8(val)
	case "uint16":
		val, err := strconv.ParseUint(flag.Value.String(), 10, 16)
		if err != nil {
			return flag.Value.String()
		}
		return uint16(val)
	case "uint32":
		val, err := strconv.ParseUint(flag.Value.String(), 10, 32)
		if err != nil {
			return flag.Value.String()
		}
		return uint32(val)
	case "uint64":
		val, err := strconv.ParseUint(flag.Value.String(), 10, 64)
		if err != nil {
			return flag.Value.String()
		}
		return val
	case "float32":
		val, err := strconv.ParseFloat(flag.Value.String(), 32)
		if err != nil {
			return flag.Value.String()
		}
		return float32(val)
	case "float64":
		val, err := strconv.ParseFloat(flag.Value.String(), 64)
		if err != nil {
			return flag.Value.String()
		}
		return val
	case "bool":
		val, err := strconv.ParseBool(flag.Value.String())
		if err != nil {
			return flag.Value.String()
		}
		return val
	case "stringArray", "stringSlice":
		return strings.Split(flag.Value.String(), ",")
	}
	return flag.Value.String()
}

// declareFlag declares a flag with the given label, usage and default value
// It also binds the flag to the configuration
func (m *manager) declareFlag(label string, usage string, defaultValue interface{}) error {
	m.setDefault(label, defaultValue)
	pflagLabel := kebabCase(label)
	label = strings.ToLower(label)

	// Check if flag already exists to avoid redefinition errors
	if pflag.Lookup(pflagLabel) != nil {
		// Flag already exists, just bind to config
		return m.bind(label, pflag.Lookup(pflagLabel))
	}

	switch v := defaultValue.(type) {
	case string:
		pflag.String(pflagLabel, v, usage)
	case int:
		pflag.Int(pflagLabel, v, usage)
	case uint:
		pflag.Uint(pflagLabel, v, usage)
	case int8:
		pflag.Int8(pflagLabel, v, usage)
	case uint8:
		pflag.Uint8(pflagLabel, v, usage)
	case int16:
		pflag.Int16(pflagLabel, v, usage)
	case uint16:
		pflag.Uint16(pflagLabel, v, usage)
	case int32:
		pflag.Int32(pflagLabel, v, usage)
	case uint32:
		pflag.Uint32(pflagLabel, v, usage)
	case int64:
		pflag.Int64(pflagLabel, v, usage)
	case uint64:
		pflag.Uint64(pflagLabel, v, usage)
	case float32:
		pflag.Float32(pflagLabel, v, usage)
	case float64:
		pflag.Float64(pflagLabel, v, usage)
	case bool:
		pflag.Bool(pflagLabel, v, usage)
	case []string:
		pflag.StringArray(pflagLabel, v, usage)
	default:
		return nil
	}

	return m.bind(label, pflag.Lookup(pflagLabel))
}

// parseStructTags parses the struct tags of the given struct and registers the flags
// It also sets the default values of the flags to the values of the struct fields
func (m *manager) parseStructTags(v reflect.Value, labelBase string) error {
	// If the config is a pointer, we need to get the type of the element
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		// If the field is not exported, we skip it
		if t.Field(i).PkgPath != "" {
			continue
		}

		field := t.Field(i)
		fieldName := getFieldName(field)

		// If the field is a pointer, we need to dereference it
		if v.Field(i).Kind() == reflect.Ptr {
			if v.Field(i).IsNil() {
				v.Field(i).Set(reflect.New(field.Type.Elem()))
			}

			err := m.parseStructTags(v.Field(i).Elem(), fieldName)
			if err != nil {
				return apperror.Wrap(err)
			}
			continue
		}

		// If the field is a struct, we need to iterate over its fields
		if field.Type.Kind() == reflect.Struct {
			subv := v.Field(i)
			if subv.Kind() == reflect.Ptr {
				subv = subv.Elem()
			}

			label := buildLabel(labelBase, fieldName)
			err := m.parseStructTags(subv, label)
			if err != nil {
				return apperror.Wrap(err)
			}
			continue
		}

		tag := buildLabel(labelBase, fieldName)
		err := m.declareFlag(tag, field.Tag.Get("usage"), v.Field(i).Interface())
		if err != nil {
			return apperror.Wrap(err)
		}
	}

	return nil
}

// kebabCase converts a string to kebab-case (dash-separated lowercase)
// Example: "ApplicationName" -> "application-name"
func kebabCase(s string) string {
	if s == "" {
		return s
	}

	var result strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				result.WriteByte('-')
			}
			result.WriteRune(unicode.ToLower(r))
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}

// getFieldName extracts the field name from struct field, preferring yaml tag over field name
func getFieldName(field reflect.StructField) string {
	yamlTag := field.Tag.Get("yaml")
	if yamlTag == "" || yamlTag == "-" {
		return field.Name
	}

	if idx := strings.Index(yamlTag, ","); idx != -1 {
		return yamlTag[:idx]
	}
	return yamlTag
}

// buildLabel constructs a dot-separated label from base and field name
func buildLabel(base, fieldName string) string {
	if base == "" {
		return fieldName
	}
	return base + "." + fieldName
}
