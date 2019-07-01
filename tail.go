package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// usage: tailf <initial line count> <filename>
// TODO: parse flags better, consider various permutations of order of flags
// TODO: manage filename flag
// TODO: -h flag - show usage
// TODO: manpage maybe?
// TODO: --version flag
// TODO: instrument and perf test
// TODO: manage error messages
// TODO: checkout magefile as a build system
// TODO: tail multiple files in a dir matching a pattern
// TODO: maintain all fds and wds in an internal structure and defer to close all
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

	//log.Println("creating inotify event")
	// TODO: apparently syscall is deprecated, use sys pkg later
	fd, err := syscall.InotifyInit()
	handleErrorAndExit(err, fmt.Sprintf("error while registering inotify: %s", fname))

	wd := watchFile(fd, fname)
	//fmt.Printf("wd1: %d", wd)

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
	//log.Println("tailing last lines")
	lastFSize := showLastLines(lcount, f)
	// cursor is at EOF-1

	defer func() {
		removeWatch(fd, wd)
	}()

	// this channel communicates the events
	events := make(chan uint32)

	go checkInotifyEvents(fd, events)

	for {
		select {
		// todo: need to verify if event is for currently watching file handler by comparing wd
		case event := <-events:
			switch event {
			case syscall.IN_MOVE_SELF:
				// file moved, close current file handler and open a new one
				//log.Println("FILE MOVED")
				f.Close()

				// wait for new file to appear
				for {
					_, err := os.Stat(fname)
					if err == nil {
						break
					}

					//log.Println("file not yet appeared")

					// todo exponential backoff, give up after a certain time
					time.Sleep(2 * time.Second)
				}

				// file appeared, open a new file handler
				f, err = os.Open(fname)
				handleErrorAndExit(err, fmt.Sprintf("file not found: %s", fname))

				// remove existing inotify watch and add a new watch for the new file handler
				removeWatch(fd, wd)
				wd = watchFile(fd, fname)
				//fmt.Printf("wd2: %d", wd)
			case syscall.IN_MODIFY:
				// file was written to or truncated, need to determine what happend
				finfo, err := os.Stat(f.Name())
				handleErrorAndExit(err, "error while sizing file during modify event")
				if finfo.Size() > lastFSize {
					//log.Println("FILE WRITTEN")
					// file has been written into, ie "write()"
					lastFSize = showFileContent(f)
				} else if finfo.Size() < lastFSize {
					//log.Println("FILE TRUNCATED")
					// file has been truncated
					f.Seek(0, io.SeekStart)
					lastFSize = showFileContent(f)
				}
			case syscall.IN_DELETE_SELF, syscall.IN_ATTRIB, syscall.IN_IGNORED, syscall.IN_UNMOUNT:
				// in ubuntu, rm sends an IN_ATTRIB possibly because of unlink()
				//log.Println("FILE DELETED, IGNORED, OR UNMOUNTED, TIME TO DIE")
				// file was deleted, exit?
				f.Close()
				os.Exit(0)
			}
		}
	}
}

// removeWatch stops watching a file by removing a given watch
// descriptor from the given inotify file descriptor
func removeWatch(fd int, wd int) (int, error) {
	return syscall.InotifyRmWatch(fd, uint32(wd))
}

// watchFile adds a new inotify watch for a given file at the given
// inotify file descriptor.
// Returns the created watch descriptor
func watchFile(fd int, fname string) int {
	//log.Println("adding watch")
	wd, err := syscall.InotifyAddWatch(
		fd,
		fname,
		syscall.IN_MOVE_SELF|syscall.IN_DELETE_SELF|syscall.IN_ATTRIB|syscall.IN_MODIFY|syscall.IN_UNMOUNT|syscall.IN_IGNORED)
	//syscall.IN_ALL_EVENTS)
	handleErrorAndExit(err, fmt.Sprintf("error while adding an inotify watch: %s", fname))
	return wd
}

// handleErrorAndExit will exit with 1 if there is an error
// todo: crude
func handleErrorAndExit(e error, msg string) {
	if e != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s: %s\n", msg, e)
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
func checkInotifyEvents(fd int, events chan<- uint32) {
	for {
		buf := make([]byte, (syscall.SizeofInotifyEvent+syscall.NAME_MAX+1)*10)
		// read from the opened inotify file descriptor, into buf
		//log.Println("reading inotify event list")
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
		buf := make([]byte, 1)
		n, err := f.Read(buf)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error while reading char at %d: %s", p, err)
			return 0
		}

		if n <= 0 {
			_, _ = fmt.Fprintf(os.Stderr, "no bytes read at %d: %s", p, err)
			return 0
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
