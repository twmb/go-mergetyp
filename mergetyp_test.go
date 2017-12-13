package mergetyp

import (
	"reflect"
	"testing"
)

type Bar struct {
	Baz chan int
}

type foobar struct {
	bar []Bar
	m   map[foo]foo
}

type foo struct {
	o, t int
}

type S struct {
	F1  uint64
	f2  float32
	f3  uint64
	f4  bool
	f5  []foo
	f6  [2]int
	f7  *int
	Foo foobar
}

// This test is simple but effective. It could be prettier or more
// comprehensive, but it does test:
//
// - slices
// - nested structs
// - pointers
// - complexly typed maps
// - ignoring nested channels
// - a few primitive types
func TestGen(t *testing.T) {
	five := 5
	sl := S{
		F1: 1,
		f2: 2,
		f3: 3,
		f4: false,
		f5: []foo{
			foo{2, 3},
			foo{4, 5},
		},
		f6: [2]int{1, 1},
		f7: &five,
		Foo: foobar{
			bar: []Bar{Bar{Baz: make(chan int)}},
			m: map[foo]foo{
				foo{2, 2}: {8, 8},
				foo{3, 3}: {9, 9},
			},
		},
	}

	six := 6
	sr := S{
		F1: 7,
		f2: 8,
		f3: 9,
		f4: true,
		f5: []foo{
			foo{2, 2},
		},
		f6: [2]int{2, 2},
		f7: &six,
		Foo: foobar{
			m: map[foo]foo{
				foo{3, 3}: {10, 10},
				foo{4, 4}: {16, 16},
			},
		},
	}

	f, err := Gen(&sl,
		SkipFields("F1", "f5>o", "Foo>bar>Baz"),
		WithSlowerMapsUnsafely(),
	)
	if err != nil {
		t.Fatal(err)
	}

	f(&sl, &sr)

	eleven := 11
	exp := S{
		F1: 1,
		f2: 10,
		f3: 12,
		f4: true,
		f5: []foo{
			foo{2, 5},
			foo{4, 5},
		},
		f6: [2]int{3, 3},
		f7: &eleven,
		Foo: foobar{
			bar: sl.Foo.bar,
			m: map[foo]foo{
				foo{2, 2}: {8, 8},
				foo{3, 3}: {19, 19},
				foo{4, 4}: {16, 16},
			},
		},
	}

	if !reflect.DeepEqual(sl, exp) {
		t.Error("not deep equal")
	}
}
