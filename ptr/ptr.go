// Package ptr provides helper functions to get pointers to basic types.
package ptr

// Point returns a pointer to the given value.
func Point[T any](v T) *T {
	return &v
}
