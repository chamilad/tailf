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

	log.Println("creating inotify event")
	// TODO: apparently syscall is deprecated, use sys pkg later
	fd, err := syscall.InotifyInit()
	handleErrorAndExit(err, fmt.Sprintf("error while registering inotify: %s", fname))

	log.Println("adding watch")
	wd, err := syscall.InotifyAddWatch(
		fd,
		fname,
		syscall.IN_MOVE_SELF|syscall.IN_DELETE_SELF|syscall.IN_ATTRIB|syscall.IN_MODIFY|syscall.IN_UNMOUNT|syscall.IN_IGNORED)
		//syscall.IN_ALL_EVENTS)

	handleErrorAndExit(err, fmt.Sprintf("error while adding an inotify watch: %s", fname))

	//log.Println("seeks")
	//n1, err:= f.Seek(0, io.SeekStart)
	//n2, err := f.Seek(0, io.SeekEnd)
	//n3, err := f.Seek(0, io.SeekCurrent)
	//
	//fmt.Printf("start: %d, end: %d, current: %d", n1, n2, n3)
	//os.Exit(0)

	// if a line count is provided, rewind cursor
	// the file should be read from the end, backwards
	// TODO: handle f == nil
	log.Println("tailing last lines")
	lastFSize := showLastLines(lcount, f)
	// cursor is at EOF-1

	defer func() {
		syscall.InotifyRmWatch(fd, uint32(wd))
	}()

	// this channel communicates the events
	events := make(chan uint32)

	go checkInotifyEvents(fd, events)

	for {
		select {
		case event := <-events:
			switch event {
			case syscall.IN_MOVE_SELF:
				// file moved, close current file handler and open a new one
				log.Println("FILE MOVED")
				f.Close()
				// todo same file may not be available immediately, wait for it?
				f, err = os.Open(fname)
				handleErrorAndExit(err, fmt.Sprintf("file not found: %s", fname))
			case syscall.IN_MODIFY:
				// file was written to or truncated, need to determine what happend
				finfo, err := os.Stat(f.Name())
				handleErrorAndExit(err, "error while sizing file during modify event")
				if finfo.Size() > lastFSize {
					log.Println("FILE WRITTEN")
					// file has been written into, ie "write()"
					lastFSize = showFileContent(f)
				} else if finfo.Size() < lastFSize {
					log.Println("FILE TRUNCATED")
					// file has been truncated
					f.Seek(0, io.SeekStart)
					lastFSize = showFileContent(f)
				}
			case syscall.IN_DELETE_SELF, syscall.IN_ATTRIB, syscall.IN_IGNORED, syscall.IN_UNMOUNT:
				// in ubuntu, rm sends an IN_ATTRIB possibly because of unlink()
				log.Println("FILE DELETED, IGNORED, OR UNMOUNTED, TIME TO DIE")
				// file was deleted, exit?
				f.Close()
				os.Exit(0)
			//default:
				//h := fmt.Sprintf("%X", event)
				//log.Printf("event received: %s\n", h)
				//
				//h = fmt.Sprintf("%X", syscall.IN_DELETE_SELF)
				//log.Printf("delete self for comparison: %s\n", h)
				//
				//fmt.Printf("event == delete_Self : %v\n", event == syscall.IN_DELETE_SELF)
			}
		}
	}
}

func handleErrorAndExit(e error, msg string) {
	if e != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s: %s\n", msg, e)
		os.Exit(1)
	}
}

func checkInotifyEvents(fd int, events chan<- uint32) {
	for {
		buf := make([]byte, (syscall.SizeofInotifyEvent+syscall.NAME_MAX+1)*10)
		// read from the opened inotify file descriptor, into buf
		log.Println("reading inotify event list")
		// read is blocking until len(buf) is available
		n, err := syscall.Read(fd, buf)
		handleErrorAndExit(err, "error while reading inotify file")

		// check if the read value is 0
		if n <= 0 {
			handleErrorAndExit(errors.New(""), "inotify read resulted in EOF")
		}

		// read the buffer for all its events
		offset := 0
		for {
			if offset+syscall.SizeofInotifyEvent > n {
				break
			}

			// unmarshal to struct
			var event syscall.InotifyEvent
			err = binary.Read(bytes.NewReader(buf[offset:(offset+syscall.SizeofInotifyEvent+1)]), binary.LittleEndian, &event)
			handleErrorAndExit(err, "error while reading inotify events from the buf")

			// notify the waiting consumer of the event
			// TODO buffer and gather all modify events to one to avoid spamming the consumer thread
			events <- event.Mask

			// move the window and read the next event
			offset += syscall.SizeofInotifyEvent + int(event.Len)
		}
	}
}

// showLastLines will move the read position of the passed
// file until the specified line count from end is met
// Returns the os.File reference which has a rewound cursor
func showLastLines(lc int, f *os.File) int64 {
	// line count counter
	l := 0
	// offset counter, negative because counting backwards
	var offset int64 = -1

	finfo, err := os.Stat(f.Name())
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error while getting fileinfo: %s", f.Name())
		return 0
	}

	fsize := finfo.Size()

	if fsize == -0 {
		return 0
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
			_, _ = fmt.Fprintf(os.Stderr, "error while seeking by char at %d: %s", offset, err)
			return 0
		}

		// read one char, a new reader is needed from seeked File ref
		r := bufio.NewReader(f)
		b, err := r.ReadByte()
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error while reading char at %d: %s", p, err)
			return 0
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
		return 0
	}

	// show the lines up to EOF
	return showFileContent(f)
}

func showFileContent(f *os.File) int64 {
	// get current position
	c, err := f.Seek(0, io.SeekCurrent)
	handleErrorAndExit(err, "error while getting current cursor pos")

	finfo, err := os.Stat(f.Name())
	handleErrorAndExit(err, "error while getting filesize")

	// len to read is total file size - current position
	buflen := finfo.Size() - c

	buf := make([]byte, buflen)
	n, err := f.Read(buf)
	handleErrorAndExit(err, "couldn't read line count")
	if n <= 0 {
		fmt.Println("reading file returned 0 or less bytes")
	}

	_, _ = fmt.Fprint(os.Stdout, string(buf[:n]))

	return finfo.Size()
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
