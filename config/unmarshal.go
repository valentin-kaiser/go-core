package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
)

func (m *manager) getValue(key string) interface{} {
	mutex.RLock()
	defer mutex.RUnlock()

	lowerKey := strings.ToLower(key)
	if flag, exists := m.flags[lowerKey]; exists && flag.Changed {
		return m.getFlagValue(flag)
	}

	envKey := m.getFlagKey(key)
	if envVal := os.Getenv(envKey); envVal != "" {
		return envVal
	}

	if val, exists := m.values[lowerKey]; exists {
		return val
	}

	if val, exists := m.defaults[lowerKey]; exists {
		return val
	}

	return nil
}

func (m *manager) unmarshal(target interface{}) error {
	mutex.RLock()
	defer mutex.RUnlock()

	return m.unmarshalStruct(reflect.ValueOf(target), "")
}

func (m *manager) unmarshalStruct(v reflect.Value, prefix string) error {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return nil
	}

	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldValue := v.Field(i)

		if !fieldValue.CanSet() {
			continue
		}

		yamlTag := field.Tag.Get("yaml")
		if yamlTag == "-" {
			continue
		}

		fieldName := getFieldName(field)
		key := buildLabel(prefix, fieldName)

		if fieldValue.Kind() == reflect.Struct {
			err := m.unmarshalStruct(fieldValue, key)
			if err != nil {
				return err
			}
			continue
		}

		if fieldValue.Kind() == reflect.Ptr && fieldValue.Type().Elem().Kind() == reflect.Struct {
			if fieldValue.IsNil() {
				fieldValue.Set(reflect.New(fieldValue.Type().Elem()))
			}
			err := m.unmarshalStruct(fieldValue, key)
			if err != nil {
				return err
			}
			continue
		}

		value := m.getValue(key)
		if value == nil {
			continue
		}

		err := setFieldValue(fieldValue, value)
		if err != nil {
			return err
		}
	}

	return nil
}

func setFieldValue(field reflect.Value, value interface{}) error {
	if !field.CanSet() {
		return nil
	}

	switch field.Kind() {
	case reflect.String:
		if str, ok := value.(string); ok {
			field.SetString(str)
			return nil
		}
		field.SetString(fmt.Sprintf("%v", value))

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if i, ok := value.(int); ok {
			field.SetInt(int64(i))
			return nil
		}
		if i, ok := value.(int64); ok {
			field.SetInt(i)
			return nil
		}
		if str, ok := value.(string); ok {
			i, err := strconv.ParseInt(str, 10, 64)
			if err == nil {
				field.SetInt(i)
			}
		}

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if i, ok := value.(uint); ok {
			field.SetUint(uint64(i))
			return nil
		}
		if i, ok := value.(uint64); ok {
			field.SetUint(i)
			return nil
		}
		if i, ok := value.(uint32); ok {
			field.SetUint(uint64(i))
			return nil
		}
		if i, ok := value.(uint16); ok {
			field.SetUint(uint64(i))
			return nil
		}
		if i, ok := value.(uint8); ok {
			field.SetUint(uint64(i))
			return nil
		}
		// Handle signed integers from YAML (convert to unsigned if non-negative)
		if i, ok := value.(int); ok {
			if i < 0 {
				return fmt.Errorf("negative integer %d cannot be converted to unsigned", i)
			}
			field.SetUint(uint64(i))
			return nil
		}
		if i, ok := value.(int64); ok {
			if i < 0 {
				return fmt.Errorf("negative integer %d cannot be converted to unsigned", i)
			}
			field.SetUint(uint64(i))
			return nil
		}
		if i, ok := value.(int32); ok {
			if i < 0 {
				return fmt.Errorf("negative integer %d cannot be converted to unsigned", i)
			}
			field.SetUint(uint64(i))
			return nil
		}
		if i, ok := value.(int16); ok {
			if i < 0 {
				return fmt.Errorf("negative integer %d cannot be converted to unsigned", i)
			}
			field.SetUint(uint64(i))
			return nil
		}
		if i, ok := value.(int8); ok {
			if i < 0 {
				return fmt.Errorf("negative integer %d cannot be converted to unsigned", i)
			}
			field.SetUint(uint64(i))
			return nil
		}
		if str, ok := value.(string); ok {
			i, err := strconv.ParseUint(str, 10, 64)
			if err == nil {
				field.SetUint(i)
			}
		}

	case reflect.Float32, reflect.Float64:
		if f, ok := value.(float64); ok {
			field.SetFloat(f)
			return nil
		}
		if f, ok := value.(float32); ok {
			field.SetFloat(float64(f))
			return nil
		}
		if i, ok := value.(int); ok {
			field.SetFloat(float64(i))
			return nil
		}
		if i, ok := value.(int64); ok {
			field.SetFloat(float64(i))
			return nil
		}
		if i, ok := value.(int32); ok {
			field.SetFloat(float64(i))
			return nil
		}
		if i, ok := value.(int16); ok {
			field.SetFloat(float64(i))
			return nil
		}
		if i, ok := value.(int8); ok {
			field.SetFloat(float64(i))
			return nil
		}
		if i, ok := value.(uint); ok {
			field.SetFloat(float64(i))
			return nil
		}
		if i, ok := value.(uint64); ok {
			field.SetFloat(float64(i))
			return nil
		}
		if i, ok := value.(uint32); ok {
			field.SetFloat(float64(i))
			return nil
		}
		if i, ok := value.(uint16); ok {
			field.SetFloat(float64(i))
			return nil
		}
		if i, ok := value.(uint8); ok {
			field.SetFloat(float64(i))
			return nil
		}
		if str, ok := value.(string); ok {
			if f, err := strconv.ParseFloat(str, 64); err == nil {
				field.SetFloat(f)
			}
		}

	case reflect.Bool:
		if b, ok := value.(bool); ok {
			field.SetBool(b)
			return nil
		}
		if str, ok := value.(string); ok {
			if b, err := strconv.ParseBool(str); err == nil {
				field.SetBool(b)
			}
		}

	case reflect.Slice:
		if field.Type().Elem().Kind() != reflect.String {
			return nil
		}
		if slice, ok := value.([]string); ok {
			field.Set(reflect.ValueOf(slice))
			return nil
		}
		if slice, ok := value.([]interface{}); ok {
			strSlice := make([]string, len(slice))
			for i, v := range slice {
				strSlice[i] = fmt.Sprintf("%v", v)
			}
			field.Set(reflect.ValueOf(strSlice))
		}
	}

	return nil
}
