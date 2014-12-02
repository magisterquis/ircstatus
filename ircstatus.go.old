/*
 * ircstatus
 * Program to make a host's staus visible to an IRC channel
 * by J. Stuart McMurray
 * Created 20141112
 * Last modified 20141127
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
package main

import (
	"bufio"
	"container/list"
	"flag"
	"fmt"
	"github.com/kd5pbo/killroutines"
	"io"
	"log"
	"math/rand"
	"net"
	"net/textproto"
	"os"
	"os/exec"
	"path"
	"regexp"
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
	flush     *bool          /* Flush pipe before reading */
	wait      *time.Duration /* Time to wait between reconnects */
	senddelay *time.Duration /* Time between sent lines */
	verbose   *bool          /* Verbose output */
	debug     *bool          /* Debug output */
	rxproto   *bool          /* Print received received IRC messages */
	savehelp  *string        /* Filename to which to save help text */

	/* Global variables */
	addr  string            /* Joined host:port */
	wq    *list.List        /* Queue of messages to send */
	user  string            /* Data passed to USER */
	txbuf *string           /* String we're trying to send */
	ipipe *textproto.Reader /* Pipe from which to read */
	onick string            /* Original nick, pre-numbers */
	rbuf  chan string       /* Global read (from pipe) buffer */
	snick string            /* Nick as understood by the server */

	/* Regular Expressions */
	reNickInUse     *regexp.Regexp /* Nick in use */
	reChannelJoined *regexp.Regexp /* Channel joined */
	reNoNickGiven   *regexp.Regexp /* No Nick given */
}

/* Regular Expression Literals */
var reNickInUse string = `(:\S+ )?433 .*\S+ :Nickname is already in use\.?`

/* TODO: Fix rCJ */
var reChannelJoined string = `(:\S+ )?353 `
var reNoNickGiven string = `(?:\S+ )?431 (\S+) .*:No nickname given\.?`

func main() { /* Signal handlers */
	os.Exit(mymain())
}
func mymain() int {
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
		"hostname.")
	gc.port = flag.Uint("port", 7000, "IRC server port.")
	gc.ssl = flag.Bool("ssl", true, "Use ssl.")
	gc.nick = flag.String("nick", *gc.nick, "IRC nickname.")
	gc.nums = flag.Bool("nums", true, "Append random numbers to the "+
		"nick.  Even if this is not given, numbers may still be "+
		"added in case of a nick conflict (which can happen in some "+
		"cases if -wait is too short).")
	gc.uname = flag.String("uname", "ircstatus", "Username.")
	gc.rname = flag.String("rname", "Status over IRC", "Real name.")
	gc.idnick = flag.String("idnick", "", "Nick to use to auth to "+
		"services.  If this is not specified but idpass is, the nick "+
		"given by -nick or the nick derived from the hostname will "+
		"be used.")
	gc.idpass = flag.String("idpass", "", "Pass to use to auth to "+
		"services.  If this is not specified and but idnick is, the "+
		"password will be read from the standard input.")
	gc.channel = flag.String("channel", "##ircstatushub", "Channel to "+
		"join.")
	gc.chanpass = flag.String("chanpass", "hunter2", "Channel "+
		"password (key).")
	gc.pipe = flag.String("pipe", "-", "Pipe from which to read.  This "+
		"can be \"-\" to indicate stdin, \"nick\" to cause a pipe "+
		"(i.e. fifo) to be created in "+os.TempDir()+" with the "+
		"name of the initial nick, or a path (like /tmp/ircstatus) "+
		"where one will be created if none exists.  Only text data "+
		"should be sent on this pipe.  Data will be buffered until "+
		"a newline (or \\r\\n) is read.  Lines should not be longer "+
		"than IRC allows (a bit under 510 bytes).")
	gc.flush = flag.Bool("flush", true, "Discard all data on the pipe "+
		"that existed before starting.  Ignored for -pipe=-.")
	gc.wait = flag.Duration("wait", time.Duration(10)*time.Second,
		"Time to wait between reconnection attempts.")
	gc.senddelay = flag.Duration("senddelay", time.Second, "Time to "+
		"delay between lines sent to avoid flooding.")
	gc.verbose = flag.Bool("verbose", false, "Print some non-error output.")
	gc.debug = flag.Bool("debug", false, "Print more non-error "+
		"output.  Implies -verbose.")
	gc.savehelp = flag.String("savehelp", "", "Does nothing but write "+
		"this help text to a file.")
	gc.rxproto = flag.Bool("rxproto", false, "Log received IRC protocol "+
		"messages.")
	flag.Parse()

	/* Only save the help */
	if "" != *gc.savehelp {
		return saveHelp(*gc.savehelp)
	}

	/* Seed the random number generator */
	rand.Seed(time.Now().Unix())

	/* Compile regular expressions */
	gc.reNickInUse = regexp.MustCompile(reNickInUse)
	gc.reChannelJoined = regexp.MustCompile(reChannelJoined)
	gc.reNoNickGiven = regexp.MustCompile(reNoNickGiven)

	/* Local hostname */
	debug("Local hostname: %v", *gc.nick)

	/* Save original nick */
	gc.onick = *gc.nick

	/* Work out the user */
	gc.user = fmt.Sprintf("%v x x :%v", *gc.uname, *gc.rname)
	debug("Initial user: %v", gc.user)

	/* Work out address */
	gc.addr = net.JoinHostPort(*gc.host, fmt.Sprintf("%v", *gc.port))
	debug("Will connect to %v", gc.addr)

	/* Work out whether we should auth to services */
	if "" != *gc.idnick || "" != *gc.idpass {
		/* Get the nick to use */
		if "" == *gc.idnick {
			*gc.idnick = *gc.nick
		}
		verbose("Auth nick: %v", *gc.idnick)
		/* Get a password */
		if "" == *gc.idpass {
			/* Try to read a line from stdin */
			p, err := bufio.NewReader(
				os.Stdin).ReadString('\n')
			if err != nil {
				log.Printf("Unable to read password to auth "+
					"to services: %v", err)
				return -5
			}
			/* Remove trailing newlines */
			p = strings.TrimRight(p, "\r\n")
			gc.idpass = &p
		}
		debug("Auth password: %v", *gc.idpass)
	}

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
			debug("Pipe %v does not already exist, creating pipe",
				pname)
			if err := syscall.Mkfifo(pname, 0644); err != nil {
				log.Printf("Unable to make %v: %v", pname, err)
				return -3
			}
			/* Clean up fifo before we exit */
			defer os.Remove(pname)
		}
		/* Check and see if one exists */
		fi, err = os.Stat(pname)
		/* Have a named pipe already */
		if err == nil && (fi.Mode()&os.ModeNamedPipe != 0) {
			debug("Pipe %v (now) exists", pname)
			/* Flush the pipe, if required */
			if *gc.flush {
				/* Put data on the pipe in case it's empty */
				cmd := forkSaveHelp(pname)
				/* Open pipe to flush it */
				debug("Opening %v for flushing", pname)
				pn, err := os.Open(pname)
				if err != nil {
					log.Printf("Unable to open %v for "+
						"flushing: %v", pname, err)
					return -6
				}
				b := make([]byte, 2048) /* Read buffer */
				/* Read from the pipe until it is empty */
				for {
					var e error
					/* TODO: select here with timeout */
					n, e := pn.Read(b)
					if e == io.EOF {
						break
					} else if e != nil && e != io.EOF {
						log.Printf("Error flushing "+
							"%v: %v", pname, e)
						return -7
					}
					debug("Read %v bytes flushing pipe", n)
				}
				debug("Waiting on pipe-filler to exit")
				cmd.Wait()
				/* Close the pipe */
				if err := pn.Close(); err != nil {
					log.Printf("Error closing %v: %v",
						pname, err)
					return -8
				}

			}
			/* Try to open the pipe RW, to prevent EOFs */
			f, e := os.OpenFile(pname, os.O_RDWR, 0600)
			if e != nil {
				log.Printf("Unable to open pipe named %v: %v",
					pname, e)
				return -1
			}
			gc.ipipe = textproto.NewReader(bufio.NewReader(f))
			break
		}
		/* Something else is there */
		log.Printf("Unable to use %v for input", pname)
		return -2
	}

	/* Make the pipe read buffer */
	gc.rbuf = make(chan string, 1)
	/* Start the pipe reader */
	go readPipe(gc.ipipe, gc.rbuf)

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

		/* Channel to communicate death from goroutines */
		dc := make(chan int) /* 0 for ok, -1 for die */

		/* Synchronize death */
		done := killroutines.New()

		/* Channel to signal writes to channel can start */
		ready := make(chan string)

		/* Start goroutines going */
		go reader(r, w, dc, done, ready)
		go sender(w, dc, done, ready)
		go waiter(cmd, dc, done)

		/* Set nick and user */
		if !setNick(false, w) {
			sleep()
			continue
		}

		/* Wait for goroutines to end */
		for i := 0; i < 3; i++ {
			if n := <-dc; -1 == n {
				/* Something bad happened */
				return -4
			}
		}

		/* Don't reconnect too fast */
		sleep()
	}
}

/* Goroutine to handle incoming messages */
func reader(r *textproto.Reader, w *textproto.Writer, dc chan int,
	done *killroutines.K, ready chan string) {
	debug("Starting reader")

	/* Channel to read from network */
	c := make(chan string)
	/* Read from the network into the channel */
	go func() {
		for {
			/* Get a line */
			l, err := r.ReadLine()
			/* If there's a problem, signal a reconnect */
			if err != nil {
				log.Printf("Error reading from network")
				close(c)
				return
			}
			/* Print if desired */
			if *gc.rxproto {
				log.Printf("IRC-> %v", l)
			}
			/* Put the line on the channel */
			c <- l
		}
	}()

	/* Read lines until an error */
	for {
		l := "" /* Received line */
		/* Get a line or be done */
		select {
		case <-done.Chan(): /* Time to die */
			dc <- 0
			return
		case line, ok := <-c:
			/* Handle errors */
			if !ok {
				/* If we have a read error, new connection */
				done.Signal()
				dc <- 0
				return
			}
			l = line
		}
		/* Handle incoming messages */
		switch {
		case strings.HasPrefix(strings.ToLower(l), "ping "): /* Ping */
			reply := fmt.Sprintf("PONG ", l[5:])
			w.PrintfLine(reply)
			debug("Sent ping reply: %v", reply)
		case gc.reNickInUse.MatchString(l): /* Nick is in use */
			verbose("Nick %v in use, trying a new one", *gc.nick)
			/* Set a new nick */
			if !setNick(true, w) {
				done.Signal()
				dc <- 0
				return
			}
		case gc.reChannelJoined.MatchString(l): /* Channel's joined */
			verbose("Joined %v", *gc.channel)
			close(ready)
		case gc.reNoNickGiven.MatchString(l): /* No Nick was given */
			/* Extract the nick */
			m := gc.reNoNickGiven.FindStringSubmatch(l)
			/* Make sure we have it */
			if 2 != len(m) {
				verbose("Error processing \"No nickname " +
					"given\" message")
			}
			/* Save the nick given by the server */
			gc.snick = m[1]
			verbose("Nick is %v", gc.snick)
		}
	}
}

/* Goroutine to handle outgoing messages.  cv is a condition variable that
wakes us up when sending is ready (after a channel join and so on, cs tells
us it's ok to send (can send). */
func sender(w *textproto.Writer, dc chan int, done *killroutines.K,
	ready chan string) {
	debug("Started sender")
	defer debug("No longer sending")
	/* Closure to send a line.  Returns true if we should return */
	send := func(l string) {
		/* Put the line in the buffer */
		gc.txbuf = &l
		/* Send the line */
		if err := w.PrintfLine("PRIVMSG %v :%s", *gc.channel,
			/* If it failed, start the reconnect process */
			*gc.txbuf); err != nil {
			verbose("Unable to send line: %v", err)
			done.Signal()
			dc <- 0
			return
		} else {
			/* If it worked, empty the buf for next time */
			gc.txbuf = nil
		}
	}
	/* Wait for writing to be allowable or the signal to return */
	select {
	case <-done.Chan(): /* Time to exit */
		debug("Sender dying before it started")
		dc <- 0
		return
	case <-ready: /* In channel, ready to send */
		debug("Sender can send")
	}

	/* First try to send the txbuf now that we can send */
	if nil != gc.txbuf {
		debug("Sending TX buffer: %v", *gc.txbuf)
		send(*gc.txbuf)
	}

	/* Set up reads from r */
	for {
		select {
		case line, ok := <-gc.rbuf: /* Received a line */
			if !ok { /* Error */
				done.Signal()
				dc <- -1
				return
			}
			/* Remove needless whitespace */
			line = strings.TrimRightFunc(line, unicode.IsSpace)
			gc.txbuf = &line
			debug("Will send line: %v", *gc.txbuf)
			/* Send the line */
			if err := w.PrintfLine("PRIVMSG %v :%s", *gc.channel,
				*gc.txbuf); err != nil {
				verbose("Unable to send line: %v", err)
				done.Signal()
				dc <- 0
				return
			}
			/* If it worked, empty the buf for next time */
			gc.txbuf = nil
			time.Sleep(*gc.senddelay)
		case <-done.Chan(): /* We're meant to stop */
			dc <- 0
			return
		}
	}
}

/* readPipe reads lines from r and sends them to c */
func readPipe(r *textproto.Reader, c chan string) {
	debug("Reading from a pipe into a channel")
	defer debug("No longer reading from the pipe.")
	/* Wait for a line or c to close */
	for {
		l, err := r.ReadLine()
		/* Close c to signal an error */
		if err != nil {
			/* TODO: Work out if we really need to exit */
			log.Printf("Error getting line to send: %v",
				err)
			close(c)
			return
		}
		/* Send the read line out */
		c <- l
	}
}

/* Wait for a process to die */
func waiter(c *exec.Cmd, dc chan int, done *killroutines.K) {
	debug("Started waiter")
	defer debug("Waited")
	wc := make(chan int)
	go func() {
		if err := c.Wait(); err != nil {
			log.Printf("openssl exited badly: %v", err)
		}
		close(wc)
	}()
	/* Wait for openssl to die */
	select {
	case <-wc: /* OpenSSL died */
		/* Tell the other goroutines to die */
		done.Signal()
	case <-done.Chan(): /* We should die */
		/* Stop OpenSSL */
		debug("Killing OpenSSL (pid %v)", c.Process.Pid)
		if err := c.Process.Kill(); err != nil {
			log.Printf("Unable to kill OpenSSL (pid %v): %v",
				c.Process.Pid, err)
		}
	}
	dc <- 0
	return
}

/* makeNick makes a new nick with numbers.  n overrides *gc.nums */
func setNick(n bool, w *textproto.Writer) bool {
	/* Add numbers if needed */
	if *gc.nums || n {
		*gc.nick = fmt.Sprintf("%v-%v", gc.onick, rand.Int63())
	}

	/* Tell the user what the nick is */
	nick := fmt.Sprintf("NICK %v\n", *gc.nick)
	verbose("Setting nick: %v", *gc.nick)
	/* Set the nick */
	if err := w.PrintfLine(nick); err != nil {
		log.Printf("Error setting nick: %v", err)
		return false
	}

	/* Set the user */
	u := fmt.Sprintf("USER %v", gc.user)
	debug("Setting user: %v", u)
	if err := w.PrintfLine(u); err != nil {
		log.Printf("Error setting user: %v", err)
		return false
	}

	/* Auth to services */
	if "" != *gc.idnick && "" != *gc.idpass {
		verbose("Authenticating to services")
		if err := w.PrintfLine("PRIVMSG nickserv :identify %v %v",
			*gc.idnick, *gc.idpass); err != nil {
			log.Printf("Error authenticating to services: %v")
			return false
		}
	}

	/* Join Channel */
	c := fmt.Sprintf("JOIN %v %v", *gc.channel, *gc.chanpass)
	debug("Joining channel: %v", c)
	if err := w.PrintfLine(c); err != nil {
		log.Printf("Error requesting to join channel %v: %v", err)
		return false
	}

	/* Send an erroneous nick command, to get our own for sure */
	debug("Requesting nick with erroneous NICK command")
	if err := w.PrintfLine("NICK"); err != nil {
		log.Printf("Unable to send intentionally erroneous NICK "+
			"command: err", err)
		return false
	}
	return true
}

/* saveHelp writes the help text to a file */
func saveHelp(fname string) int {
	/* Open output file */
	f, err := os.Create(fname)
	if err != nil {
		fmt.Printf("Unable to open %v to write help text: %v\n", fname,
			err)
		return -9
	}
	debug("Opened %v for saving help", fname)
	flag.CommandLine.SetOutput(f)
	debug("Set output to %v", f)
	flag.PrintDefaults()
	debug("Saved help text to %v", fname)
	return 0
}

/* forkSaveHelp writes the help data to the specified file. */
func forkSaveHelp(fname string) *exec.Cmd {
	/* Make a command out of ourselves */
	c := exec.Command(os.Args[0], "-savehelp", fname)
	/* Run the command */
	debug("Running %v to have data to flush from %v", c.Args, fname)
	err := c.Start()
	if err != nil {
		fmt.Printf("Error putting data into %v for flushing: %v",
			fname, err)
	}
	return c
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
