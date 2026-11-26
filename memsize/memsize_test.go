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
		{"nil value", nil, 0, false},
		{"string", "hello", 9, false},
		{"int", 42, 4, false},
		{"bool", true, 4, false},
		{"slice", []int{1, 2, 3}, 20, false},
		{"map", map[string]int{"a": 1, "b": 2}, 26, false},
		{"struct", struct{ Name string }{Name: "test"}, 32, false},
		{"nil pointer", (*int)(nil), 0, false},
		{"nil slice", []int(nil), 0, false},
		{"nil map", map[string]int(nil), 0, false},
		{"nil channel", (chan int)(nil), 0, false},
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
		{0, "0 B"},
		{1, "1 B"},
		{999, "999 B"},
		{1000, "1.0 kB"},
		{1024, "1.0 kB"},
		{1500, "1.5 kB"},
		{1000000, "1.0 MB"},
		{1024000, "1.0 MB"},
		{1500000, "1.5 MB"},
		{1000000000, "1.0 GB"},
		{1500000000, "1.5 GB"},
		{1000000000000, "1.0 TB"},
		{1500000000000, "1.5 TB"},
		{1000000000000000, "1.0 PB"},
		{1500000000000000, "1.5 PB"},
		{1000000000000000000, "1.0 EB"},
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
		{"nil", nil, true},
		{"nil pointer", (*int)(nil), true},
		{"nil slice", []int(nil), true},
		{"nil map", map[string]int(nil), true},
		{"nil channel", (chan int)(nil), true},
		{"non-nil pointer", new(int), false},
		{"non-nil slice", []int{1, 2, 3}, false},
		{"non-nil map", map[string]int{"a": 1}, false},
		{"non-nil channel", make(chan int), false},
		{"string", "hello", false},
		{"int", 42, false},
		{"bool", true, false},
		{"struct", struct{}{}, false},
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
