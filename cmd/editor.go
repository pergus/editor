package main

import (
	"fmt"
	"os"

	. "github.com/pergus/editor"
)

func main() {
	var err error
	readonly := false

	if len(os.Args) == 2 {
		err = Editor(os.Args[1], readonly, "keymap.json")

	} else {
		err = Editor("", readonly, "keymap.json")
	}
	if err != nil {
		fmt.Printf("%v", err)
	}
}
