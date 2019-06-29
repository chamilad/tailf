package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// usage: tailf <initial line count> <filename>
// TODO: keep tailing the file during logrotate
// TODO: parse flags better, consider various permutations of order of flags
// TODO: manage filename flag
// TODO: -h flag - show usage
// TODO: manpage maybe?
// TODO: --version flag
// TODO: instrument and perf test
// TODO: manage error messages
// TODO: checkout magefile as a build system
// TODO: tail multiple files in a dir matching a pattern
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

	fname, err := filepath.Abs(fname)
	handleErrorAndExit(err, "error while converting filenames")

	// check if file exists
	f, err := os.Open(fname)
	// close the handler later
	defer f.Close()
	handleErrorAndExit(err, fmt.Sprintf("file not found: %s", fname))

	// if a line count is provided, rewind cursor
	// the file should be read from the end, backwards
	// TODO: handle f == nil
	log.Println("tailing last lines")
	f = seekBackwardsToLineCount(lcount, f)

	log.Println("creating inotify event")
	// TODO: apparently syscall is deprecated, use sys pkg later
	fd, err := syscall.InotifyInit()
	handleErrorAndExit(err, fmt.Sprintf("error while registering inotify: %s", fname))

	log.Println("adding watch")
	wd, err := syscall.InotifyAddWatch(fd, fname, syscall.IN_MOVE_SELF)
	handleErrorAndExit(err, fmt.Sprintf("error while adding an inotify watch: %s", fname))

	defer func() {
		syscall.InotifyRmWatch(fd, uint32(wd))
	}()

	var com chan string

	go func() {
		for {
			log.Println("overwatch")
			checkInotifyEvents(fd, com)
		}
	}()

	// wait for new lines to appear
	//reader := bufio.NewReader(f)
	// TODO: this forloop fucks shit up, need to use a better method
	//for {
	//	line, err := reader.ReadString('\n')
	//	if err != nil {
	//		if err == io.EOF {
	//			// TODO: sleep for a while? check when perf testing
	//			continue
	//		}
	//
	//		_, _ = fmt.Fprintf(os.Stderr, "error while reading file: %s", err)
	//		break
	//	}
	//
	//	_, _ = fmt.Fprint(os.Stdout, line)
	//}
}

func handleErrorAndExit(e error, msg string) {
	if e != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s: %s\n", msg, e)
		os.Exit(1)
	}
}

func checkInotifyEvents(fd int, com chan<- string) {
	buf := make([]byte, (syscall.SizeofInotifyEvent+syscall.NAME_MAX+1)*10)
	// read from the opened inotify file descriptor, into buf
	log.Println("reading inotify event list")
	// looks like read is blocking until len(buf) is available
	n, err := syscall.Read(fd, buf)
	handleErrorAndExit(err, "error while reading inotify file")

	// check if the read value is 0
	if n <= 0 {
		handleErrorAndExit(errors.New(""), "inotify read resulted in EOF")
	}

	log.Println("==========================read bytes: " + string(buf))

	// read the buffer for all its events
	offset := 0
	for {
		log.Println("in the loop")
		if offset+syscall.SizeofInotifyEvent > n {
			log.Println("no buf left to read")
			return
		}

		var event syscall.InotifyEvent
		err = binary.Read(bytes.NewReader(buf[offset:(offset+syscall.SizeofInotifyEvent+1)]), binary.LittleEndian, &event)
		handleErrorAndExit(err, "error while reading inotify events from the buf")

		if event.Mask == syscall.IN_MOVE_SELF {
			log.Println("file move detected")
			// send message to other thread
			com <- "move"
		}

		offset += syscall.SizeofInotifyEvent + int(event.Len)
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
	var offset int64 = -1

	finfo, err := os.Stat(f.Name())
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error while getting fileinfo: %s", f.Name())
		return nil
	}

	fsize := finfo.Size()

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

	// seek to the found position
	_, err = f.Seek(int64(offset), io.SeekEnd)
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
