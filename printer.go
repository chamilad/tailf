package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type PrintContent struct {
	filename string
	content  string
	color    func(string) string
}

type ContentPrinter struct {
	multiFile bool
}

// start initiates a loop that will constantly watch for print events
// and output them using the information provided
func (p *ContentPrinter) start(contents <-chan *PrintContent, done <-chan bool) {
	for {
		select {
		case c := <-contents:
			debug(fmt.Sprintf("printer: received print content for file %s", c.filename))
			p.print(c)
		case <-done:
			debug("printer: received notice to shutdown")
			return
		}
	}
}

// print prints the given PrintContent object to stdout
// Whether a color should be used and the filename should be prefixed
// is determined by the PrintContent config
func (p *ContentPrinter) print(c *PrintContent) {
	debug(fmt.Sprintf("printer: printing line for %s", c.filename))
	if p.multiFile {
		lines := strings.Split(strings.Trim(c.content, "\n"), "\n")
		for _, l := range lines {
			bfn := filepath.Base(c.filename)
			_, _ = fmt.Fprint(os.Stdout, fmt.Sprintf("%s %s\n", c.color(bfn+" => "), l))
		}
	} else {
		_, _ = fmt.Fprint(os.Stdout, c.content)
	}
}
