package gen

import (
	"fmt"
	"go/format"
	"testing"
)

type bar struct {
	baz   int
	bingo *[]int
}

type foo struct {
	b [2]map[int]int
}

func TestGen(t *testing.T) {
	str, err := Gen(new(foo))
	if err != nil {
		t.Fatal(err)
	}
	fmtted, err := format.Source([]byte(str))
	if err != nil {
		fmt.Printf("vvv\n%s\n^^^\n", str)
		t.Fatal(err)
	}
	fmt.Printf("vvv\n%s\n^^^\n", fmtted)
}
