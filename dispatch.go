package main

import (
	"fmt"
	"syscall"
)

type Dispatch struct {
	tailers map[uint32]*FileTailer
}

// start starts the event consumer loop that receives events from a
// given channel and dispatches the events to the relevant file tailer
//
func (d *Dispatch) start(events <-chan syscall.InotifyEvent, done chan bool) {
	debug("dispatch: starting")
	for {
		// if no more tailers remain, signal a shutdown
		if len(d.tailers) == 0 {
			debug("dispatch: no tailers left to dispatch to, shutting down")
			close(done)

			return
		}

		select {
		case <-done:
			debug("dispatch: received notice to shutdown")
			return
		case event := <-events:
			wd := uint32(event.Wd)
			debug(fmt.Sprintf("dispatch: received inotify event for wd %d", wd))

			// does dispatch have a tailer to send this to
			if t, ok := d.tailers[wd]; ok {
				// send to processing
				nwd, err := t.processEvent(event)
				debug("dispatch: sent event to tailer")

				// was there an error during processing?
				if err != nil {
					// file deletions mean the tailer should be
					// decommissioned
					if err.Error() == "file deleted" {
						debug("dispatch: watching file has been deleted")
						t.close()
						delete(d.tailers, wd)
					} else {
						// something unexpected
						debug(fmt.Sprintf("dispatch: tailer couldn't process event, %s", err))
						return
					}
				}

				// was the watch descriptor updated during processing?
				if nwd != 0 {
					debug("dispatch: tailer refreshed file handler")
					delete(d.tailers, wd)
					d.registerTailer(nwd, t)
				}
			} else {
				debug(fmt.Sprintf("dispatch: received event is for a wd without a tailer at the moment: %d", wd))
			}
		}
	}
}

// registerTailer registers a FileTailer object in the Dispatch
// structure so that incoming Inotify Events can be distributed
// to them
func (d *Dispatch) registerTailer(wd uint32, t *FileTailer) {
	debug(fmt.Sprintf("dispatch: registering tailer for wd %d", wd))
	d.tailers[wd] = t
}

// shutdown is meant to be invoked during a main thread shutdown. This
// will close any unhandled open files.
func (d *Dispatch) shutdown() {
	debug(fmt.Sprintf("dispatch: shutting down, %d filetailers to close", len(d.tailers)))
	for _, t := range d.tailers {
		// schedule open file handlers to be closed
		debug(fmt.Sprintf("dispatch: closing file tailer %s", t.file.Name()))
		// todo: null check
		t.close()
	}
}
