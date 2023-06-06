package main

import (
	"os"

	. "github.com/pergus/editor"
)

func main() {
	if len(os.Args) == 2 {
		Editor(os.Args[1])
	} else {
		Editor("")
	}
}
