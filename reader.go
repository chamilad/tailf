package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"syscall"
)

type EventReader struct {
	fd int
}

// init opens an Inotify kernel structure
func (e *EventReader) init() {
	debug("reader: initializing inotify")
	// TODO: apparently syscall is deprecated, use sys pkg later
	// TODO: check if fd opened below needs to be closed
	fd, err := syscall.InotifyInit()
	handleErrorAndExit(err, "error while inotify init")

	e.fd = fd
}

// start starts an infinite loop to read InotifyEvent structures from
// it. The read() syscall is a blocking one until any data is present.
// Once the inotify events are present, the events are unmarshalled
// and communicated to the consumer
// At the moment, the read() call could close improperly if the main
// thread gives out. Need a way to timeout based on a notification
// from the main thread at the read().
func (e *EventReader) start(events chan<- syscall.InotifyEvent) {
	for {
		buf := make([]byte, (syscall.SizeofInotifyEvent+syscall.NAME_MAX+1)*10)

		// read from the opened inotify file descriptor, into buf
		// read() is blocking until some data is available
		debug("reader: waiting for inotify event list")
		n, err := syscall.Read(e.fd, buf)
		handleErrorAndExit(err, "error while reading inotify file")

		// check if the read value is 0
		if n <= 0 {
			printErr("inotify read resulted in EOF")
		}

		debug(fmt.Sprintf("reader: read %d from inotify", n))

		// read the buffer for all its events
		offset := 0
		for {
			if offset+syscall.SizeofInotifyEvent > n {
				debug("reader: reached end of inotify buffer")
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
			debug(fmt.Sprintf("reader: sent event for wd %d to queue", event.Wd))

			// move the window and read the next event
			offset += syscall.SizeofInotifyEvent + int(event.Len)
		}
	}
}
