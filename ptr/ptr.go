// Package ptr provides helper functions to get pointers to basic types.
package ptr

// Point returns a pointer to the given value.
//
//go:fix inline
func Point[T any](v T) *T {
	return new(v)
}

// Deref returns the value pointed to by p.
// If p is nil, it returns the zero value of type T.
func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
