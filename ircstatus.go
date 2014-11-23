/*
 * ircstatus
 * Program to make a host's staus visible to an IRC channel
 * by J. Stuart McMurray
 * Created 20141112
 * Last modified 20141121
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
package main

import (
	"bufio"
	"container/list"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/textproto"
	"os"
	"os/exec"
	"path"
	"strings"
	"syscall"
	"time"
	"unicode"
)

/* Defaults */
var defaultnick string = "ircstatus"

/* Global config */
var gc struct {
	/* Flags */
	host      *string        /* IRC server hostname */
	port      *uint          /* IRC server port */
	ssl       *bool          /* Whether to use SSL */
	nick      *string        /* IRC nick to use */
	nums      *bool          /* Append random numbers to nick */
	uname     *string        /* Username to pass to IRC server */
	rname     *string        /* Real name to pass to IRC server */
	idnick    *string        /* Nick to use to auth to NickServ */
	idpass    *string        /* Password to use to auth to Nickserv */
	channel   *string        /* Channel to join */
	chanpass  *string        /* Channel password */
	pipe      *string        /* FIFO for reading */
	wait      *time.Duration /* Time to wait between reconnects */
	senddelay *time.Duration /* Time between sent lines */
	verbose   *bool          /* Verbose output */
	debug     *bool          /* Debug output */

	/* Global variables */
	addr  string            /* Joined host:port */
	wq    *list.List        /* Queue of messages to send */
	user  string            /* Data passed to USER */
	txbuf *string           /* String we're trying to send */
	ipipe *textproto.Reader /* Pipe from which to read */
}

func main() {
	/* Get local hostname for flag default */
	n, err := os.Hostname()
	gc.nick = &n
	if nil != err {
		log.Printf("Unable to determine hostname: %v", err)
		*gc.nick = defaultnick
	}
	/* Only want the bit before the first . */
	*gc.nick = strings.SplitN(*gc.nick, ".", 2)[0]

	/* Get options */
	gc.host = flag.String("host", "chat.freenode.net", "IRC server "+
		"hostname")
	gc.port = flag.Uint("port", 7000, "IRC server port")
	gc.ssl = flag.Bool("ssl", true, "Use ssl")
	gc.nick = flag.String("nick", *gc.nick, "IRC nickname")
	gc.nums = flag.Bool("nums", true, "Append random numbers to the nick")
	gc.uname = flag.String("uname", "ircstatus", "Username")
	gc.rname = flag.String("rname", "Status over IRC", "Real name")
	gc.idnick = flag.String("idnick", "", "Nick to use to auth to "+
		"services.  If this is not specified but idpass is, the nick "+
		"given by -nick or the nick derived from the hostname will "+
		"be used")
	gc.idpass = flag.String("idpass", "", "Pass to use to auth to "+
		"services.  If this is not specified and but idnick is, the "+
		"password will be read from the standard input")
	gc.channel = flag.String("channel", "##ircstatushub", "Channel to "+
		"join")
	gc.chanpass = flag.String("chanpass", "hunter2", "Channel "+
		"password (key)")
	gc.pipe = flag.String("pipe", "-", "Pipe from which to read.  This "+
		"can be \"-\" to indicate stdin, \"nick\" to cause a pipe "+
		"(i.e. fifo) to be created in "+os.TempDir()+" with the "+
		"name of the initial nick, or a path (like /tmp/ircstatus) "+
		"where one will be created if none exists.  Only text data "+
		"should be sent on this pipe.  Data will be buffered until "+
		"a newline (or \\r\\n) is read.  Lines should not be longer "+
		"than IRC allows (a bit under 510 bytes)")
	gc.wait = flag.Duration("wait", time.Duration(10)*time.Second,
		"Time to wait between reconnection attempts")
	gc.senddelay = flag.Duration("senddelay", time.Second, "Time to "+
		"delay between lines sent to avoid flooding.")
	gc.verbose = flag.Bool("verbose", false, "Print some non-error output")
	gc.debug = flag.Bool("debug", false, "Print more non-error "+
		"output.  Implies -verbose")
	flag.Parse()

	/* Local hostname */
	debug("Local hostname: %v", *gc.nick)

	/* Work out our nick */
	if *gc.nums {
		rand.Seed(time.Now().Unix())
		*gc.nick = fmt.Sprintf("%v-%v", *gc.nick, rand.Int63())
	}
	verbose("Initial nick: %v", *gc.nick)

	/* Work out the user */
	gc.user = fmt.Sprintf("%v x x :%v", *gc.uname, *gc.rname)
	debug("Initial user: %v", gc.user)

	/* Work out address */
	gc.addr = net.JoinHostPort(*gc.host, fmt.Sprintf("%v", *gc.port))
	debug("Will connect to %v", gc.addr)

	/* Open the pipe */
	pname := "" /* Pipe name */
	switch *gc.pipe {
	case "-": /* stdin */
		debug("Taking input from stdin")
		gc.ipipe = textproto.NewReader(bufio.NewReader(os.Stdin))
	case "nick": /* Name based on nick */
		debug("Pipe based on nick")
		pname = path.Join(os.TempDir(), *gc.nick) /* /tmp/nick */
		fallthrough
	default: /* User supplied name */
		if "" == pname { /* Didn't fallthrough */
			pname = *gc.pipe
		}
		debug("Pipe name: %v", pname)
		/* Check and see if one exists */
		fi, err := os.Stat(pname)
		/* Nothing there */
		if os.IsNotExist(err) {
			debug("Pipe does not already exist, creating pipe")
			if err := syscall.Mkfifo(pname, 0644); err != nil {
				log.Printf("Unable to make %v: %v", pname, err)
				os.Exit(-3)
			}
			/* Clean up fifo before we exit */
			defer os.Remove(pname)
		}
		/* Check and see if one exists */
		fi, err = os.Stat(pname)
		/* Have a named pipe already */
		if err == nil && (fi.Mode()&os.ModeNamedPipe != 0) {
			debug("Pipe %v (now) exists", pname)
			/* Try to open the file */
			f, e := os.OpenFile(pname, os.O_RDWR, 0600)
			if e != nil {
				log.Printf("Unable to open pipe named %v: %v",
					pname, e)
				os.Exit(-1)
			}
			gc.ipipe = textproto.NewReader(bufio.NewReader(f))
			break
		}
		/* Something else is there */
		log.Printf("Unable to use %v for input", pname)
		os.Exit(-2)
	}

	/* Main program loop */
	for {
		/* Command to use to connect to server */
		cmd := exec.Command("openssl", "s_client", "-quiet",
			"-connect", gc.addr)
		debug("Connection command: %v %v", cmd.Path, cmd.Args)

		/* Get ahold of i/o pipes */
		stdin, err := cmd.StdinPipe()
		if err != nil {
			log.Printf("Unable to get network input pipe: %v", err)
			sleep()
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("Unable to get network output pipe: %v",
				err)
			sleep()
		}

		/* Turn into textproto i/o structs */
		r := textproto.NewReader(bufio.NewReader(stdout))
		w := textproto.NewWriter(bufio.NewWriter(stdin))

		/* Connect to server */
		debug("Connecting to %v", gc.addr)
		if err := cmd.Start(); err != nil {
			/* Retry again in a bit if we fail */
			log.Printf("Retrying connection to %v in %v: %v",
				gc.addr, *gc.wait, err)
			sleep()
			continue
		}

		/* Set nick and user */
		n := fmt.Sprintf("NICK %v\n", *gc.nick)
		debug("Setting nick: %v", n)
		if err := w.PrintfLine(n); err != nil {
			log.Printf("Error setting nick: %v", err)
			sleep()
			continue
		}
		u := fmt.Sprintf("USER %v", gc.user)
		debug("Setting user: %v", u)
		if err := w.PrintfLine(u); err != nil {
			log.Printf("Error setting user: %v", err)
			sleep()
			continue
		}

		/* Auth to services */

		/* Join Channel */
		c := fmt.Sprintf("JOIN %v %v", *gc.channel, *gc.chanpass)
		debug("Joining channel: %v", c)
		if err := w.PrintfLine(c); err != nil {
			log.Printf("Error requesting to join channel %v: %v",
				err)
			sleep()
			continue
		}

		/* Tell interested users we've sent initial messages */
		verbose("Set nick and user and sent request to join channel")

		/* Channel to communicate death */
		dc := make(chan int) /* 0 for ok, -1 for die */

		go reader(r, w, dc) /* DEBUG */
		go sender(gc.ipipe, w, dc)
		go waiter(cmd, dc)

		/* Wait for goroutines to end */
		for i := 0; i < 3; i++ {
			if n := <-dc; -1 == n {
				/* Something bad happened */
				return
			}
		}

		/* Don't reconnect too fast */
		sleep()
	}
}

/* Goroutine to handle incoming messages */
func reader(r *textproto.Reader, w *textproto.Writer, dc chan int) {
	debug("Starting reader")
	/* Read lines until an error */
	for {
		/* Get a line */
		l, err := r.ReadLine()
		/* Handle errors */
		if err != nil {
			/* TODO: Make this better */
			verbose("Read error: %v", err)
			dc <- 0
			return
		}
		/* Handle pings */
		if strings.HasPrefix(strings.ToLower(l), "ping ") {
			w.PrintfLine("PONG ", l[5:])
		}
	}
}

/* Goroutine to handle outgoing messages */
func sender(p *textproto.Reader, w *textproto.Writer, dc chan int) {
	debug("Started sender")
	for {
		/* Get a line to send, either from the buffer or the pipe */
		if nil != gc.txbuf {
			debug("Will send buffered line: %v", *gc.txbuf)
		} else {
			line, err := p.ReadLine()
			if err != nil {
				/* TODO: Work out if we really need to exit */
				log.Printf("Error getting line to send: %v",
					err)
				dc <- -1
				return
			}
			/* Remove needless whitespace */
			line = strings.TrimRightFunc(line, unicode.IsSpace)
			gc.txbuf = &line
			debug("Will send line: %v", *gc.txbuf)
		}
		/* Send the line */
		if err := w.PrintfLine("PRIVMSG %v :%s", *gc.channel,
			*gc.txbuf); err != nil {
			verbose("Unable to send line: %v", err)
			dc <- 0
			return
		} else {
			/* If it worked, empty the buf for next time */
			gc.txbuf = nil
		}
		time.Sleep(*gc.senddelay)
	}
}

/* Wait for a process to die */
func waiter(c *exec.Cmd, dc chan int) {
	debug("Started waiter")
	if err := c.Wait(); err != nil {
		log.Printf("openssl exited badly: %v", err)
	}
	dc <- 0
}

/* Verbose and debug output */
func debug(f string, a ...interface{}) {
	if *gc.debug {
		log.Printf(f, a...)
	}
}

func verbose(f string, a ...interface{}) {
	if *gc.verbose || *gc.debug {
		log.Printf(f, a...)
	}
}

/* sleep sleeps the required amount of time before a reconnect */
func sleep() {
	log.Printf("Sleeping %v before reconnect", *gc.wait)
	time.Sleep(*gc.wait)
}
