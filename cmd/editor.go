package main

import (
	"fmt"
	"os"

	. "github.com/pergus/editor"
)

func main() {
	var err error
	readonly := true

	if len(os.Args) == 2 {
		err = Editor(os.Args[1], readonly)

	} else {
		err = Editor("", readonly)
	}
	if err != nil {
		fmt.Printf("%v", err)
	}
}
