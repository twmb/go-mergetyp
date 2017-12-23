package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// 1) package path
// 2) type

// package name, package path

// flag for output filename

var (
	pkgPath = flag.String("pkg-path", "", "path used in import statements for the package we are generating in")
	pkgName = flag.String("pkg-name", "", "package name for the package we are generating in")
	typ     = flag.String("typ", "", "type to use for generating")
	outFile = flag.String("outfile", "", "output filename to write to")
)

func chk(fmt string, err error) {
	if err != nil {
		die(fmt, err)
	}
}

func die(f string, args ...interface{}) {
	fmt.Printf(f, args...)
	os.Exit(1)
}

func main() {
	flag.Parse()

	f, err := os.Create(*outFile)
	chk("unable to create outfile: %v", err)
	_, err = fmt.Fprintf(f, `package %s; type MergeGen_%s *%s`, *pkgName, *typ, *typ)
	chk("unable to write stub: %v", err)
	chk("unable to close stub: %v", f.Close())

	rand.Seed(time.Now().UnixNano())

	var tmpName string
	var bsF *os.File
	for i := 0; i < 10000; i++ {
		tmpName = "mergegenbs" + strconv.FormatUint(rand.Uint64(), 10) + ".go"
		bsF, err = os.OpenFile(tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			continue
		}
		break
	}
	chk("unable to create bootstrap file: %v", err)
	_, err = fmt.Fprintf(bsF, bootstrapFmt, *pkgPath, *typ)
	chk("unable to write bootstrap file: %v", err)
	chk("unable to close bootstrap file: %v", bsF.Close())

	ffinal, err := os.Create(*outFile + "tmp")
	chk("unable to create intermediate outfile: %v", err)
	defer os.Remove(ffinal.Name())

	cmd := exec.Command("go", "run", tmpName)
	cmd.Stdout = ffinal
	cmd.Stderr = os.Stderr
	chk("unable to run bootstrap: %v", cmd.Run())
	chk("unable to close intermediate outfile: %v", ffinal.Close())

	cmd = exec.Command("gofmt", "-w", ffinal.Name())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	chk("unable to format intermediate outfile: %v", cmd.Run())

	chk("unable to rename intermediate outfile: %v", os.Rename(ffinal.Name(), *outFile))
}

const bootstrapFmt = `// +build ignore

package main

import (
	"fmt"
	"os"

	pkg "%s"

	"github.com/twmb/go-mergetyp/mergegen/gen"
)

func main() {
	if err := gen.Gen(new(pkg.MergeGen_%s)); err != nil {
		fmt.Fprintf(os.Stderr, "unable to generate merge: %%v\n", err)
		os.Exit(1)
	}
}
`
