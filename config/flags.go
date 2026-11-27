package config

import (
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/pflag"
	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/flag"
)

const (
	defaultName    = "config"
	fileSourceName = "file"

	configFileExtension = ".yaml"
	configDirMode       = 0o750
	configFileMode      = 0o600
	reloadDebounce      = 100 * time.Millisecond

	keySeparator       = "."
	dashSeparator      = "-"
	envKeySeparator    = "_"
	listSeparator      = ","
	tagOptionSeparator = ","
	tagIgnore          = "-"
	yamlTagName        = "yaml"
	usageTagName       = "usage"

	flagTypeString      = "string"
	flagTypeInt         = "int"
	flagTypeInt8        = "int8"
	flagTypeInt16       = "int16"
	flagTypeInt32       = "int32"
	flagTypeInt64       = "int64"
	flagTypeUint        = "uint"
	flagTypeUint8       = "uint8"
	flagTypeUint16      = "uint16"
	flagTypeUint32      = "uint32"
	flagTypeUint64      = "uint64"
	flagTypeFloat32     = "float32"
	flagTypeFloat64     = "float64"
	flagTypeBool        = "bool"
	flagTypeStringArray = "stringArray"
	flagTypeStringSlice = "stringSlice"

	decimalBase   = 10
	bitSizeNative = 0
	bitSize8      = 8
	bitSize16     = 16
	bitSize32     = 32
	bitSize64     = 64
)

func (m *manager) setDefault(key string, value any) {
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
	key = strings.ReplaceAll(key, keySeparator, envKeySeparator)
	key = strings.ReplaceAll(key, dashSeparator, envKeySeparator)
	key = strings.ToUpper(key)

	if m.prefix == "" {
		return key
	}
	return m.prefix + envKeySeparator + key
}

func (m *manager) getFlagValue(flag *pflag.Flag) any {
	switch flag.Value.Type() {
	case flagTypeString:
		return flag.Value.String()
	case flagTypeInt:
		val, err := strconv.Atoi(flag.Value.String())
		if err != nil {
			return flag.Value.String()
		}
		return val
	case flagTypeInt8:
		val, err := strconv.ParseInt(flag.Value.String(), decimalBase, bitSize8)
		if err != nil {
			return flag.Value.String()
		}
		return int8(val)
	case flagTypeInt16:
		val, err := strconv.ParseInt(flag.Value.String(), decimalBase, bitSize16)
		if err != nil {
			return flag.Value.String()
		}
		return int16(val)
	case flagTypeInt32:
		val, err := strconv.ParseInt(flag.Value.String(), decimalBase, bitSize32)
		if err != nil {
			return flag.Value.String()
		}
		return int32(val)
	case flagTypeInt64:
		val, err := strconv.ParseInt(flag.Value.String(), decimalBase, bitSize64)
		if err != nil {
			return flag.Value.String()
		}
		return val
	case flagTypeUint:
		val, err := strconv.ParseUint(flag.Value.String(), decimalBase, bitSizeNative)
		if err != nil {
			return flag.Value.String()
		}
		return uint(val)
	case flagTypeUint8:
		val, err := strconv.ParseUint(flag.Value.String(), decimalBase, bitSize8)
		if err != nil {
			return flag.Value.String()
		}
		return uint8(val)
	case flagTypeUint16:
		val, err := strconv.ParseUint(flag.Value.String(), decimalBase, bitSize16)
		if err != nil {
			return flag.Value.String()
		}
		return uint16(val)
	case flagTypeUint32:
		val, err := strconv.ParseUint(flag.Value.String(), decimalBase, bitSize32)
		if err != nil {
			return flag.Value.String()
		}
		return uint32(val)
	case flagTypeUint64:
		val, err := strconv.ParseUint(flag.Value.String(), decimalBase, bitSize64)
		if err != nil {
			return flag.Value.String()
		}
		return val
	case flagTypeFloat32:
		val, err := strconv.ParseFloat(flag.Value.String(), bitSize32)
		if err != nil {
			return flag.Value.String()
		}
		return float32(val)
	case flagTypeFloat64:
		val, err := strconv.ParseFloat(flag.Value.String(), bitSize64)
		if err != nil {
			return flag.Value.String()
		}
		return val
	case flagTypeBool:
		val, err := strconv.ParseBool(flag.Value.String())
		if err != nil {
			return flag.Value.String()
		}
		return val
	case flagTypeStringArray, flagTypeStringSlice:
		return strings.Split(flag.Value.String(), listSeparator)
	}
	return flag.Value.String()
}

// declareFlag declares a flag with the given label, usage and default value
// It also binds the flag to the configuration
func (m *manager) declareFlag(label string, usage string, defaultValue any) error {
	m.setDefault(label, defaultValue)
	pflagLabel := kebabCase(label)
	label = strings.ToLower(label)
	flag.RegisterEnvVar(pflagLabel, label)

	if pflag.Lookup(pflagLabel) != nil {
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
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}

	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).PkgPath != "" {
			continue
		}
		field := t.Field(i)
		fieldName := getFieldName(field)
		if v.Field(i).Kind() == reflect.Pointer {
			if v.Field(i).IsNil() {
				v.Field(i).Set(reflect.New(field.Type.Elem()))
			}
			err := m.parseStructTags(v.Field(i).Elem(), fieldName)
			if err != nil {
				return apperror.NewErrorf("parsing struct tags of field %s failed", fieldName).AddError(err)
			}
			continue
		}
		if field.Type.Kind() == reflect.Struct {
			subv := v.Field(i)
			if subv.Kind() == reflect.Pointer {
				subv = subv.Elem()
			}
			label := buildLabel(labelBase, fieldName)
			err := m.parseStructTags(subv, label)
			if err != nil {
				return apperror.NewErrorf("parsing struct tags of field %s failed", fieldName).AddError(err)
			}
			continue
		}
		tag := buildLabel(labelBase, fieldName)
		err := m.declareFlag(tag, field.Tag.Get(usageTagName), v.Field(i).Interface())
		if err != nil {
			return apperror.NewErrorf("declaring flag %s failed", tag).AddError(err)
		}
	}

	return nil
}

// kebabCase converts a string to kebab-case (dash-separated lowercase)
// Example: "ApplicationName" -> "application-name"
// Handles acronyms properly: "HTTPPort" -> "http-port", "ARCHost" -> "arc-host"
// Handles dot-separated paths: "Server.ARCHost" -> "server.arc-host"
func kebabCase(s string) string {
	if s == "" {
		return s
	}

	if strings.Contains(s, keySeparator) {
		parts := strings.Split(s, keySeparator)
		for i, part := range parts {
			parts[i] = kebabCase(part)
		}
		return strings.Join(parts, keySeparator)
	}

	runes := []rune(s)
	var result strings.Builder

	for i, r := range runes {
		if !unicode.IsUpper(r) {
			result.WriteRune(r)
			continue
		}
		if startsWord(runes, i) {
			result.WriteString(dashSeparator)
		}
		result.WriteRune(unicode.ToLower(r))
	}

	return result.String()
}

// startsWord reports whether the uppercase rune at index i begins a new word: it follows a
// lowercase rune, or it is the last uppercase rune of an acronym that precedes a lowercase one.
func startsWord(runes []rune, i int) bool {
	if i == 0 {
		return false
	}
	prevIsLower := unicode.IsLower(runes[i-1])
	nextIsLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
	return prevIsLower || nextIsLower
}

// getFieldName extracts the field name from struct field, preferring yaml tag over field name
func getFieldName(field reflect.StructField) string {
	yamlTag := field.Tag.Get(yamlTagName)
	if yamlTag == "" || yamlTag == tagIgnore {
		return field.Name
	}

	if before, _, ok := strings.Cut(yamlTag, tagOptionSeparator); ok {
		return before
	}
	return yamlTag
}

// buildLabel constructs a dot-separated label from base and field name
func buildLabel(base, fieldName string) string {
	if base == "" {
		return fieldName
	}
	return base + keySeparator + fieldName
}
