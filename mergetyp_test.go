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
	m   map[foo]map[foo]int
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
	m2  map[int]*int
	m3  map[int][]foo
	m4  map[int]foo
	m5  map[int][2]*foo
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
		// The following maps are a bit harder to think about the
		// result of merging...
		Foo: foobar{
			bar: []Bar{Bar{Baz: make(chan int)}},
			m: map[foo]map[foo]int{
				foo{2, 2}: map[foo]int{foo{8, 8}: 1},
				foo{3, 3}: map[foo]int{foo{9, 9}: 2},
			},
		},
		m2: map[int]*int{
			5: func() *int { a := 5; return &a }(),
		},
		m3: map[int][]foo{
			1: []foo{foo{2, 2}, foo{3, 3}},
			2: []foo{foo{4, 4}},
		},
		m4: map[int]foo{
			1: foo{2, 2},
		},
		m5: map[int][2]*foo{
			1: [2]*foo{&foo{1, 2}, &foo{3, 4}},
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
			m: map[foo]map[foo]int{
				foo{3, 3}: map[foo]int{foo{9, 9}: 9},
				foo{4, 4}: map[foo]int{foo{16, 16}: 4},
			},
		},
		m2: map[int]*int{
			5: func() *int { a := 6; return &a }(),
			7: func() *int { a := 7; return &a }(),
		},
		m3: map[int][]foo{
			1: []foo{foo{5, 5}},
		},
		m4: map[int]foo{
			1: foo{2, 2},
			2: foo{3, 3},
		},
		m5: map[int][2]*foo{
			1: [2]*foo{&foo{1, 2}, &foo{3, 4}},
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
			m: map[foo]map[foo]int{
				foo{2, 2}: map[foo]int{foo{8, 8}: 1},
				foo{3, 3}: map[foo]int{foo{9, 9}: 11},
				foo{4, 4}: map[foo]int{foo{16, 16}: 4},
			},
		},
		m2: map[int]*int{
			5: func() *int { a := 11; return &a }(),
			7: func() *int { a := 7; return &a }(),
		},
		m3: map[int][]foo{
			1: []foo{foo{7, 7}, foo{3, 3}},
			2: []foo{foo{4, 4}},
		},
		m4: map[int]foo{
			1: foo{4, 4},
			2: foo{3, 3},
		},
		m5: map[int][2]*foo{
			1: [2]*foo{&foo{2, 4}, &foo{6, 8}},
		},
	}

	if !reflect.DeepEqual(sl, exp) {
		t.Error("not deep equal")
	}
}
