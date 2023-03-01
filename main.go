package main

import (
	. "editor/editor"
	"os"
)

func main() {
	if len(os.Args) == 2 {
		Editor(os.Args[1])
	} else {
		Editor("")
	}
}
