package main

import (
	"flag"
	"fmt"
	"os"
)

/* saveHelp writes the help text to a file */
func saveHelp(fname string) int {
	/* Open output file */
	f, err := os.Create(fname)
	if err != nil {
		fmt.Printf("Unable to open %v to write help text: %v\n", fname,
			err)
		return -9
	}
	debug("Opened %v for saving help", fname)
	flag.CommandLine.SetOutput(f)
	debug("Set output to %v", f)
	flag.PrintDefaults()
	debug("Saved help text to %v", fname)
	return 0
}
