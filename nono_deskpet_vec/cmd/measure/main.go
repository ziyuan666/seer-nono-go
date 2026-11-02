package main

import (
	"fmt"
	"os"

	"nonovec/pkg/sprite"
)

func main() {
	if err := sprite.MeasureAllFrames(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
