package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
)

// usage: tailf <filename>
func main() {
	// args without bin name
	if len(os.Args) == 1 {
		_, _ = fmt.Fprint(os.Stderr, "no file specified to tail")
		os.Exit(1)
	}
	args := os.Args[1:]

	fname := args [0]
	// check if file exists
	f, err := os.Open(fname)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "file not found: %s", fname)
		os.Exit(1)
	}

	reader := bufio.NewReader(f)
	for {

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				continue
			}

			_, _ = fmt.Fprintf(os.Stderr, "error while reading file: %s", err)
			break
		}

		_, _ = fmt.Fprint(os.Stdout, line)
	}

	os.Exit(0)
}
