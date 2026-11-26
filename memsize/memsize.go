// Package memsize provides utilities to estimate the memory footprint of Go values
// without relying on the reflect or unsafe packages for direct memory inspection.
//
// It leverages the gob encoding mechanism to serialize values into memory buffers
// and calculate their approximate in-memory size in bytes.
//
// This approach prioritizes safety and portability over raw performance, making it
// suitable for debugging, profiling, or logging approximate memory usage of
// application data structures.
//
// Features:
//   - Estimate the size of any Go value in bytes using gob encoding.
//   - Human-readable byte formatting with SI units (e.g., "1.2 kB").
//   - Graceful handling of nil pointers, maps, slices, arrays, and channels.
//
// Example:
//
//	size, err := memsize.SizeOf(myStruct)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println("Memory size:", memsize.ToByteString(size))
package memsize

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"reflect"

	"github.com/valentin-kaiser/go-core/v2/apperror"
)

// SizeOf returns the number of bytes that 'value' takes up in memory, without using reflect or unsafe
// this functions has minor overhead for the encoding process.
func SizeOf(value any) (int64, error) {
	if InterfaceIsNullPointer(value) {
		return 0, nil
	}
	buff := new(bytes.Buffer)
	err := gob.NewEncoder(buff).Encode(value)
	if err != nil {
		return 0, apperror.NewError("encoding value failed").AddError(err)
	}
	return int64(buff.Len()), nil
}

// ToByteString returns the number of bytes as a string with a unit
func ToByteString(b int64) string {
	const (
		unit = 1000
		// unitPrefixes are the SI prefixes of kilo, mega, giga, tera, peta and exa bytes.
		unitPrefixes = "kMGTPE"
	)
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB",
		float64(b)/float64(div), unitPrefixes[exp])
}

// InterfaceIsNullPointer returns true if the interface is nil or if the interface is a pointer, map, chan, or slice and is nil
func InterfaceIsNullPointer(i any) bool {
	if i == nil {
		return true
	}
	switch reflect.TypeOf(i).Kind() {
	case reflect.Pointer, reflect.Map, reflect.Chan, reflect.Slice:
		return reflect.ValueOf(i).IsNil()
	}
	return false
}
