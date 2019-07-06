package main

import (
	"fmt"
	"syscall"
)

type Dispatch struct {
	tailers map[uint32]*FileTailer
}

func (d *Dispatch) start(events <-chan syscall.InotifyEvent, done <-chan bool) {
	debug("dispatch: starting")
	for {
		select {
		case <-done:
			debug("dispatch: received notice to shutdown")
			return
		case event := <-events:
			uwd := uint32(event.Wd)
			debug(fmt.Sprintf("dispatch: received inotify event for wd %d", uwd))
			if t, ok := d.tailers[uwd]; ok {
				t.processEvent(event)
				debug("dispatch: sent event to tailer")
			} else {
				debug(fmt.Sprintf("dispatch: received event is for a wd without a tailer at the moment: %d", uwd))
			}
		}
	}
}

func (d *Dispatch) registerTailer(wd uint32, t *FileTailer) {
	debug(fmt.Sprintf("dispatch: registering tailer for wd %d", wd))
	d.tailers[wd] = t
}

func (d *Dispatch) shutdown() {
	debug(fmt.Sprintf("dispatch: shutting down, %d filetailers to close", len(d.tailers)))
	for _, t := range d.tailers {
		// schedule open file handlers to be closed
		debug(fmt.Sprintf("dispatch: closing file tailer %s", t.file.Name()))
		t.close()
	}
}
