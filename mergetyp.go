// Package mergetyp can generate a relatively fast future proof function to
// merge two arbitrary types.
//
// If you have ever had to write a function to merge a structure, only to add
// fields to the structure later, this package may benefit you.
//
// The Gen function can generate a merge function that will skip potentially
// nested fields as appropriate. The speed of the function is slightly slower
// than a native inline function, but not terribly so (for one of my structs,
// the generated merge was 3.3x slower, from 60ns to 200ns).
//
// Using Gen can help future proof merging changes when the struct may grow
// over time. Further, the field blacklisting functionality can eliminate
// batches of tedious code.
//
// Recursive types are supported, as is skipping fields selectively in
// recursive types until a base limit. It is not yet possible to skip a field
// forever while recursing.
package mergetyp

import (
	"errors"
	"reflect"
	"unsafe"
)

// This is what happens in atomic.Value!
type ifaceWords struct {
	typ  unsafe.Pointer
	data unsafe.Pointer
}

// Config configures how a merge function will be generated. This type is used
// internally. Every call to Gen runs all option functions over an empty
// config. These functions can change the configuration to skip fields and
// whatnot.
type Config struct {
	skips     []string
	unsafeMap bool
}

// SkipFields is like SkipField, but allows for specifying multiple fields to
// be skipped.
func SkipFields(fields ...string) func(*Config) error {
	return func(c *Config) error {
		c.skips = append(c.skips, fields...)
		return nil
	}
}

// WithSlowerMapsUnsafely enables merging maps. This package returns a function
// that internally uses unsafe.Pointer to merge fields directly. To merge maps,
// we have to convert that unsafe.Pointer back into an interface{} and then
// call reflect.ValueOf on it. The conversion back into an interface{} is
// unsafe, and using reflect.Value to merge maps is slow.
func WithSlowerMapsUnsafely() func(*Config) error {
	return func(c *Config) error {
		c.unsafeMap = true
		return nil
	}
}

// SkipField adds a field to be skipped in a struct for generated merge
// function.
//
// All fields in skip must exist in some struct at some level. Only structs
// count as levels, and levels are specified with >. For example, with
// Foo>bar>Baz, the Baz field will be skipped three structs deep from an input
// struct.
//
// More concretely, with the following structs:
//
//     type Bar struct {
//         Baz chan int
//     }
//
//     type foobar struct {
//         bar []Bar
//     }
//
//     type MyType struct {
//         Foo foobar
//         p   int
//     }
//
// and the call Gen(new(MyType), SkipField("Foo>bar>Baz")), the channel deep in
// the struct will be ignored and only p will be merged.
func SkipField(field string) func(*Config) error {
	return func(c *Config) error {
		c.skips = append(c.skips, field)
		return nil
	}
}

// Gen returns a function to merge two values of the same type.
//
// The returned function will merge two values, the left and right value, into
// the left value.
//
// The input type must be a singly-indirected value (that is, a *Foo, not a Foo
// nor a **Foo), and that same type must be used on the returned function. The
// returned function will panic if used on other types.
//
// Some types cannot be merged: interfaces in structs cannot be merged (because
// there is no type behind it), and channels, functions, strings, and unsafe
// pointers cannot be merged.
//
// Maps can be merged, but you have to opt into merging maps. The returned
// closure's speed comes from using unsafe.Pointer internally and never using
// reflect. For maps, we have to go back into the reflect world, and doing so
// from an unsafe.Pointer relies on Go language internals. The internals have
// not changed in many Go releases, but exercise caution if you need to merge
// maps. If maps are merged, it is likely unsafe to re-use the right value's
// map.
//
// If the arbitrary value contains a slice, the merge function will swap the
// longer of the two slices to the left value. If the value contains a struct
// that has fields that point to other fields, the other fields will be merged
// twice (once for the direct field, once for the reference).
//
// Bool fields are merged such that "true" is always kept.
//
// This function takes an arbitrary number of options to configure merging
// behavior. These options control enabling merging maps, skipping fields,
// etc.
func Gen(i interface{}, options ...func(*Config) error) (func(l, r interface{}), error) {
	var c Config
	for _, option := range options {
		if err := option(&c); err != nil {
			return nil, err
		}
	}

	v := reflect.ValueOf(i)
	if v.Kind() != reflect.Ptr {
		return nil, errors.New("merge functions can only be generated for pointer types")
	}
	v = reflect.Indirect(v)
	if v.Kind() == reflect.Ptr {
		return nil, errors.New("merge functions can only be generated for single-pointer-indirection types")
	}

	f, err := (&generator{
		structFs: make(map[string]*mergeF),
		useMap:   c.unsafeMap,
		skips:    c.skips,
	}).gen(v)
	if err != nil {
		return nil, err
	}

	// _just_ to be sure that we allow the input value to be recycled,
	// we create our own zero type for saving the type pointer.
	z := reflect.Zero(reflect.ValueOf(i).Type()).Interface()
	iw := (*ifaceWords)(unsafe.Pointer(&z))
	check := func(l, r interface{}) (unsafe.Pointer, unsafe.Pointer) {
		il := (*ifaceWords)(unsafe.Pointer(&l))
		ir := (*ifaceWords)(unsafe.Pointer(&r))

		if il.typ != iw.typ || ir.typ != iw.typ {
			panic("merge function used on type it was not generated for")
		}
		return il.data, ir.data
	}

	if f == nil { // nothing inside to merge
		return func(l, r interface{}) {
			check(l, r)
		}, nil
	}

	return func(l, r interface{}) {
		lp, rp := check(l, r)
		f(lp, rp)
	}, nil
}

// MustGen is like Gen but panics if the merge function cannot be generated.
func MustGen(i interface{}, options ...func(*Config) error) func(l, r interface{}) {
	f, err := Gen(i, options...)
	if err != nil {
		panic(err)
	}
	return f
}
