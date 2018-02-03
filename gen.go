package mergetyp

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unsafe"

	"github.com/twmb/vali"
)

// This file contains the logic to recursively generate merge functions. We
// special case primitive types at every level to avoid wrapping simple
// additions with a closure.

type generator struct {
	structFs map[string]*mergeF
	useMap   bool
	skips    []string
}

type mergeF = func(unsafe.Pointer, unsafe.Pointer)

func fieldByOffset(u unsafe.Pointer, o uintptr) unsafe.Pointer {
	return unsafe.Pointer(uintptr(u) + o)
}

// gen is the entry point for all recursion; it generates a closure to merge
// an arbitrary value (with some exceptions that return errors).
func (g *generator) gen(v reflect.Value) (mergeF, error) {
	if len(g.skips) > 0 {
		switch v.Kind() {
		// We can only contain skips on structs or types that may
		// contain structs.
		case reflect.Slice, reflect.Struct, reflect.Array, reflect.Ptr:
		default:
			return nil, fmt.Errorf("unable to skip fields on kind %v", v.Kind())
		}
	}

	switch v.Kind() {
	case reflect.Interface:
		// TODO we can probably create a Merger interface and test/use
		// that here.
		return nil, errors.New("it is impossible to merge two types that are interfaces (unable to determine concrete type)")
	case reflect.Chan:
		return nil, errors.New("unable to merge channels")
	case reflect.Func:
		return nil, errors.New("unable to merge functions")
	case reflect.String:
		return nil, errors.New("unable to merge strings")
	case reflect.UnsafePointer:
		return nil, errors.New("unable to merge unsafe pointers (unable to determine the type)")
	case reflect.Invalid:
		return nil, errors.New("unable to merge an invalid type")

	// The primitive cases below are only necessary on native types or
	// behind reflect.Ptr.
	case reflect.Bool:
		return func(l, r unsafe.Pointer) {
			if *(*bool)(r) {
				*(*bool)(l) = true
			}
		}, nil
	case reflect.Int:
		return func(l, r unsafe.Pointer) { *(*int)(l) += *(*int)(r) }, nil
	case reflect.Int8:
		return func(l, r unsafe.Pointer) { *(*int8)(l) += *(*int8)(r) }, nil
	case reflect.Int16:
		return func(l, r unsafe.Pointer) { *(*int16)(l) += *(*int16)(r) }, nil
	case reflect.Int32:
		return func(l, r unsafe.Pointer) { *(*int32)(l) += *(*int32)(r) }, nil
	case reflect.Int64:
		return func(l, r unsafe.Pointer) { *(*int64)(l) += *(*int64)(r) }, nil
	case reflect.Uint:
		return func(l, r unsafe.Pointer) { *(*uint)(l) += *(*uint)(r) }, nil
	case reflect.Uint8:
		return func(l, r unsafe.Pointer) { *(*uint8)(l) += *(*uint8)(r) }, nil
	case reflect.Uint16:
		return func(l, r unsafe.Pointer) { *(*uint16)(l) += *(*uint16)(r) }, nil
	case reflect.Uint32:
		return func(l, r unsafe.Pointer) { *(*uint32)(l) += *(*uint32)(r) }, nil
	case reflect.Uint64:
		return func(l, r unsafe.Pointer) { *(*uint64)(l) += *(*uint64)(r) }, nil
	case reflect.Uintptr:
		return func(l, r unsafe.Pointer) { *(*uintptr)(l) += *(*uintptr)(r) }, nil
	case reflect.Float32:
		return func(l, r unsafe.Pointer) { *(*float32)(l) += *(*float32)(r) }, nil
	case reflect.Float64:
		return func(l, r unsafe.Pointer) { *(*float64)(l) += *(*float64)(r) }, nil
	case reflect.Complex64:
		return func(l, r unsafe.Pointer) { *(*complex64)(l) += *(*complex64)(r) }, nil
	case reflect.Complex128:
		return func(l, r unsafe.Pointer) { *(*complex128)(l) += *(*complex128)(r) }, nil

	// We do not attempt to optimize what is behind a pointer.
	case reflect.Ptr:
		et := v.Type().Elem()
		ez := reflect.Zero(et)
		f, err := g.gen(ez)
		if err != nil {
			return nil, err
		}
		if f == nil {
			return nil, nil
		}
		// If either side of what the pointer points to is nil, our
		// job is easy and we should not recurse.
		return func(l, r unsafe.Pointer) {
			pl := (*unsafe.Pointer)(l)
			pr := (*unsafe.Pointer)(r)
			il := *pl
			ir := *pr
			if il == nil {
				*pl = ir
				*pr = nil
				return
			}
			if ir == nil {
				return
			}
			f(il, ir)
		}, nil

	case reflect.Array:
		return g.genArray(v)

	case reflect.Slice:
		return g.genSlice(v)

	case reflect.Struct:
		// We may skip recursive struct fields selectively, so we have
		// to recursive until we will not skip recusrive fields.
		if len(g.skips) > 0 {
			return g.genStruct(v)
		}

		// For recursive structs, we save a pointer to a function that
		// we will fill in when we return up.
		typ := v.Type()
		name := typ.PkgPath() + "." + typ.Name()
		pf, exists := g.structFs[name]
		if exists {
			return func(l, r unsafe.Pointer) { (*pf)(l, r) }, nil
		}

		pf = new(mergeF)
		g.structFs[name] = pf
		f, err := g.genStruct(v)
		if err != nil {
			return nil, err
		}
		*pf = f
		return f, nil

	case reflect.Map:
		if !g.useMap {
			return nil, errors.New("unable to merge maps: use WithSlowerMapsUnsafely if it is absolutely necessary to merge maps")
		}
		return g.genMap(v)

	default:
		panic("this switch statement should be comprehensive?")
	}
}

// genMap generates the closure to merge an map. This is the most unsafe
// function; we have to do a bunch of trickery with values we should not be
// accessing.
func (g *generator) genMap(v reflect.Value) (mergeF, error) {
	// First, we generate the merge function for the map values.
	et := v.Type().Elem()

	ez := reflect.Zero(et)
	f, err := g.gen(ez)
	if err != nil {
		return nil, err
	}

	// The function we calculate for merging is based off a pointer to the
	// value. If the map's value _is_ a pointer, we do not need to take its
	// address before calling our closure. If the value is not a pointer,
	// we do need to take the address.
	//
	// Maps _are_ pointers, and pointers are also obviously pointers.
	// Everything else is a struct or a primitive.
	var valNeedsIndir bool
	etk := et.Kind()
	if etk == reflect.Map || etk == reflect.Ptr {
		valNeedsIndir = true
	}

	// For maps, there is no way to actually set keys with unsafe.Pointers,
	// and we cannot create an interface and cast the pointer to a map of
	// the right size. We must go through reflect's few map functions.
	//
	// Unfortunately, the key or value may be of types we technically
	// cannot access. Calling reflect.Value's Interface function may panic.
	// To get around that, we will unsafely reach into a reflect.Value
	// directly and create an interface{}. With that, we can pull out
	// the value's type and recreate interfaces for reflect.ValueOf later.
	i := vali.Interface(v)
	iw := (*ifaceWords)(unsafe.Pointer(&i))
	it := iw.typ

	var zv reflect.Value

	return func(l, r unsafe.Pointer) {
		var li interface{}
		var ri interface{}

		// Now, reconstruct the map with the value's type we pulled out
		// above. We set typ last so the GC does not see a partial
		// type.
		//
		// Go maps _are_ pointers, so we have a pointer to a pointer
		// right now. We un-pointer l and r to setup the interface
		// correctly.
		//
		// We fill in the type last so that the GC does not observe a
		// partially built value.
		liw := (*ifaceWords)(unsafe.Pointer(&li))
		liw.data = *(*unsafe.Pointer)(l)
		liw.typ = it

		riw := (*ifaceWords)(unsafe.Pointer(&ri))
		riw.data = *(*unsafe.Pointer)(r)
		riw.typ = it

		// With these fake interfaces, we can do reflect.ValueOf and
		// access map getter/setter functions.
		lv := reflect.ValueOf(li)
		rv := reflect.ValueOf(ri)

		rks := rv.MapKeys()
		for _, rk := range rks {
			lkv := lv.MapIndex(rk)

			// If left's map value does not exist for a right's
			// key, we can just set left's key to what is in right.
			if lkv == zv {
				lv.SetMapIndex(rk, rv.MapIndex(rk))
				continue
			}

			// If left's map value does exist, we have to merge it
			// with right's value. Our merge function works on
			// unsafe.Pointers, so we have to again use the unsafe
			// vali.Interface to get an interface{} and then
			// reach into the interface's data.
			rkv := rv.MapIndex(rk)
			lkvi := vali.Interface(lkv)
			rkvi := vali.Interface(rkv)

			lkviw := (*ifaceWords)(unsafe.Pointer(&lkvi))
			rkviw := (*ifaceWords)(unsafe.Pointer(&rkvi))
			if valNeedsIndir {
				f(unsafe.Pointer(&lkviw.data), unsafe.Pointer(&rkviw.data))
			} else {
				f(lkviw.data, rkviw.data)
			}

			// Now that the value is merged, we can set left's
			// value to the new value.
			lv.SetMapIndex(rk, reflect.ValueOf(lkvi))
		}
	}, nil
}

// genArray generates the closure to merge an array.
func (g *generator) genArray(v reflect.Value) (mergeF, error) {
	len := uintptr(v.Len())
	et := v.Type().Elem()
	size := et.Size()
	end := len * size

	// This massive block is a bunch of duplication to use faster functions
	// for primitive types.
	switch et.Kind() {
	case reflect.Bool:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				if *(*bool)(fieldByOffset(r, offset)) {
					*(*bool)(fieldByOffset(l, offset)) = true
				}
			}
		}, nil
	case reflect.Int:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				*(*int)(fieldByOffset(l, offset)) += *(*int)(fieldByOffset(r, offset))
			}
		}, nil
	case reflect.Int8:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				*(*int8)(fieldByOffset(l, offset)) += *(*int8)(fieldByOffset(r, offset))
			}
		}, nil
	case reflect.Int16:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				*(*int16)(fieldByOffset(l, offset)) += *(*int16)(fieldByOffset(r, offset))
			}
		}, nil
	case reflect.Int32:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				*(*int32)(fieldByOffset(l, offset)) += *(*int32)(fieldByOffset(r, offset))
			}
		}, nil
	case reflect.Int64:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				*(*int64)(fieldByOffset(l, offset)) += *(*int64)(fieldByOffset(r, offset))
			}
		}, nil
	case reflect.Uint:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				*(*uint)(fieldByOffset(l, offset)) += *(*uint)(fieldByOffset(r, offset))
			}
		}, nil
	case reflect.Uint8:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				*(*uint8)(fieldByOffset(l, offset)) += *(*uint8)(fieldByOffset(r, offset))
			}
		}, nil
	case reflect.Uint16:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				*(*uint16)(fieldByOffset(l, offset)) += *(*uint16)(fieldByOffset(r, offset))
			}
		}, nil
	case reflect.Uint32:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				*(*uint32)(fieldByOffset(l, offset)) += *(*uint32)(fieldByOffset(r, offset))
			}
		}, nil
	case reflect.Uint64:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				*(*uint64)(fieldByOffset(l, offset)) += *(*uint64)(fieldByOffset(r, offset))
			}
		}, nil
	case reflect.Float32:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				*(*float32)(fieldByOffset(l, offset)) += *(*float32)(fieldByOffset(r, offset))
			}
		}, nil
	case reflect.Float64:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				*(*float64)(fieldByOffset(l, offset)) += *(*float64)(fieldByOffset(r, offset))
			}
		}, nil
	case reflect.Complex64:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				*(*complex64)(fieldByOffset(l, offset)) += *(*complex64)(fieldByOffset(r, offset))
			}
		}, nil
	case reflect.Complex128:
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				*(*complex128)(fieldByOffset(l, offset)) += *(*complex128)(fieldByOffset(r, offset))
			}
		}, nil

	// Our default cases is recursion, per usual.
	default:
		z := reflect.Zero(et)
		f, err := g.gen(z)
		if err != nil {
			return nil, err
		}
		if f == nil {
			return nil, nil
		}
		return func(l, r unsafe.Pointer) {
			for offset := uintptr(0); offset < end; offset += size {
				f(fieldByOffset(l, offset), fieldByOffset(r, offset))
			}
		}, nil
	}
}

// genSlice generates the closure to merge a slice.
func (g *generator) genSlice(v reflect.Value) (mergeF, error) {
	et := v.Type().Elem()
	size := et.Size()

	normalize := func(l, r unsafe.Pointer) (*reflect.SliceHeader, *reflect.SliceHeader, uintptr) {
		hl := ((*reflect.SliceHeader)(l))
		hr := ((*reflect.SliceHeader)(r))

		limit := uintptr(hl.Len)
		if uintptr(hr.Len) < limit {
			limit = uintptr(hr.Len)
		}
		// Left side becomes longest slice.
		if hr.Len > hl.Len {
			*hl, *hr = *hr, *hl
		}

		end := limit * size
		return hl, hr, end
	}

	// Just like in array above, we special case slices of primitive types
	// so that the merge function generated is faster.
	switch et.Kind() {
	case reflect.Bool:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				if *(*bool)(unsafe.Pointer(hr.Data + offset)) {
					*(*bool)(unsafe.Pointer(hl.Data + offset)) = true
				}
			}
		}, nil
	case reflect.Int:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				*(*int)(unsafe.Pointer(hl.Data + offset)) += *(*int)(unsafe.Pointer(hr.Data + offset))
			}
		}, nil
	case reflect.Int8:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				*(*int8)(unsafe.Pointer(hl.Data + offset)) += *(*int8)(unsafe.Pointer(hr.Data + offset))
			}
		}, nil
	case reflect.Int16:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				*(*int16)(unsafe.Pointer(hl.Data + offset)) += *(*int16)(unsafe.Pointer(hr.Data + offset))
			}
		}, nil
	case reflect.Int32:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				*(*int32)(unsafe.Pointer(hl.Data + offset)) += *(*int32)(unsafe.Pointer(hr.Data + offset))
			}
		}, nil
	case reflect.Int64:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				*(*int64)(unsafe.Pointer(hl.Data + offset)) += *(*int64)(unsafe.Pointer(hr.Data + offset))
			}
		}, nil
	case reflect.Uint:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				*(*uint)(unsafe.Pointer(hl.Data + offset)) += *(*uint)(unsafe.Pointer(hr.Data + offset))
			}
		}, nil
	case reflect.Uint8:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				*(*uint8)(unsafe.Pointer(hl.Data + offset)) += *(*uint8)(unsafe.Pointer(hr.Data + offset))
			}
		}, nil
	case reflect.Uint16:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				*(*uint16)(unsafe.Pointer(hl.Data + offset)) += *(*uint16)(unsafe.Pointer(hr.Data + offset))
			}
		}, nil
	case reflect.Uint32:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				*(*uint32)(unsafe.Pointer(hl.Data + offset)) += *(*uint32)(unsafe.Pointer(hr.Data + offset))
			}
		}, nil
	case reflect.Uint64:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				*(*uint64)(unsafe.Pointer(hl.Data + offset)) += *(*uint64)(unsafe.Pointer(hr.Data + offset))
			}
		}, nil
	case reflect.Float32:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				*(*float32)(unsafe.Pointer(hl.Data + offset)) += *(*float32)(unsafe.Pointer(hr.Data + offset))
			}
		}, nil
	case reflect.Float64:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				*(*float64)(unsafe.Pointer(hl.Data + offset)) += *(*float64)(unsafe.Pointer(hr.Data + offset))
			}
		}, nil
	case reflect.Complex64:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				*(*complex64)(unsafe.Pointer(hl.Data + offset)) += *(*complex64)(unsafe.Pointer(hr.Data + offset))
			}
		}, nil
	case reflect.Complex128:
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				*(*complex128)(unsafe.Pointer(hl.Data + offset)) += *(*complex128)(unsafe.Pointer(hr.Data + offset))
			}
		}, nil

	default:
		z := reflect.Zero(et)
		f, err := g.gen(z)
		if err != nil {
			return nil, err
		}
		if f == nil {
			return nil, nil
		}
		return func(l, r unsafe.Pointer) {
			hl, hr, end := normalize(l, r)
			for offset := uintptr(0); offset < end; offset += size {
				f(unsafe.Pointer(hl.Data+offset), unsafe.Pointer(hr.Data+offset))
			}

		}, nil
	}
}

// genStruct generates a closure to merge a struct.
func (g *generator) genStruct(v reflect.Value) (mergeF, error) {
	// We actually care about skips in structs!
	//
	// For all skips that do not have `>`, we require the field to exist,
	// and then we skip it.
	//
	// For all skips that have `>`, trim past the `>` and pass the remnants
	// when recursing down that field. `foo>bar` and `foo>baz` would pass
	// `bar, baz` when going down `foo`.
	skipMyLevel := make(map[string]struct{})
	skipNextLevel := make(map[string][]string)

	for _, skip := range g.skips {
		idx := strings.IndexByte(skip, '>')
		if idx == -1 {
			skipMyLevel[skip] = struct{}{}
			continue
		}

		if idx == 0 || len(skip) < idx+1 {
			return nil, errors.New("invalid skip: empty field name")
		}

		field := skip[:idx]
		subFields := skip[idx+1:]

		skipNextLevel[field] = append(skipNextLevel[field], subFields)
	}

	// I expect that most structs to merge will contain primitive number
	// types. To avoid a bunch of recursive closure function overhead, we
	// can save offsets to these primitive types and merge them directly.
	//
	// The overhead from using _all_ off these, all of the time, is minimal
	// compared to a slice of exact functions.
	var bools, is, i8s, i16s, i32s, i64s,
		us, u8s, u16s, u32s, u64s,
		f32s, f64s, c64s, c128s []uintptr

	// If we _do_ need to recurse, this type merges the field offset with
	// the function to merge that field.
	type offsetF struct {
		offset uintptr
		f      mergeF
	}
	var offsetFs []offsetF

	// If we add a single field, we return a function. If we skip all
	// fields, we return nil. Levels higher up will bubble up the nil
	// as appropriate.
	added := 0
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if _, exists := skipMyLevel[sf.Name]; exists {
			delete(skipMyLevel, sf.Name)
			continue
		}

		switch sf.Type.Kind() {
		case reflect.Bool:
			bools = append(bools, sf.Offset)
		case reflect.Int:
			is = append(is, sf.Offset)
		case reflect.Int8:
			i8s = append(i8s, sf.Offset)
		case reflect.Int16:
			i16s = append(i16s, sf.Offset)
		case reflect.Int32:
			i32s = append(i32s, sf.Offset)
		case reflect.Int64:
			i64s = append(i64s, sf.Offset)
		case reflect.Uint:
			us = append(us, sf.Offset)
		case reflect.Uint8:
			u8s = append(u8s, sf.Offset)
		case reflect.Uint16:
			u16s = append(u16s, sf.Offset)
		case reflect.Uint32:
			u32s = append(u32s, sf.Offset)
		case reflect.Uint64:
			u64s = append(u64s, sf.Offset)
		case reflect.Float32:
			f32s = append(f32s, sf.Offset)
		case reflect.Float64:
			f64s = append(f64s, sf.Offset)
		case reflect.Complex64:
			c64s = append(c64s, sf.Offset)
		case reflect.Complex128:
			c128s = append(c128s, sf.Offset)
		default:
			f, err := (&generator{
				structFs: g.structFs,
				useMap:   g.useMap,
				skips:    skipNextLevel[sf.Name],
			}).gen(v.Field(i))
			delete(skipNextLevel, sf.Name)
			if err != nil {
				return nil, err
			}
			if f == nil {
				continue
			}
			offsetFs = append(offsetFs, offsetF{sf.Offset, f})
		}
		added++
	}

	// We require that the skip fields be an exact match: all fields to
	// skip must have been seen.
	if len(skipMyLevel) > 0 {
		return nil, errors.New("did not see all fields that we were required to skip")
	}
	if len(skipNextLevel) > 0 {
		return nil, errors.New("did not see all fields names for next level skips")
	}

	if added == 0 {
		return nil, nil
	}

	return func(l, r unsafe.Pointer) {
		for _, offset := range bools {
			if *(*bool)(fieldByOffset(r, offset)) {
				*(*bool)(fieldByOffset(l, offset)) = true
			}
		}
		for _, offset := range is {
			*(*int)(fieldByOffset(l, offset)) += *(*int)(fieldByOffset(r, offset))
		}
		for _, offset := range i8s {
			*(*int8)(fieldByOffset(l, offset)) += *(*int8)(fieldByOffset(r, offset))
		}
		for _, offset := range i16s {
			*(*int16)(fieldByOffset(l, offset)) += *(*int16)(fieldByOffset(r, offset))
		}
		for _, offset := range i32s {
			*(*int32)(fieldByOffset(l, offset)) += *(*int32)(fieldByOffset(r, offset))
		}
		for _, offset := range i64s {
			*(*int64)(fieldByOffset(l, offset)) += *(*int64)(fieldByOffset(r, offset))
		}
		for _, offset := range us {
			*(*uint)(fieldByOffset(l, offset)) += *(*uint)(fieldByOffset(r, offset))
		}
		for _, offset := range u8s {
			*(*uint8)(fieldByOffset(l, offset)) += *(*uint8)(fieldByOffset(r, offset))
		}
		for _, offset := range u16s {
			*(*uint16)(fieldByOffset(l, offset)) += *(*uint16)(fieldByOffset(r, offset))
		}
		for _, offset := range u32s {
			*(*uint32)(fieldByOffset(l, offset)) += *(*uint32)(fieldByOffset(r, offset))
		}
		for _, offset := range u64s {
			*(*uint64)(fieldByOffset(l, offset)) += *(*uint64)(fieldByOffset(r, offset))
		}
		for _, offset := range f32s {
			*(*float32)(fieldByOffset(l, offset)) += *(*float32)(fieldByOffset(r, offset))
		}
		for _, offset := range f64s {
			*(*float64)(fieldByOffset(l, offset)) += *(*float64)(fieldByOffset(r, offset))
		}
		for _, offset := range c64s {
			*(*complex64)(fieldByOffset(l, offset)) += *(*complex64)(fieldByOffset(r, offset))
		}
		for _, offset := range c128s {
			*(*complex128)(fieldByOffset(l, offset)) += *(*complex128)(fieldByOffset(r, offset))
		}
		for _, of := range offsetFs {
			of.f(fieldByOffset(l, of.offset), fieldByOffset(r, of.offset))
		}
	}, nil
}
