package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
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
// TODO: manage error messages
// TODO: checkout magefile as a build system

var (
	outputColors = [5]func(string) string{red, yellow, blue, magenta, green}
)

// structure to collect tailing file info
type FileTailer struct {
	name     string
	file     *os.File
	fileSize int64
	wd       uint32
	fd       int
	color    func(string) string
}

type EventReader struct {
	fd int
}

type PrintContent struct {
	filename string
	content  string
	color    func(string) string
}

type ContentPrinter struct {
	multiFile bool
}

func (p *ContentPrinter) start(contents <-chan *PrintContent, done <-chan bool) {
	for {
		select {
		case c := <-contents:
			debug(fmt.Sprintf("received print content for file %s", c.filename))
			p.print(c)
		case <-done:
			return
		}
	}
}

func (p *ContentPrinter) print(c *PrintContent) {
	debug(fmt.Sprintf("printing line for %s", c.filename))
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

func newFileTailer(fd int, name string, c func(string) string) *FileTailer {
	t := &FileTailer{
		name:  name,
		fd:    fd,
		color: c,
	}

	return t
}

// refresh closes the existing filehandler and opens a new one
// Useful when the current filehandler goes stale, when ex:
// the file gets deleted but the same file is recreated after
// sometime
func (t *FileTailer) refresh() {
	t.unregisterWatch()
	_ = t.file.Close()

	// file appeared, open a new file handler
	f, err := os.Open(t.name)
	// todo: throw these errors
	handleErrorAndExit(err, fmt.Sprintf("error while reopening file: %s", t.name))

	t.file = f
	t.registerWatch()
}

func (t *FileTailer) openFile() {
	f, err := os.Open(t.name)
	handleErrorAndExit(err, fmt.Sprintf("error while opening file: %s", t.name))

	t.file = f
}

func (t *FileTailer) registerWatch() {
	debug(fmt.Sprintf("adding watch for file %s", t.file.Name()))
	wd, err := syscall.InotifyAddWatch(
		t.fd,
		t.file.Name(),
		syscall.IN_MOVE_SELF|syscall.IN_DELETE_SELF|syscall.IN_ATTRIB|
			syscall.IN_MODIFY|syscall.IN_UNMOUNT|syscall.IN_IGNORED)
	//syscall.IN_ALL_EVENTS)
	handleErrorAndExit(
		err,
		fmt.Sprintf(
			"error while adding an inotify watch: %s",
			t.file.Name()))

	t.wd = uint32(wd)
	debug(fmt.Sprintf("wd for watched file: %d", t.wd))
}

func (t *FileTailer) unregisterWatch() {
	debug(fmt.Sprintf("removing watch: %d", t.wd))
	syscall.InotifyRmWatch(t.fd, t.wd)
}

func (t *FileTailer) watch(events <-chan syscall.InotifyEvent, content chan<- *PrintContent, done <-chan bool) {
	//ConLoop:
	for {
		select {
		case <-done:
			debug("received notice to shutdown")
			//break ConLoop
			return
		case event := <-events:
			// is this an event for the file we are currently
			// interested in?
			if uint32(event.Wd) == t.wd {
				switch event.Mask {
				case syscall.IN_MOVE_SELF:
					// file moved, close current file handler and
					// open a new one
					debug("FILE MOVED")

					// wait for new file to appear
					for {
						_, err := os.Stat(t.file.Name())
						if err == nil {
							break
						}

						debug("file not yet appeared")

						// todo exponential backoff, give up after a certain time
						// there is a window to miss some events,
						// during timeout if the file is created and
						// written to, we miss those events
						// those possible writes are covered by the
						// readContentToEOF() done later after creating
						// a new wd
						time.Sleep(2 * time.Second)
					}

					// file appeared, open a new file handler
					t.refresh()

					// refresh inotify watch
					//owd := cwd
					// add a new watch

					//nwd := watchFile(fd, tf.name)
					//mark it to be closed during shutdown
					//wds = append(wds, nwd)
					// set current wd to the new wd
					//cwd = nwd

					// show any content created during the timeout
					// also reset last read file size
					content <- t.readFile()
					//content, rsize := readContentToEOF(t.file)
					//t.fileSize = rsize
					//if len(files) > 1 {
					//	printContentWithFileName(t, content)
					//} else {
					//	printContent(content)
					//}

					// remove existing inotify watch
					//_, _ = removeWatch(fd, owd)
				case syscall.IN_MODIFY:
					// file was written to or truncated, need to determine what happened
					finfo, err := os.Stat(t.file.Name())
					handleErrorAndExit(err, "error while sizing file during modify event")

					if finfo.Size() < t.fileSize {
						debug("FILE TRUNCATED")

						// file has been truncated, go to the beginning
						_, _ = t.file.Seek(0, io.SeekStart)
					} else if finfo.Size() > t.fileSize {
						// file has been written into, ie "write()"
						// no need to seek anywhere
						debug("FILE WRITTEN")

						//content, rsize := readContentToEOF(t.file)
						//t.fileSize = rsize
						//if len(files) > 1 {
						//	printContentWithFileName(t, content)
						//} else {
						//	printContent(content)
						//}
					}

					content <- t.readFile()
					//content, rsize := readContentToEOF(t.file)
					//t.fileSize = rsize
					//if len(files) > 1 {
					//	printContentWithFileName(t, content)
					//} else {
					//	printContent(content)
					//}
				case syscall.IN_ATTRIB:
					debug(fmt.Sprintf("ATTRIB received: %d", event.Wd))

					// rm sends an IN_ATTRIB possibly because of unlink()
					// check if file deleted and not any other
					// IN_ATTRIB source
					_, err := os.Stat(t.file.Name())
					if err != nil {
						debug("FILE DELETED, TIME TO DIE")
						// let defers be executed. os.Exit() would not allow that
						//break ConLoop
						return
					}
				case syscall.IN_DELETE_SELF, syscall.IN_IGNORED, syscall.IN_UNMOUNT:
					debug("FILE DELETED, IGNORED, OR UNMOUNTED, TIME TO DIE")

					// file was deleted, exit
					//_ = tf.file.Close()
					// let defers be executed. os.Exit() would not allow that
					//break ConLoop
					return
				}
			}
		}
	}
}

func (t *FileTailer) readFile() *PrintContent {
	// get current position
	curPos, err := t.file.Seek(0, io.SeekCurrent)
	handleErrorAndExit(err, "error while getting current cursor pos")

	finfo, err := os.Stat(t.file.Name())
	handleErrorAndExit(err, "error while getting filesize")

	// len to read is total file size - current position
	t.fileSize = finfo.Size()
	//t.fileSize = fsize

	buflen := t.fileSize - curPos

	buf := make([]byte, buflen)
	n, err := t.file.Read(buf)
	handleErrorAndExit(err, "couldn't read line count")
	if n <= 0 {
		debug("reading file returned 0 or less bytes")
	}

	debug(fmt.Sprintf("read %d bytes from %s", buflen, t.file.Name()))

	//debug(fmt.Sprintf("sending back: %s", string(buf[:n])))
	return &PrintContent{
		content:  string(buf[:n]),
		filename: t.file.Name(),
		color:    t.color,
	}
}

func (t *FileTailer) close() {
	debug(fmt.Sprintf("closing file tailer %s", filepath.Base(t.name)))
	t.unregisterWatch()
	t.file.Close()
}

//func (t *FileTailer) stop() {
//
//}

func (e *EventReader) start(events chan<- syscall.InotifyEvent) {
	for {
		buf := make([]byte, (syscall.SizeofInotifyEvent+syscall.NAME_MAX+1)*10)

		// read from the opened inotify file descriptor, into buf
		// read() is blocking until some data is available
		debug("reading inotify event list")
		n, err := syscall.Read(e.fd, buf)
		handleErrorAndExit(err, "error while reading inotify file")

		// check if the read value is 0
		if n <= 0 {
			printErr("inotify read resulted in EOF")
		}

		// read the buffer for all its events
		offset := 0
		for {
			if offset+syscall.SizeofInotifyEvent > n {
				debug("reached end of inotify buffer")
				break
			}

			// unmarshal to struct
			var event syscall.InotifyEvent
			err = binary.Read(bytes.NewReader(buf[offset:(offset+syscall.SizeofInotifyEvent+1)]), binary.LittleEndian, &event)
			handleErrorAndExit(err, "error while reading inotify events from the buf")

			debug(fmt.Sprintf("read inotify event for wd %d", event.Wd))

			// notify the waiting consumer of the event
			// TODO buffer and gather all modify events to one to avoid spamming the consumer thread
			events <- event

			// move the window and read the next event
			offset += syscall.SizeofInotifyEvent + int(event.Len)
		}
	}
}

func main() {
	debug("processing input")
	// watch descriptors to be closed
	//var wds []uint32

	// line count to watch with
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
			// shadowing, so using tf to temp store string value
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

	debug(fmt.Sprintf("%d files to tail", len(files)))

	// limit number of files to 5 to reduce clutter
	if len(files) > 5 {
		handleErrorAndExit(errors.New("too many files to tail"),
			"max file limit is 5, would be too much information for ya")
	}

	// if not tail count is provided, set default tail count to 5,
	// awkward otherwise
	if lcount == 0 {
		lcount = 5
	}

	debug("registering signal trap")
	// channels to trap signals
	sigs := make(chan os.Signal, 1)
	// channel to ping workers to shutdown when a signal is received
	done := make(chan bool, 1)
	// this channel communicates the events
	events := make(chan syscall.InotifyEvent)
	// channel to pass read content from tailer to printer
	content := make(chan *PrintContent)

	// subscribe for 9 and 15 signals
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// wait async for signals
	go func() {
		debug("waiting for signals")
		<-sigs
		debug("signal received")

		// close the channel to broadcast, otherwise only one listener
		// receives the message
		close(done)
	}()

	debug("creating inotify event")
	// TODO: apparently syscall is deprecated, use sys pkg later
	// TODO: check if fd opened below needs to be closed
	fd, err := syscall.InotifyInit()
	handleErrorAndExit(err, "error while inotify init")

	tailers := make([]*FileTailer, 0)

	// not passing values to be evaled later
	defer func() {
		debug(fmt.Sprintf("defer func, %d file tailers", len(tailers)))
		for _, t := range tailers {
			// schedule open file handlers to be closed
			debug(fmt.Sprintf("defer: closing file tailer %s", t.file.Name()))
			t.close()

			// schedule open wds to be closed during shutdown
			//debug(fmt.Sprintf("defer: closing wd %d", t.wd))
			//t.unregisterWatch()
			//_, _ = removeWatch(fd, t.wd)
		}
	}()

	// start printer early
	printer := &ContentPrinter{
		multiFile: len(files) > 1,
	}
	go printer.start(content, done)

	// for each filename given,
	// 1. registerWatch an inotify watch
	// 2. spawn an inotify event watcher
	// 3. spawn an event consumer
	// todo: go func content of this loop, otherwise last lines are printed one after the other
	for i, fname := range files {
		debug(fmt.Sprintf("registering tailer for %s", fname))

		// create a worker
		t := newFileTailer(fd, fname, outputColors[i])
		tailers = append(tailers, t)

		// create a file handler
		t.openFile()

		// watch watching the file
		t.registerWatch()

		// if a line count is provided, rewind cursor
		// the file should be read from the end, backwards
		debug("tailing last lines")
		seekBackwardsByLineCount(lcount, t.file)

		// read content and queue to be printed
		content <- t.readFile()

		// read from the rewound position to EOF
		//content, lastFSize := readContentToEOF(t.file)
		// cursor is at EOF-1
		//
		//if len(files) > 1 {
		//	printContentWithFileName(t, content)
		//} else {
		//	printContent(content)
		//}

		// start consumer loop
		go t.watch(events, content, done)
	}

	// start an Inotify event reader loop
	eventReader := &EventReader{
		fd: fd,
	}
	go eventReader.start(events)

	// holding the main thread until shutdown
	<-done
	debug("received notice to shutdown")
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

	//tf := &FileTailer{
	//	name: fname,
	//}

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

// printContent writes the given string to stdout
//func printContent(s string) {
//	_, _ = fmt.Fprint(os.Stdout, s)
//}

// printContentWithFileName prints the given content to stdout,
// prefixing each line with the base name of the given filename
// the prefixing filename is colored with a distinctive color
//func printContentWithFileName(f *FileTailer, content string) {
//	debug(fmt.Sprintf("printing line for %s", f.name))
//	lines := strings.Split(strings.Trim(content, "\n"), "\n")
//	for _, l := range lines {
//		bfn := filepath.Base(f.name)
//		printContent(fmt.Sprintf("%s %s\n", f.color(bfn+" => "), l))
//	}
//}

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

// removeWatch stops watching a file by removing a given watch
// descriptor from the given inotify file descriptor
//func removeWatch(fd int, wd uint32) (int, error) {
//	debug(fmt.Sprintf("removing watch: %d", wd))
//	return syscall.InotifyRmWatch(fd, wd)
//}

// watchFile adds a new inotify watch for a given file at the given
// inotify file descriptor.
// Returns the created watch descriptor
//func watchFile(fd int, fname string) uint32 {
//	debug("adding watch")
//	wd, err := syscall.InotifyAddWatch(
//		fd,
//		fname,
//		syscall.IN_MOVE_SELF|syscall.IN_DELETE_SELF|syscall.IN_ATTRIB|
//			syscall.IN_MODIFY|syscall.IN_UNMOUNT|syscall.IN_IGNORED)
//	//syscall.IN_ALL_EVENTS)
//	handleErrorAndExit(err, fmt.Sprintf("error while adding an inotify watch: %s", fname))
//
//	uwd := uint32(wd)
//	debug(fmt.Sprintf("wd for watched file: %d", uwd))
//	return uwd
//}

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

// checkInotifyEvents runs an infinite loop reading the given inotify
// file descriptor. The read() syscall is a blocking one until any data
// is present. Once the inotify events are present, the events are
// unmarshalled and the event mask is communicated to the consumer
// At the moment, the read() call could close improperly if the main
// thread gives out. Need a way to timeout based on a notification
// from the main thread.
//func checkInotifyEvents(fd int, events chan<- syscall.InotifyEvent) {
//	for {
//		buf := make([]byte, (syscall.SizeofInotifyEvent+syscall.NAME_MAX+1)*10)
//
//		// read from the opened inotify file descriptor, into buf
//		// read() is blocking until some data is available
//		debug("reading inotify event list")
//		n, err := syscall.Read(fd, buf)
//		handleErrorAndExit(err, "error while reading inotify file")
//
//		// check if the read value is 0
//		if n <= 0 {
//			printErr("inotify read resulted in EOF")
//		}
//
//		// read the buffer for all its events
//		offset := 0
//		for {
//			if offset+syscall.SizeofInotifyEvent > n {
//				debug("reached end of inotify buffer")
//				break
//			}
//
//			// unmarshal to struct
//			var event syscall.InotifyEvent
//			err = binary.Read(bytes.NewReader(buf[offset:(offset+syscall.SizeofInotifyEvent+1)]), binary.LittleEndian, &event)
//			handleErrorAndExit(err, "error while reading inotify events from the buf")
//
//			debug(fmt.Sprintf("read inotify event for wd %d", event.Wd))
//
//			// notify the waiting consumer of the event
//			// TODO buffer and gather all modify events to one to avoid spamming the consumer thread
//			events <- event
//
//			// move the window and read the next event
//			offset += syscall.SizeofInotifyEvent + int(event.Len)
//		}
//	}
//}

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
		// check if we are past the file watch
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

// readContentToEOF reads the given file from the current cursor
// position to the end of file.
// Returns the read content and the file size at the time of read
//func readContentToEOF(f *os.File) (string, int64) {
//// get current position
//curPos, err := f.Seek(0, io.SeekCurrent)
//handleErrorAndExit(err, "error while getting current cursor pos")
//
//finfo, err := os.Stat(f.Name())
//handleErrorAndExit(err, "error while getting filesize")
//
//// len to read is total file size - current position
//fsize := finfo.Size()
//buflen := fsize - curPos
//
//buf := make([]byte, buflen)
//n, err := f.Read(buf)
//handleErrorAndExit(err, "couldn't read line count")
//if n <= 0 {
//	debug("reading file returned 0 or less bytes")
//}
//
//debug(fmt.Sprintf("read %d bytes from %s", buflen, f.Name()))
//
//return string(buf[:n]), fsize
//}

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

// red colors the given string to red, with ANSI/VT100 88/256
// color sequences
func red(s string) string {
	return fmt.Sprintf("\x1b[1;31m%s\x1b[0m", s)
}

// yellow colors the given string to yellow, with ANSI/VT100 88/256
// color sequences
func yellow(s string) string {
	return fmt.Sprintf("\x1b[1;33m%s\x1b[0m", s)
}

// blue colors the given string to blue, with ANSI/VT100 88/256
// color sequences
func blue(s string) string {
	return fmt.Sprintf("\x1b[1;34m%s\x1b[0m", s)
}

// magenta colors the given string to magenta, with ANSI/VT100 88/256
// color sequences
func magenta(s string) string {
	return fmt.Sprintf("\x1b[1;35m%s\x1b[0m", s)
}

// green colors the given string to green, with ANSI/VT100 88/256
// color sequences
func green(s string) string {
	return fmt.Sprintf("\x1b[1;32m%s\x1b[0m", s)
}
