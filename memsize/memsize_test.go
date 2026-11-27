package memsize_test

import (
	"testing"

	"github.com/valentin-kaiser/go-core/memsize"
)

func TestSizeOf(t *testing.T) {
	testCases := []struct {
		name     string
		value    any
		expected int64
		hasError bool
	}{
		{name: "nil value", value: nil, expected: 0, hasError: false},
		{name: "string", value: "hello", expected: 9, hasError: false},
		{name: "int", value: 42, expected: 4, hasError: false},
		{name: "bool", value: true, expected: 4, hasError: false},
		{name: "slice", value: []int{1, 2, 3}, expected: 20, hasError: false},
		{name: "map", value: map[string]int{"a": 1, "b": 2}, expected: 26, hasError: false},
		{name: "struct", value: struct{ Name string }{Name: "test"}, expected: 32, hasError: false},
		{name: "nil pointer", value: (*int)(nil), expected: 0, hasError: false},
		{name: "nil slice", value: []int(nil), expected: 0, hasError: false},
		{name: "nil map", value: map[string]int(nil), expected: 0, hasError: false},
		{name: "nil channel", value: (chan int)(nil), expected: 0, hasError: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			size, err := memsize.SizeOf(tc.value)
			if tc.hasError {
				if err == nil {
					t.Errorf("Expected error for %s, but got none", tc.name)
				}
				return
			}
			if err != nil {
				t.Errorf("Unexpected error for %s: %v", tc.name, err)
				return
			}
			if size != tc.expected {
				t.Errorf("SizeOf(%s) = %d, expected %d", tc.name, size, tc.expected)
			}
		})
	}
}

func TestSizeOfComplexStructure(t *testing.T) {
	type ComplexStruct struct {
		Name     string
		Age      int
		Tags     []string
		Metadata map[string]any
		Nested   struct {
			Value string
		}
	}

	value := ComplexStruct{
		Name: "test",
		Age:  30,
		Tags: []string{"tag1", "tag2"},
		Metadata: map[string]any{
			"key1": "value1",
			"key2": 123,
		},
		Nested: struct {
			Value string
		}{Value: "nested"},
	}

	size, err := memsize.SizeOf(value)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if size <= 0 {
		t.Errorf("Expected positive size, got %d", size)
	}
}

func TestToByteString(t *testing.T) {
	testCases := []struct {
		bytes    int64
		expected string
	}{
		{bytes: 0, expected: "0 B"},
		{bytes: 1, expected: "1 B"},
		{bytes: 999, expected: "999 B"},
		{bytes: 1000, expected: "1.0 kB"},
		{bytes: 1024, expected: "1.0 kB"},
		{bytes: 1500, expected: "1.5 kB"},
		{bytes: 1000000, expected: "1.0 MB"},
		{bytes: 1024000, expected: "1.0 MB"},
		{bytes: 1500000, expected: "1.5 MB"},
		{bytes: 1000000000, expected: "1.0 GB"},
		{bytes: 1500000000, expected: "1.5 GB"},
		{bytes: 1000000000000, expected: "1.0 TB"},
		{bytes: 1500000000000, expected: "1.5 TB"},
		{bytes: 1000000000000000, expected: "1.0 PB"},
		{bytes: 1500000000000000, expected: "1.5 PB"},
		{bytes: 1000000000000000000, expected: "1.0 EB"},
	}

	for _, tc := range testCases {
		result := memsize.ToByteString(tc.bytes)
		if result != tc.expected {
			t.Errorf("ToByteString(%d) = %s, expected %s", tc.bytes, result, tc.expected)
		}
	}
}

func TestInterfaceIsNullPointer(t *testing.T) {
	testCases := []struct {
		name     string
		value    any
		expected bool
	}{
		{name: "nil", value: nil, expected: true},
		{name: "nil pointer", value: (*int)(nil), expected: true},
		{name: "nil slice", value: []int(nil), expected: true},
		{name: "nil map", value: map[string]int(nil), expected: true},
		{name: "nil channel", value: (chan int)(nil), expected: true},
		{name: "non-nil pointer", value: new(int), expected: false},
		{name: "non-nil slice", value: []int{1, 2, 3}, expected: false},
		{name: "non-nil map", value: map[string]int{"a": 1}, expected: false},
		{name: "non-nil channel", value: make(chan int), expected: false},
		{name: "string", value: "hello", expected: false},
		{name: "int", value: 42, expected: false},
		{name: "bool", value: true, expected: false},
		{name: "struct", value: struct{}{}, expected: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := memsize.InterfaceIsNullPointer(tc.value)
			if result != tc.expected {
				t.Errorf("interfaceIsNullPointer(%s) = %v, expected %v", tc.name, result, tc.expected)
			}
		})
	}
}

// Test that SizeOf handles unencodable types gracefully
func TestSizeOfUnencodableType(t *testing.T) {
	// Functions are not encodable by gob
	fn := func() {}
	_, err := memsize.SizeOf(fn)
	if err == nil {
		t.Error("Expected error when trying to encode function, but got none")
	}
}

// Benchmark tests
func BenchmarkSizeOf(b *testing.B) {
	testValue := struct {
		Name string
		Age  int
		Tags []string
	}{
		Name: "benchmark",
		Age:  25,
		Tags: []string{"tag1", "tag2", "tag3"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := memsize.SizeOf(testValue)
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
	}
}

func BenchmarkToByteString(b *testing.B) {
	testValues := []int64{42, 1024, 1048576, 1073741824}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, value := range testValues {
			memsize.ToByteString(value)
		}
	}
}
