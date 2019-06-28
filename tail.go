package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// usage: tailf <initial line count> <filename>
func main() {
	// args without bin name
	if len(os.Args) == 1 {
		_, _ = fmt.Fprint(os.Stderr, "no file specified to tail")
		os.Exit(1)
	}
	args := os.Args[1:]

	var fname string
	var lcount int

	// check if a line count is provided
	if len(args) == 2 {
		lcount = extractLineCount(args[0])
		fname = args[1]
	} else {
		lcount = 0
		fname = args[0]
	}
	// check if file exists
	f, err := os.Open(fname)
	// close the handler later
	defer f.Close()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "file not found: %s", fname)
		os.Exit(1)
	}

	// if a line count is provided, rewind cursor
	// the file should be read from the end, backwards
	f = seekBackwardsToLineCount(lcount, f)

	// wait for new lines to appear
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
}

// seekBackwardsToLineCount will move the read position of
// the passed file until the specified line count from end
// is met
// Returns the os.File reference which has a rewound cursor
func seekBackwardsToLineCount(lc int, f *os.File) *os.File {
	// line count counter
	l := 0
	// offset counter, negative because counting backwards
	offset := -1

	// loop until lc is passed
	for ; ; offset-- {
		// seek backwards by offset from the end
		p, err := f.Seek(int64(offset), io.SeekEnd)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error while seeking by char at %d: %s", offset, err)
			return nil
		}

		// read one char, a new reader is needed from seeked File ref
		r := bufio.NewReader(f)
		b, err := r.ReadByte()
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error while reading char at %d: %s", p, err)
			return nil
		}

		// check if read char is new line
		s := string(b)
		if s == "\n" {
			l++
			// if line count is passed
			if l > lc {
				// increase the offset by one (to compensate for last
				// read new line
				offset++

				// escape from loop
				break
			}
		}
	}

	_, err := f.Seek(int64(offset), io.SeekEnd)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "end: error while seeking by char at %d: %s", offset, err)
		return nil
	}

	return f
}

// extractLineCount parses the given string to a usable int value
// It can tolerate - prefix
func extractLineCount(s string) int {
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimSpace(s)

	if i, err := strconv.ParseInt(s, 10, 0); err != nil {
		return 0
	} else {
		return int(i)
	}
}
