package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/valentin-kaiser/go-core/apperror"
)

func (m *manager) getValue(key string) any {
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

	prefix := lowerKey + keySeparator
	reconstructedMap := make(map[string]any)
	for k, v := range m.values {
		if strings.HasPrefix(k, prefix) {
			suffix := k[len(prefix):]
			reconstructedMap[suffix] = v
		}
	}

	if len(reconstructedMap) > 0 {
		return reconstructedMap
	}

	if val, exists := m.defaults[lowerKey]; exists {
		return val
	}

	return nil
}

func (m *manager) unmarshal(target any) error {
	mutex.RLock()
	defer mutex.RUnlock()

	return m.unmarshalStruct(reflect.ValueOf(target), "")
}

func (m *manager) unmarshalStruct(v reflect.Value, prefix string) error {
	if v.Kind() == reflect.Pointer {
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
		yamlTag := field.Tag.Get(yamlTagName)
		if yamlTag == tagIgnore {
			continue
		}
		fieldName := getFieldName(field)
		key := buildLabel(prefix, fieldName)
		if fieldValue.Kind() == reflect.Struct {
			err := m.unmarshalStruct(fieldValue, key)
			if err != nil {
				return apperror.NewErrorf("unmarshalling struct %s failed", key).AddError(err)
			}
			continue
		}
		if fieldValue.Kind() == reflect.Pointer && fieldValue.Type().Elem().Kind() == reflect.Struct {
			if fieldValue.IsNil() {
				fieldValue.Set(reflect.New(fieldValue.Type().Elem()))
			}
			err := m.unmarshalStruct(fieldValue, key)
			if err != nil {
				return apperror.NewErrorf("unmarshalling struct %s failed", key).AddError(err)
			}
			continue
		}
		value := m.getValue(key)
		if value == nil {
			continue
		}
		err := m.setFieldValue(fieldValue, value)
		if err != nil {
			return apperror.NewErrorf("setting field %s failed", key).AddError(err)
		}
	}

	return nil
}

func (m *manager) setFieldValue(field reflect.Value, value any) error {
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
			i, err := strconv.ParseInt(str, decimalBase, bitSize64)
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
				return apperror.NewErrorf("negative integer %d cannot be converted to unsigned", i)
			}
			field.SetUint(uint64(i))
			return nil
		}
		if i, ok := value.(int64); ok {
			if i < 0 {
				return apperror.NewErrorf("negative integer %d cannot be converted to unsigned", i)
			}
			field.SetUint(uint64(i))
			return nil
		}
		if i, ok := value.(int32); ok {
			if i < 0 {
				return apperror.NewErrorf("negative integer %d cannot be converted to unsigned", i)
			}
			field.SetUint(uint64(i))
			return nil
		}
		if i, ok := value.(int16); ok {
			if i < 0 {
				return apperror.NewErrorf("negative integer %d cannot be converted to unsigned", i)
			}
			field.SetUint(uint64(i))
			return nil
		}
		if i, ok := value.(int8); ok {
			if i < 0 {
				return apperror.NewErrorf("negative integer %d cannot be converted to unsigned", i)
			}
			field.SetUint(uint64(i))
			return nil
		}
		if str, ok := value.(string); ok {
			i, err := strconv.ParseUint(str, decimalBase, bitSize64)
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
			f, err := strconv.ParseFloat(str, bitSize64)
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

func (m *manager) setSliceValue(field reflect.Value, value any) error {
	slice, ok := value.([]any)
	if !ok {
		// Typed slices need no conversion
		if reflect.TypeOf(value).AssignableTo(field.Type()) {
			field.Set(reflect.ValueOf(value))
		}
		return nil
	}

	elemType := field.Type().Elem()
	newSlice := reflect.MakeSlice(field.Type(), len(slice), len(slice))
	for i, item := range slice {
		err := m.setSliceElement(newSlice.Index(i), elemType, item)
		if err != nil {
			return apperror.NewErrorf("unmarshalling slice element %d failed", i).AddError(err)
		}
	}

	field.Set(newSlice)
	return nil
}

func (m *manager) setSliceElement(elemValue reflect.Value, elemType reflect.Type, item any) error {
	switch elemType.Kind() {
	case reflect.Struct:
		return m.unmarshalNestedStruct(elemValue, item)
	case reflect.Pointer:
		if elemType.Elem().Kind() != reflect.Struct {
			newElem := reflect.New(elemType.Elem())
			err := m.setFieldValue(newElem.Elem(), item)
			if err != nil {
				return err
			}
			elemValue.Set(newElem)
			return nil
		}
		elemValue.Set(reflect.New(elemType.Elem()))
		return m.unmarshalNestedStruct(elemValue, item)
	default:
		return m.setFieldValue(elemValue, item)
	}
}

func (m *manager) unmarshalMapToStruct(v reflect.Value, data map[string]any) error {
	if v.Kind() == reflect.Pointer {
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
		yamlTag := field.Tag.Get(yamlTagName)
		if yamlTag == tagIgnore {
			continue
		}
		fieldName := getFieldName(field)
		value, exists := data[fieldName]
		if !exists {
			value, exists = data[strings.ToLower(fieldName)]
		}
		if !exists {
			continue
		}
		err := m.unmarshalField(fieldValue, value)
		if err != nil {
			return apperror.NewErrorf("unmarshalling field %s failed", fieldName).AddError(err)
		}
	}

	return nil
}

func (m *manager) unmarshalField(fieldValue reflect.Value, value any) error {
	if fieldValue.Kind() == reflect.Struct {
		return m.unmarshalNestedStruct(fieldValue, value)
	}

	if fieldValue.Kind() == reflect.Pointer && fieldValue.Type().Elem().Kind() == reflect.Struct {
		if fieldValue.IsNil() {
			fieldValue.Set(reflect.New(fieldValue.Type().Elem()))
		}
		return m.unmarshalNestedStruct(fieldValue, value)
	}

	if fieldValue.Kind() == reflect.Slice {
		return m.setSliceValue(fieldValue, value)
	}

	return m.setFieldValue(fieldValue, value)
}

// unmarshalNestedStruct fills target from a decoded YAML or JSON object, which has string keys
// or, from some decoders, generic keys. Values of any other type are ignored.
func (m *manager) unmarshalNestedStruct(target reflect.Value, value any) error {
	switch nested := value.(type) {
	case map[string]any:
		return m.unmarshalMapToStruct(target, nested)
	case map[any]any:
		return m.unmarshalMapToStruct(target, stringKeyedMap(nested))
	default:
		return nil
	}
}

// stringKeyedMap converts a map with generic keys, as some decoders produce, to a map with string
// keys. Entries whose key is not a string are dropped.
func stringKeyedMap(generic map[any]any) map[string]any {
	stringMap := make(map[string]any)
	for k, v := range generic {
		if keyStr, ok := k.(string); ok {
			stringMap[keyStr] = v
		}
	}
	return stringMap
}

func (m *manager) setMapValue(field reflect.Value, value any) error {
	if value == nil {
		return nil
	}

	valValue := reflect.ValueOf(value)
	if valValue.Type().AssignableTo(field.Type()) {
		field.Set(valValue)
		return nil
	}

	inputMap, ok := value.(map[string]any)
	if !ok {
		if genericMap, ok := value.(map[any]any); ok {
			inputMap = make(map[string]any)
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
		err := m.setFieldValue(keyValue, k)
		if err != nil {
			return apperror.NewErrorf("setting map key %v failed", k).AddError(err)
		}
		mapValue := reflect.New(valueType).Elem()
		err = m.setMapEntryValue(mapValue, valueType, v)
		if err != nil {
			return apperror.NewErrorf("setting map value of key %v failed", k).AddError(err)
		}
		newMap.SetMapIndex(keyValue, mapValue)
	}

	field.Set(newMap)
	return nil
}

func (m *manager) setMapEntryValue(mapValue reflect.Value, valueType reflect.Type, value any) error {
	switch {
	case valueType.Kind() == reflect.Pointer:
		if mapValue.IsNil() {
			mapValue.Set(reflect.New(valueType.Elem()))
		}
		if valueType.Elem().Kind() == reflect.Struct {
			return m.setStructValue(mapValue, value)
		}
		return m.setFieldValue(mapValue.Elem(), value)
	case valueType.Kind() == reflect.Struct:
		return m.setStructValue(mapValue.Addr(), value)
	default:
		return m.setFieldValue(mapValue, value)
	}
}

func (m *manager) setStructValue(field reflect.Value, value any) error {
	if field.Kind() != reflect.Pointer {
		return apperror.NewError("setStructValue requires pointer to struct")
	}

	if field.IsNil() {
		field.Set(reflect.New(field.Type().Elem()))
	}

	structValue := field.Elem()
	if structValue.Kind() != reflect.Struct {
		return apperror.NewErrorf("expected struct, got %v", structValue.Kind())
	}

	inputMap, ok := value.(map[string]any)
	if !ok {
		if genericMap, ok := value.(map[any]any); ok {
			inputMap = make(map[string]any)
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
		fieldName := fieldInfo.Tag.Get(yamlTagName)
		if fieldName == "" || fieldName == tagIgnore {
			fieldName = strings.ToLower(fieldInfo.Name)
		} else {
			if idx := strings.Index(fieldName, tagOptionSeparator); idx != -1 {
				fieldName = fieldName[:idx]
			}
		}
		var fieldVal any
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
		switch {
		case fieldValue.Kind() == reflect.Struct:
			err := m.setStructValue(fieldValue.Addr(), fieldVal)
			if err != nil {
				return apperror.NewErrorf("setting field %s failed", fieldName).AddError(err)
			}
		case fieldValue.Kind() == reflect.Pointer && fieldValue.Type().Elem().Kind() == reflect.Struct:
			err := m.setStructValue(fieldValue, fieldVal)
			if err != nil {
				return apperror.NewErrorf("setting field %s failed", fieldName).AddError(err)
			}
		default:
			err := m.setFieldValue(fieldValue, fieldVal)
			if err != nil {
				return apperror.NewErrorf("setting field %s failed", fieldName).AddError(err)
			}
		}
	}

	return nil
}
