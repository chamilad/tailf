package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	// show debug information, for dev cycles
	DEBUG_MODE = false
)

// usage: tailf <filename>
//        tailf paths ...<file paths> // tail multiple files
//        tailf paths ...<path/wildcard_pattern> // tail multiple files
// 		  tailf <path>/<wildcard_pattern> // tail files that match
// 	 	                                     this pattern
//        tailf -<initial line count> <all above usages>
//        tailf -h | --help
//        tailf -v | --version

// TODO: manpage maybe?
// TODO: instrument and perf test
// TODO: error handling should be meaningful
// TODO: checkout magefile as a build system

var (
	outputColors = [5]func(string) string{red, yellow, blue, magenta, green}
)

func main() {
	debug("main: processing input")
	// line count to start with
	var lcount int

	// args without bin name
	if len(os.Args) == 1 {
		printErr("no file specified to tail")
		os.Exit(1)
	}

	args := os.Args[1:]

	// list of files to tail
	files := make([]string, 0)

	// parse arguments
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			// one of init count, help, or version

			// is it the help flag
			if arg == "-h" || arg == "--help" {
				showUsageAndExit()
			}

			// is it the version flag
			if arg == "-v" || arg == "--version" {
				showVersionAndExit()
			}

			// is it the line count flag
			lc, err := readLineCountArg(arg)
			handleErrorAndExit(err, fmt.Sprintf("unknown flag: %s", arg))

			// it is the line count flag
			lcount = lc
		} else {
			// should be either a single file name, multiple filenames or a file pattern
			fname, err := parseFileName(arg)
			if err != nil {
				printErr(fmt.Sprintf("file not found: %s", arg))
				showUsageAndExit()
			}

			files = append(files, fname)
		}
	}

	// if there are no files to tail, exit
	if len(files) == 0 {
		handleErrorAndExit(errors.New("no files provided to tail"), "")
	}

	debug(fmt.Sprintf("main: %d files to tail", len(files)))

	// limit number of files to 5 to reduce clutter
	if len(files) > 5 {
		handleErrorAndExit(errors.New("too many files to tail"),
			"max file limit is 5, would be too much information for ya")
	}

	// if no tail count is provided, set default tail count to 5,
	// awkward otherwise
	if lcount == 0 {
		lcount = 5
	}

	// channel to ping workers to shutdown when a signal is received
	done := make(chan bool, 1)
	// this channel communicates the events
	events := make(chan syscall.InotifyEvent)
	// channel to pass read content from tailer to printer
	content := make(chan *PrintContent)

	debug("main: registering signal trap")
	// channels to trap signals
	sigs := make(chan os.Signal, 1)
	// subscribe for 9 and 15 signals
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// wait async for signals
	go func() {
		debug("sig: waiting for signals")
		<-sigs
		debug("sig: signal received")

		// close the channel to broadcast, otherwise only one listener
		// receives the message
		close(done)
		debug("sig: sent message to shutdown")
	}()

	// start printer early
	printer := &ContentPrinter{
		// this flag determines if the filename is prefixed on the line
		// printing
		multiFile: len(files) > 1,
	}
	go printer.start(content, done)

	// start an Inotify event reader loop
	// though there are no consumers at this point, the events will be
	// collected in the channel
	eventReader := &EventReader{}
	eventReader.init()
	go eventReader.start(events)

	dispatch := &Dispatch{
		tailers: make(map[uint32]*FileTailer),
	}

	defer func() {
		debug("defer: shutting down dispatch")
		dispatch.shutdown()
		debug("defer: done")
	}()

	// for each filename given,
	// 1. register an inotify watch
	// 2. spawn an event consumer
	// todo: go func content of this loop, otherwise last lines are printed one after the other
	for i, fname := range files {
		debug(fmt.Sprintf("main: registering tailer for %s", fname))

		// create a worker
		t := newFileTailer(eventReader.fd, fname, content, outputColors[i])

		// create a file handler
		t.openFile()

		// start watching the file
		err := t.registerWatch()
		// if the file can't be watched, crash and burn
		if err != nil {
			panic(err)
		}

		dispatch.registerTailer(t.wd, t)

		// if a line count is provided, rewind cursor
		// the file should be read from the end, backwards
		debug("tailing last lines")
		seekBackwardsByLineCount(lcount, t.file)

		// read from the rewound position to EOF and queue to be
		// printed
		content <- t.readFile()
	}

	dispatch.start(events, done)

	// holding the main thread until shutdown
	<-done
	debug("main: received notice to shutdown")
}

// parseFileName accepts a string argument and checks to see if the
// file with the absolute path exists or not
// Returns the absoulte filename and an error if the file doesn't exist
func parseFileName(s string) (string, error) {
	// todo: expand by wildcards,
	//  ? - any single char
	//  * - any multiple chars
	//  [] - list or range of chars
	//  {} - wildcard or exact name terms
	//  [!] - not []
	//  \ - escape
	//  NOTE: not urgent, can work with tools like find
	fname, err := filepath.Abs(s)
	handleErrorAndExit(err, "error while converting filenames")

	// check if file exists
	_, err = os.Stat(fname)
	handleErrorAndExit(err, fmt.Sprintf("file not found: %s", fname))

	return fname, nil
}

// showVersionAndExit shows version details
func showVersionAndExit() {
	printErr("version details will appear in the future")
	os.Exit(0)
}

// showUsageAndExit <- take a wild guess
func showUsageAndExit() {
	printErr("usage details will appear in the future")
	os.Exit(0)
}

// printErr prints the given message to stderr
func printErr(s string) {
	_, _ = fmt.Fprintf(os.Stderr, "%s\n", s)
}

// debug prints the given message to stderr only if the DEBUG_MODE is
// true
func debug(s string) {
	if DEBUG_MODE {
		printErr(s)
	}
}

// handleErrorAndExit will exit with 1 if there is an error
func handleErrorAndExit(e error, msg string) {
	if e != nil {
		printErr(fmt.Sprintf("%s: %s\n", msg, e))
		// os.Exit() here will not run defers
		// tried sending signals, however the receivers do not kick
		// into action soon enough in some cases. Routines that manage
		// resources should take care to carefully release them without
		// depending on defer funcs too much.
		os.Exit(1)
	}
}

// seekBackwardsByLineCount will move the read position of the passed
// file until the specified line count from end is met
// Returns the os.File reference which has a rewound cursor
func seekBackwardsByLineCount(lc int, f *os.File) {
	// line count counter
	l := 0
	// offset counter, negative because counting backwards
	var offset int64 = -1

	finfo, err := os.Stat(f.Name())
	if err != nil {
		printErr(fmt.Sprintf("error while getting fileinfo: %s", f.Name()))
		//return 0
	}

	fsize := finfo.Size()

	if fsize == 0 {
		debug("file has no content to show")
		//return 0
	}

	// loop until lc is passed
	for ; ; offset-- {
		// check if we are past the file start
		if offset+fsize == 0 {
			// if so, return this position, there's no room to backup
			break
		}

		// seek backwards by offset from the end
		p, err := f.Seek(int64(offset), io.SeekEnd)
		if err != nil {
			printErr(fmt.Sprintf("error while seeking by char at %d: %s", offset, err))
			//return 0
		}

		// read one char, a new reader is needed from seeked File ref
		buf := make([]byte, 1)
		n, err := f.Read(buf)
		if err != nil {
			printErr(fmt.Sprintf("error while reading char at %d: %s", p, err))
			//return 0
		}

		if n <= 0 {
			printErr(fmt.Sprintf("no bytes read at %d: %s", p, err))
			//return 0
		}

		// check if read char is new line
		s := string(buf)
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

	// seek to the found position
	_, err = f.Seek(int64(offset), io.SeekEnd)
	if err != nil {
		printErr(fmt.Sprintf("end: error while seeking by char at %d: %s", offset, err))
		//return 0
	}

	// show the lines up to EOF
	//return readContentToEOF(file)
}

// readLineCountArg parses the given string to a usable int value
// It can tolerate - prefix
func readLineCountArg(s string) (int, error) {
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimSpace(s)

	if i, err := strconv.ParseInt(s, 10, 0); err != nil {
		return 0, err
	} else {
		return int(i), nil
	}
}
