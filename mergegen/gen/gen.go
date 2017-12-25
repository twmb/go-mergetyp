package gen

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

func typCant(t reflect.Type) error {
	switch t.Kind() {
	case reflect.Interface:
		return errors.New("unable to merge interfaces")
	case reflect.Chan:
		return errors.New("unable to merge channels")
	case reflect.Func:
		return errors.New("unable to merge functions")
	case reflect.String:
		return errors.New("unable to merge strings")
	case reflect.UnsafePointer:
		return errors.New("unable to merge unsafe.Pointers")
	case reflect.Invalid:
		return errors.New("unable to merge an invalid type")
	}
	return nil
}

func lrs(level int) (lu string, ru string, l string, r string) {
	return strings.Repeat("l", level),
		strings.Repeat("r", level),
		strings.Repeat("l", level+1),
		strings.Repeat("r", level+1)
}

type typesSeen map[string]struct{}

func genNum(b *bytes.Buffer, t reflect.Type, l, r string) bool {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16,
		reflect.Int32, reflect.Int64, reflect.Uint,
		reflect.Uint8, reflect.Uint16, reflect.Uint32,
		reflect.Uint64, reflect.Uintptr, reflect.Float32,
		reflect.Float64, reflect.Complex64, reflect.Complex128:
		fmt.Fprintf(b, "*%s += *%s\n", l, r)
	case reflect.Bool:
		fmt.Fprintf(b, "if *%s {\n*%s = true\n}\n", r, l)
	default:
		return false
	}
	return true
}

func (seen typesSeen) isRecursive(b *bytes.Buffer, name, l, r string) bool {
	if name != "" {
		if _, exists := seen[name]; exists {
			fmt.Fprintf(b, "%s.Merge(%s)\n", l, r)
			return true
		}
		seen[name] = struct{}{}
	}
	return false
}

func (seen typesSeen) genPtr(canIndir bool, b *bytes.Buffer, t reflect.Type, level int) error {
	if err := typCant(t); err != nil {
		return err
	}

	lu, ru, l, r := lrs(level)

	// If we _can_ indirect the level we are coming from, we can set that
	// upper level's left to this level's right if the upper level's left
	// is nil. Otherwise, we have to check both upper nils.
	if canIndir {
		level--
		lu, ru, l, r = lrs(level)
		fmt.Fprintf(b, "if %s == nil {\n", l)
		fmt.Fprintf(b, "*%s, *%s = %s, nil\n", lu, ru, r)
		fmt.Fprintf(b, "} else if %s != nil {\n", r)
		level++
		lu, ru, l, r = lrs(level)
	} else {
		fmt.Fprintf(b, "if %s != nil && %s != nil {\n", lu, ru)
	}
	defer fmt.Fprintf(b, "}\n")

	// pointers can recurse, or this may be a simple writeout
	if seen.isRecursive(b, t.Name(), lu, ru) || genNum(b, t, lu, ru) {
		return nil
	}

	// We do not want to indirect an array; otherwise we will copy the
	// entire thing.
	if t.Kind() == reflect.Array {
		return seen.genArray(b, t.Elem(), level)
	} else if t.Kind() == reflect.Struct {
		return seen.genStruct(b, t, level)
	}

	fmt.Fprintf(b, "%s, %s := *%s, *%s\n", l, r, lu, ru)
	if t.Kind() == reflect.Ptr {
		return seen.genPtr(true, b, t.Elem(), level+1)
	} else if t.Kind() == reflect.Map {
		return seen.genMap(b, t.Elem(), level+1)
	} // else t.Kind() == reflect.Slice...
	err := seen.genSlice(b, t.Elem(), level+1)
	fmt.Fprintf(b, "*%s, *%s = %s, %s\n", lu, ru, l, r)
	return err
}

func (seen typesSeen) genStruct(b *bytes.Buffer, t reflect.Type, level int) error {
	err := typCant(t)
	if err != nil {
		return err
	}
	lu, ru, l, r := lrs(level)
	for i := 0; i < t.NumField(); i++ {
		// TODO if private?
		sf := t.Field(i)
		lun := lu + "." + sf.Name
		run := ru + "." + sf.Name

		if genNum(b, sf.Type, "&"+lun, "&"+run) {
			continue
		}

		fmt.Fprint(b, "{\n") // scope each level to allow variable name reuse
		switch sf.Type.Kind() {
		case reflect.Array: // get the addr to each field and generate
			fmt.Fprintf(b, "%s, %s := &%s, &%s\n", l, r, lun, run)
			err = seen.genArray(b, sf.Type.Elem(), level+1)
		case reflect.Struct: // same
			fmt.Fprintf(b, "%s, %s := &%s, &%s\n", l, r, lun, run)
			err = seen.genStruct(b, sf.Type, level+1)

		case reflect.Slice: // copy the slice, generate, reset the slice
			fmt.Fprintf(b, "%s, %s := %s, %s\n", l, r, lun, run)
			err = seen.genSlice(b, sf.Type.Elem(), level+1)
			fmt.Fprintf(b, "%s, %s = %s, %s\n", lun, run, l, r)

		case reflect.Ptr:
			// genPtr has no concept of field names, so we have
			// to save a pointer to our field with the standard
			// name and then save the deref of that to another
			// level.
			_, _, ll, rr := lrs(level + 1)
			fmt.Fprintf(b, "%s, %s := &%s, &%s\n", l, r, lun, run)
			fmt.Fprintf(b, "%s, %s := *%s, *%s\n", ll, rr, l, r)
			err = seen.genPtr(true, b, sf.Type.Elem(), level+2)
		case reflect.Map:
			// Somewhat similar to above, but we have to save a
			// pointer to our value. I'm sure the abstraction could
			// be better.
			_, _, ll, rr := lrs(level + 1)
			fmt.Fprintf(b, "%s, %s := &%s, &%s\n", l, r, lun, run)
			fmt.Fprintf(b, "%s, %s := *%s, *%s\n", ll, rr, l, r)
			err = seen.genMap(b, sf.Type.Elem(), level+2)

		}
		if err != nil {
			return err
		}
		fmt.Fprint(b, "}\n")
	}
	return nil
}

func (seen typesSeen) genArray(b *bytes.Buffer, t reflect.Type, level int) error {
	if err := typCant(t); err != nil {
		return err
	}

	lu, ru, l, r := lrs(level)

	// genArray is always a pointer to an array. We avoid copying the
	// elements unless we know they are small (pointer, struct, map).
	fmt.Fprintf(b, "for i := range %s {\n", ru)
	fmt.Fprintf(b, "%s, %s := &%s[i], &%s[i]\n", l, r, lu, ru)
	defer fmt.Fprintf(b, "}\n")

	if genNum(b, t, l, r) {
		return nil
	}

	if t.Kind() == reflect.Array {
		return seen.genArray(b, t.Elem(), level+1)
	} else if t.Kind() == reflect.Struct {
		return seen.genStruct(b, t, level+1)
	}

	// Small elements: copy them out for recursive functions.
	level++
	lu, ru, l, r = lrs(level)
	fmt.Fprintf(b, "%s, %s := *%s, *%s\n", l, r, lu, ru)

	if t.Kind() == reflect.Ptr {
		return seen.genPtr(true, b, t.Elem(), level+1)
	} else if t.Kind() == reflect.Map {
		return seen.genMap(b, t.Elem(), level+1)
	} // else t.Kind() == reflect.Slice...

	// We need to copy this potentially longer slice back
	// into the array.
	err := seen.genSlice(b, t.Elem(), level+1)
	fmt.Fprintf(b, "*%s, *%s = %s, %s\n", lu, ru, l, r)
	return err
}

func (seen typesSeen) genSlice(b *bytes.Buffer, t reflect.Type, level int) error {
	if err := typCant(t); err != nil {
		return err
	}
	lu, ru, _, _ := lrs(level)
	// Since all the code is nested, we do not need to worry about returning
	// swapped slices: if we swap at this level, it will persist up. After
	// this potential swap, we can just treat the slice like an array.
	fmt.Fprintf(b, "if len(%s) > len(%s) {\n", ru, lu)
	fmt.Fprintf(b, "%[1]s, %[2]s = %[2]s, %[1]s\n", lu, ru)
	fmt.Fprintf(b, "}\n")
	return seen.genArray(b, t, level)
}

func (seen typesSeen) genMap(b *bytes.Buffer, t reflect.Type, level int) error {
	err := typCant(t)
	if err != nil {
		return err
	}

	// This is similar to getPtr, except we can always indirect because
	// only pointers can be at the top level.
	level--
	lu, ru, l, r := lrs(level)
	fmt.Fprintf(b, "if %s == nil {\n", l)
	fmt.Fprintf(b, "*%s, *%s = %s, nil\n", lu, ru, r)
	fmt.Fprintf(b, "} else if %s != nil {\n", r)
	level++
	lu, ru, l, r = lrs(level)
	defer fmt.Fprint(b, "}\n")

	fmt.Fprintf(b, "for k, %s := range %s {\n", r, ru)
	defer fmt.Fprint(b, "}\n")

	fmt.Fprintf(b, "%s, exists := %s[k]\n", l, lu)
	fmt.Fprint(b, "if !exists {\n")
	fmt.Fprintf(b, "%s[k] = %s\n", lu, r)
	fmt.Fprint(b, "continue\n")
	fmt.Fprint(b, "}\n")

	reassign := func() { fmt.Fprintf(b, "%s[k] = %s\n", lu, l) }

	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16,
		reflect.Int32, reflect.Int64, reflect.Uint,
		reflect.Uint8, reflect.Uint16, reflect.Uint32,
		reflect.Uint64, reflect.Uintptr, reflect.Float32,
		reflect.Float64, reflect.Complex64, reflect.Complex128:
		fmt.Fprintf(b, "%s += %s\n", l, r)
		reassign()
	case reflect.Bool:
		fmt.Fprintf(b, "if %s {\n%s[k] = true\n}\n", r, l)
		reassign()
	case reflect.Struct:
		err = seen.genStruct(b, t, level+1)
		reassign()
	case reflect.Array:
		err = seen.genArray(b, t.Elem(), level+1)
		reassign()
	case reflect.Slice:
		err = seen.genSlice(b, t.Elem(), level+1)
		reassign()

	// The following two cases generate one useless conditional, but it
	// is cheap and may even be compiled out because of dead code elim.
	case reflect.Ptr:
		fmt.Fprintf(b, "if %s == nil {\n", l)
		fmt.Fprintf(b, "%s[k] = %s\n", lu, r)
		fmt.Fprint(b, "continue\n")
		fmt.Fprint(b, "}\n")
		err = seen.genPtr(false, b, t.Elem(), level+1)

	case reflect.Map:
		fmt.Fprintf(b, "if %s == nil {\n", l)
		fmt.Fprintf(b, "%s[k] = %s\n", lu, r)
		fmt.Fprint(b, "continue\n")
		fmt.Fprint(b, "}\n")
		err = seen.genMap(b, t.Elem(), level+1)
	}

	return err
}

func Gen(i interface{}) (string, error) {
	t := reflect.TypeOf(i)
	if t.Kind() != reflect.Ptr {
		return "", errors.New("value is not a pointer")
	}
	t = t.Elem()
	if t.Kind() == reflect.Ptr {
		return "", errors.New("unable to merge types that are simply pointers (invalid receiver type)")
	}
	b := new(bytes.Buffer)
	fmt.Fprintf(b, "func (l *%[1]s) Merge(r *%[1]s) {\n", t.Name())
	seen := make(typesSeen)
	err := seen.genPtr(false, b, t, 1)
	fmt.Fprintf(b, "}\n")
	if err != nil {
		return "", err
	}
	return b.String(), nil
}
