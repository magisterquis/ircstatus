package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"os"
	"os/exec"
	"path"
	"syscall"
	"time"
)

/*
 * The MIT License (MIT)
 *
 * Copyright (c) 2014 J. Stuart McMurray
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

type Pipe struct {
	R     <-chan string /* Line channel */
	r     chan string   /* Writable, closeable R */
	E     <-chan error  /* Error channel */
	e     chan error    /* Writable E */
	Pname string        /* Pipe name */
}

/* makePipe makes or opens a named pipe and returns a channel to which data
sent to the pipe will be sent.  If flush is true, the pipe will be flushed
before reads start.  The pipe name is returned for removal before main()
returns. */
func makePipe(pname, nick string, flush bool) (*Pipe, error) {

	/* Struct to return */
	p := &Pipe{Pname: pname}
	var f *os.File

	/* Make/flush/open the pipe if it's not stdin */
	var rf io.Reader
	if "-" == pname {
		rf = os.Stdin
		p.Pname = "-"
	} else {
		/* Work out the proper name for the pipe */
		if "nick" == pname { /* Name based on nick */
			debug("Pipe based on nick")
			p.Pname = path.Join(os.TempDir(), nick) /* /tmp/nick */
			debug("Pipe name: %v", p.Pname)
		}

		/* Make sure the pipe exists */
		if err := createPipe(p.Pname); nil != err {
			return nil, errors.New(fmt.Sprintf("unable to "+
				"ensure pipe %v exists: %v", p.Pname, err))
		}

		/* Flush the pipe if desired */
		if flush {
			if err := flushPipe(p.Pname); nil != err {
				return nil, errors.New(fmt.Sprintf("unable "+
					"to flush pipe %v: %v", p.Pname, err))
			}
			debug("Pipe %v flushed", p.Pname)
		}

		/* Try to open the pipe RW, to prevent EOFs */
		var e error
		rf, e = os.OpenFile(p.Pname, os.O_RDWR, 0600)
		if nil != e {
			return nil, errors.New(fmt.Sprintf("unable to open "+
				"pipe %v: %v", p.Pname, e))
		}
		debug("Opened pipe r/w: %v", p.Pname)

	}

	/* Make comms channels */
	p.r = make(chan string)
	p.R = p.r
	p.e = make(chan error)
	p.E = p.e
	/* Reader to get lines to put in channel */
	r := textproto.NewReader(bufio.NewReader(rf))
	go func() {
		for {
			/* Get a line from the reader */
			line, err := r.ReadLine()
			/* Close the channel on error */
			if nil != err {
				/* Send forth the error */
				p.e <- err
				/* Close the output channel */
				close(p.r)
				/* Close the pipe if not stdin */
				if "-" != p.Pname {
					if err := f.Close(); nil != err {
						verbose("Error closing %v: %v",
							p.Pname, err)
					}
				}
				/* Don't send on the closed channel */
				return
			}
			/* Send out the line */
			p.r <- line
		}
	}()
	return p, nil
}

/* createPipe ensures that a pipe named pname exists */
func createPipe(pname string) error {
MakePipe:
	debug("Checking whether %v exists and is a pipe", pname)
	/* Check and see if one exists */
	fi, err := os.Stat(pname)
	/* Check output */
	switch {
	case nil != err && os.IsNotExist(err): /* Pipe does not exist */
		debug("Pipe %v does not already exist, creating pipe", pname)
		if err := syscall.Mkfifo(pname, 0644); err != nil {
			return errors.New(fmt.Sprintf("unable to make pipe "+
				"%v: %v", pname, err))
		}
		goto MakePipe /* Neener neener */
	case nil != err: /* Error calling stat() */
		return errors.New(fmt.Sprintf("unable to get stat "+
			"information for %v: %v", pname, gc.wait, err))
	case 0 == fi.Mode()&os.ModeNamedPipe: /* pname is not a pipe */
		return errors.New(fmt.Sprintf("%v exists but is not a pipe",
			pname))
	default: /* All is good */
		debug("%v exists and is a pipe", pname)
	}
	return nil
}

/* flushPipe flushes data from the pipe named pname */
func flushPipe(pname string) error {
	var cmd *exec.Cmd = nil
	/* Put data on the pipe in case it's empty */
	for nil == cmd {
		var err error
		if cmd, err = forkSaveHelp(pname); nil != err {
			return errors.New(fmt.Sprintf("unable to start "+
				"command to put flushable data into %v: %v",
				pname, gc.wait, err))
		}
	}
	debug("Started %v", cmd.Args)
	/* Wait for the child */
	go func() {
		debug("Waiting on pipe-filler to exit in background")
		cmd.Wait()
		debug("Pipe-filler exited.")
	}()

	/* Open pipe to flush it */
	debug("Opening %v for flushing", pname)
	pn, err := os.Open(pname)
	if err != nil {
		return errors.New(fmt.Sprintf("unable to open %v for "+
			"flushing: %v", pname, err))
	}
FlushLoop:
	for {
		select {
		case e := <-flushBytes(pn):
			if io.EOF == e {
				debug("Finished flushing %v", pname)
				break FlushLoop
			} else if nil != e {
				debug("Error flushing pipe %v: %v",
					pname, e)
				break FlushLoop
			}
		case <-time.After(*gc.wait):
			verbose("Timed out after %v while flushing %v",
				*gc.wait, pname)
			break FlushLoop
		}
	}
	/* Close the pipe */
	if err := pn.Close(); nil != err {
		debug("Error closing %v after flushing: %v",
			pname, err)
	}
	return nil
}

/* flushBytes returns a channel on which an error or nil will be sent
depending on whether reading (and discarding) bytes from r fails or succeeds */
func flushBytes(r *os.File) <-chan error {
	c := make(chan error, 1)
	go func() {
		/* Read buffer */
		b := make([]byte, 4096)
		/* Get some bytes */
		n, err := r.Read(b)
		/* Report how many */
		if n > 0 {
			debug("Got %v bytes flushing pipe", n)
		}
		/* Send errors back */
		if io.EOF == err { /* No more data to read */
			c <- io.EOF
			return
		} else if nil != err { /* An error occurred */
			c <- err
			return
		}
		c <- nil
		return
	}()
	return c
}

/* forkSaveHelp writes the help data to the specified file. */
func forkSaveHelp(fname string) (*exec.Cmd, error) {
	/* Make a command out of ourselves */
	c := exec.Command(os.Args[0], "-savehelp", fname)
	/* Run the command */
	debug("Attempting to Run %v to have data to flush from %v",
		c.Args, fname)
	err := c.Start()
	if err != nil {
		fmt.Printf("Error putting data into %v for flushing: %v",
			fname, err)
		return nil, err
	}
	return c, nil
}
