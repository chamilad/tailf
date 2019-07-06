package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// structure to collect tailing file info
type FileTailer struct {
	name     string
	file     *os.File
	fileSize int64
	wd       uint32
	fd       int
	contentQ chan<- *PrintContent
	color    func(string) string
}

func newFileTailer(fd int, name string, content chan<- *PrintContent, c func(string) string) *FileTailer {
	t := &FileTailer{
		name:     name,
		fd:       fd,
		contentQ: content,
		color:    c,
	}

	return t
}

// refresh closes the existing filehandler and opens a new one. It
// also closes the Inotify watch opened for the older filehandler
// and opens a new one.
// Useful when the current filehandler goes stale, when ex:
// the file gets deleted but the same file is recreated after
// sometime
func (t *FileTailer) refresh() error {
	t.unregisterWatch()
	_ = t.file.Close()

	// file appeared, open a new file handler
	f, err := os.Open(t.name)
	// todo: throw these errors
	//handleErrorAndExit(err, fmt.Sprintf("error while reopening file: %s", t.name))
	if err != nil {
		return err
	}

	t.file = f
	err = t.registerWatch()
	//if err != nil {
	//	return err
	//}

	return nil
}

//
func (t *FileTailer) openFile() {
	f, err := os.Open(t.name)
	handleErrorAndExit(err, fmt.Sprintf("error while opening file: %s", t.name))

	t.file = f
}

// registerWatch adds an Inotify watch on the file currently in use
func (t *FileTailer) registerWatch() error {
	debug(fmt.Sprintf("adding watch for file %s under %d", t.file.Name(), t.fd))
	wd, err := syscall.InotifyAddWatch(
		t.fd,
		t.file.Name(),
		syscall.IN_MOVE_SELF|syscall.IN_DELETE_SELF|syscall.IN_ATTRIB|
			syscall.IN_MODIFY|syscall.IN_UNMOUNT|syscall.IN_IGNORED)
	//syscall.IN_ALL_EVENTS)
	//if err != nil {
	//	return errors.New(fmt.Sprintf("cannot watch file %s", t.file.Name()))
	//}
	handleErrorAndExit(
		err,
		fmt.Sprintf(
			"error while adding an inotify watch: %s",
			t.file.Name()))

	t.wd = uint32(wd)
	debug(fmt.Sprintf("wd for watched file: %d", t.wd))

	return nil
}

// unregisterWatch removes the Inotify watch
func (t *FileTailer) unregisterWatch() {
	debug(fmt.Sprintf("removing watch: %d", t.wd))
	syscall.InotifyRmWatch(t.fd, t.wd)
}

// start starts an infinite loop to wait on either InotifyEvents from
// the EventReader or for the done channel to be closed (which
// indicates a shutdown). On interesting events, it reads the file and
// pushes content to content channel to be printed by a printer
// This is expected to be run as a goroutine
func (t *FileTailer) processEvent(e syscall.InotifyEvent) {
	debug(fmt.Sprintf("tailer %d: received event to process %d", t.wd, e.Wd))

	switch e.Mask {
	case syscall.IN_MODIFY:
		// file was written to or truncated, need to determine what happened
		finfo, err := os.Stat(t.file.Name())
		handleErrorAndExit(err, "error while sizing file during modify event")

		if finfo.Size() < t.fileSize {
			debug(fmt.Sprintf("tailer %d: FILE TRUNCATED", t.wd))

			// file has been truncated, go to the beginning
			_, _ = t.file.Seek(0, io.SeekStart)
		} else if finfo.Size() > t.fileSize {
			// file has been written into, ie "write()"
			// no need to seek anywhere
			debug(fmt.Sprintf("tailer %d: FILE WRITTEN", t.wd))
		}

		// read and print content
		t.contentQ <- t.readFile()
	case syscall.IN_MOVE_SELF:
		// file moved, close current file handler and
		// open a new one
		debug(fmt.Sprintf("tailer %d: FILE MOVED", t.wd))

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

		// file appeared, open a new file handler and
		// refresh Inotify watch
		err := t.refresh()
		if err != nil {
			return
		}

		// show any content created during the timeout
		// also reset last read file size
		t.contentQ <- t.readFile()
	case syscall.IN_ATTRIB:
		debug(fmt.Sprintf("tailer %d: ATTRIB received: %d", t.wd, e.Wd))

		// rm sends an IN_ATTRIB possibly because of unlink()
		// check if file deleted and not any other
		// IN_ATTRIB source
		_, err := os.Stat(t.file.Name())
		if err != nil {
			debug(fmt.Sprintf("tailer %d: FILE DELETED, TIME TO DIE", t.wd))
			// end the watch cycle, and possibly the
			// invoking goroutine
			return
		}
	case syscall.IN_DELETE_SELF, syscall.IN_IGNORED, syscall.IN_UNMOUNT:
		debug(fmt.Sprintf("tailer %d: FILE DELETED, IGNORED, OR UNMOUNTED, TIME TO DIE", t.wd))

		// end the watch cycle, and possibly the
		// invoking goroutine
		return
	}
}

// readFile reads the file from the current cursor position to the end
// of file. The current file size is updated at the same time of the
// read.
// Returns a PrintContent struct with the read content, the filename,
// and the color to be printed with. This method can be used directly
// to feed a ContentPrinter.
func (t *FileTailer) readFile() *PrintContent {
	// get current position
	curPos, err := t.file.Seek(0, io.SeekCurrent)
	handleErrorAndExit(err, "error while getting current cursor pos")

	finfo, err := os.Stat(t.file.Name())
	handleErrorAndExit(err, "error while getting filesize")

	// len to read is total file size - current position
	t.fileSize = finfo.Size()
	buflen := t.fileSize - curPos

	buf := make([]byte, buflen)
	n, err := t.file.Read(buf)
	handleErrorAndExit(err, "couldn't read line count")
	if n <= 0 {
		debug("reading file returned 0 or less bytes")
	}

	debug(fmt.Sprintf("tailer %d: read %d bytes from %s", t.wd, buflen, t.file.Name()))

	return &PrintContent{
		content:  string(buf[:n]),
		filename: t.file.Name(),
		color:    t.color,
	}
}

// close removes the Inotify watch and closes the file handler. This is
// intended to be done during a shutdown
func (t *FileTailer) close() {
	debug(fmt.Sprintf("tailer %d: closing file tailer %s", t.wd, filepath.Base(t.name)))
	t.unregisterWatch()
	t.file.Close()
}
