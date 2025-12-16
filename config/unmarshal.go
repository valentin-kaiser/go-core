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

		err := m.setFieldValue(fieldValue, value)
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *manager) setFieldValue(field reflect.Value, value interface{}) error {
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
			f, err := strconv.ParseFloat(str, 64)
			if err == nil {
				field.SetFloat(f)
			}
		}

	case reflect.Bool:
		if b, ok := value.(bool); ok {
			field.SetBool(b)
			return nil
		}
		if str, ok := value.(string); ok {
			b, err := strconv.ParseBool(str)
			if err == nil {
				field.SetBool(b)
			}
		}

	case reflect.Slice:
		return m.setSliceValue(field, value)
	case reflect.Map:
		return m.setMapValue(field, value)
	}

	return nil
}

func (m *manager) setSliceValue(field reflect.Value, value interface{}) error {
	elemType := field.Type().Elem()
	elemKind := elemType.Kind()

	// Handle []interface{} from YAML
	slice, ok := value.([]interface{})
	if !ok {
		// Try direct assignment for typed slices
		if reflect.TypeOf(value).AssignableTo(field.Type()) {
			field.Set(reflect.ValueOf(value))
			return nil
		}
		return nil
	}

	// Create a new slice of the appropriate type
	newSlice := reflect.MakeSlice(field.Type(), len(slice), len(slice))

	for i, item := range slice {
		elemValue := newSlice.Index(i)

		switch elemKind {
		case reflect.Struct:
			// Handle slice of structs
			if itemMap, ok := item.(map[interface{}]interface{}); ok {
				// Convert map[interface{}]interface{} to map[string]interface{}
				stringMap := make(map[string]interface{})
				for k, v := range itemMap {
					if keyStr, ok := k.(string); ok {
						stringMap[keyStr] = v
					}
				}
				if err := m.unmarshalMapToStruct(elemValue, stringMap); err != nil {
					return err
				}
			} else if itemMap, ok := item.(map[string]interface{}); ok {
				if err := m.unmarshalMapToStruct(elemValue, itemMap); err != nil {
					return err
				}
			}

		case reflect.Ptr:
			// Handle slice of pointers to structs
			if elemType.Elem().Kind() == reflect.Struct {
				elemValue.Set(reflect.New(elemType.Elem()))
				if itemMap, ok := item.(map[interface{}]interface{}); ok {
					stringMap := make(map[string]interface{})
					for k, v := range itemMap {
						if keyStr, ok := k.(string); ok {
							stringMap[keyStr] = v
						}
					}
					if err := m.unmarshalMapToStruct(elemValue, stringMap); err != nil {
						return err
					}
				} else if itemMap, ok := item.(map[string]interface{}); ok {
					if err := m.unmarshalMapToStruct(elemValue, itemMap); err != nil {
						return err
					}
				}
			} else {
				// Handle pointer to primitive types
				newElem := reflect.New(elemType.Elem())
				if err := m.setFieldValue(newElem.Elem(), item); err != nil {
					return err
				}
				elemValue.Set(newElem)
			}

		default:
			// Handle primitive types
			if err := m.setFieldValue(elemValue, item); err != nil {
				return err
			}
		}
	}

	field.Set(newSlice)
	return nil
}

func (m *manager) unmarshalMapToStruct(v reflect.Value, data map[string]interface{}) error {
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

		// Look for the value in the data map
		value, exists := data[fieldName]
		if !exists {
			// Try lowercase version
			value, exists = data[strings.ToLower(fieldName)]
		}
		if !exists {
			continue
		}

		if fieldValue.Kind() == reflect.Struct {
			if nestedMap, ok := value.(map[string]interface{}); ok {
				if err := m.unmarshalMapToStruct(fieldValue, nestedMap); err != nil {
					return err
				}
			} else if nestedMap, ok := value.(map[interface{}]interface{}); ok {
				stringMap := make(map[string]interface{})
				for k, v := range nestedMap {
					if keyStr, ok := k.(string); ok {
						stringMap[keyStr] = v
					}
				}
				if err := m.unmarshalMapToStruct(fieldValue, stringMap); err != nil {
					return err
				}
			}
			continue
		}

		if fieldValue.Kind() == reflect.Ptr && fieldValue.Type().Elem().Kind() == reflect.Struct {
			if fieldValue.IsNil() {
				fieldValue.Set(reflect.New(fieldValue.Type().Elem()))
			}
			if nestedMap, ok := value.(map[string]interface{}); ok {
				if err := m.unmarshalMapToStruct(fieldValue, nestedMap); err != nil {
					return err
				}
			} else if nestedMap, ok := value.(map[interface{}]interface{}); ok {
				stringMap := make(map[string]interface{})
				for k, v := range nestedMap {
					if keyStr, ok := k.(string); ok {
						stringMap[keyStr] = v
					}
				}
				if err := m.unmarshalMapToStruct(fieldValue, stringMap); err != nil {
					return err
				}
			}
			continue
		}

		if fieldValue.Kind() == reflect.Slice {
			if err := m.setSliceValue(fieldValue, value); err != nil {
				return err
			}
			continue
		}

		if err := m.setFieldValue(fieldValue, value); err != nil {
			return err
		}
	}

	return nil
}

func (m *manager) setMapValue(field reflect.Value, value interface{}) error {
	if value == nil {
		return nil
	}

	valValue := reflect.ValueOf(value)
	if valValue.Type().AssignableTo(field.Type()) {
		field.Set(valValue)
		return nil
	}

	inputMap, ok := value.(map[string]interface{})
	if !ok {
		if genericMap, ok := value.(map[interface{}]interface{}); ok {
			inputMap = make(map[string]interface{})
			for k, v := range genericMap {
				inputMap[fmt.Sprintf("%v", k)] = v
			}
		} else {
			return nil
		}
	}

	keyType := field.Type().Key()
	valueType := field.Type().Elem()

	newMap := reflect.MakeMap(field.Type())
	for k, v := range inputMap {
		keyValue := reflect.New(keyType).Elem()
		if err := m.setFieldValue(keyValue, k); err != nil {
			return err
		}

		mapValue := reflect.New(valueType).Elem()

		if valueType.Kind() == reflect.Ptr {
			if mapValue.IsNil() {
				mapValue.Set(reflect.New(valueType.Elem()))
			}
			if valueType.Elem().Kind() == reflect.Struct {
				if err := m.setStructValue(mapValue, v); err != nil {
					return err
				}
			} else {
				if err := m.setFieldValue(mapValue.Elem(), v); err != nil {
					return err
				}
			}
		} else if valueType.Kind() == reflect.Struct {
			if err := m.setStructValue(mapValue.Addr(), v); err != nil {
				return err
			}
		} else {
			if err := m.setFieldValue(mapValue, v); err != nil {
				return err
			}
		}

		newMap.SetMapIndex(keyValue, mapValue)
	}

	field.Set(newMap)
	return nil
}

func (m *manager) setStructValue(field reflect.Value, value interface{}) error {
	if field.Kind() != reflect.Ptr {
		return fmt.Errorf("setStructValue requires pointer to struct")
	}

	if field.IsNil() {
		field.Set(reflect.New(field.Type().Elem()))
	}

	structValue := field.Elem()
	if structValue.Kind() != reflect.Struct {
		return fmt.Errorf("expected struct, got %v", structValue.Kind())
	}

	inputMap, ok := value.(map[string]interface{})
	if !ok {
		if genericMap, ok := value.(map[interface{}]interface{}); ok {
			inputMap = make(map[string]interface{})
			for k, v := range genericMap {
				inputMap[fmt.Sprintf("%v", k)] = v
			}
		} else {
			return nil
		}
	}

	structType := structValue.Type()
	for i := 0; i < structType.NumField(); i++ {
		fieldInfo := structType.Field(i)
		fieldValue := structValue.Field(i)

		if !fieldValue.CanSet() {
			continue
		}

		fieldName := fieldInfo.Tag.Get("yaml")
		if fieldName == "" || fieldName == "-" {
			fieldName = strings.ToLower(fieldInfo.Name)
		} else {
			if idx := strings.Index(fieldName, ","); idx != -1 {
				fieldName = fieldName[:idx]
			}
		}

		var fieldVal interface{}
		var found bool
		for k, v := range inputMap {
			if strings.EqualFold(k, fieldName) {
				fieldVal = v
				found = true
				break
			}
		}

		if !found {
			continue
		}

		if fieldValue.Kind() == reflect.Struct {
			if err := m.setStructValue(fieldValue.Addr(), fieldVal); err != nil {
				return err
			}
		} else if fieldValue.Kind() == reflect.Ptr && fieldValue.Type().Elem().Kind() == reflect.Struct {
			if err := m.setStructValue(fieldValue, fieldVal); err != nil {
				return err
			}
		} else {
			if err := m.setFieldValue(fieldValue, fieldVal); err != nil {
				return err
			}
		}
	}

	return nil
}
