package main

import (
	"fmt"
	"os"

	. "github.com/pergus/editor"
)

func main() {
	var err error

	if len(os.Args) == 2 {
		err = Editor(os.Args[1])

	} else {
		err = Editor("")
	}
	if err != nil {
		fmt.Printf("%v", err)
	}
}
