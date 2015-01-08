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
	"errors"
	"flag"
	"fmt"
	"github.com/kd5pbo/minimalirc"
	"io"
	"log"
	"math"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"time"
)

/* Defaults */
const defaultnick = "ircstatus"

/* Global config */
var gc struct {
	/* Flags */
	host      *string        /* IRC server hostname */
	port      *uint          /* IRC server port */
	ssl       *bool          /* Connect with SSL/TLS */
	sslname   *string        /* Hostname on cert */
	nick      *string        /* IRC nick to use */
	nums      *bool          /* Append random numbers to nick */
	uname     *string        /* Username to pass to IRC server */
	rname     *string        /* Real name to pass to IRC server */
	idnick    *string        /* Nick to use to auth to NickServ */
	idpass    *string        /* Password to use to auth to Nickserv */
	channel   *string        /* Channel to join */
	chanpass  *string        /* Channel password */
	qmsg      *string        /* IRC quit message */
	pipe      *string        /* FIFO for reading */
	flush     *bool          /* Flush pipe before reading */
	rmpipe    *bool          /* Remove pipe after exit */
	wait      *time.Duration /* Time to wait between reconnects */
	senddelay *time.Duration /* Time between sent lines */
	verbose   *bool          /* Verbose output */
	debug     *bool          /* Debug output */
	rxproto   *bool          /* Print received received IRC messages */
	txlines   *bool          /* Print lines sent to IRC server */
	timeout   *time.Duration /* IRC timeout */
	savehelp  *string        /* Filename to which to save help text */
}

/* Global regular expressions */
const reChannelJoined = `(:\S+ )?353 .*\S+ `
const reNickInUse = `(:\S+ )?433 .*\S+ :Nickname is already in use\.?`

var re struct {
	ChannelJoined *regexp.Regexp
	NickInUse     *regexp.Regexp
}

/* Global name of pipe to remove, if any */
var rempname string = ""

/* Global IRC struct */
var irc *minimalirc.IRC = nil

func main() { /* Signal handlers */
	ret := 0            /* Return value from main */
	m := make(chan int) /* Channel on which to get return value */
	go func() {
		i := mymain()
		m <- i
	}()
	/* Set up signal channel */
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, os.Interrupt)
	select {
	case ret = <-m:
		break
	case s := <-sigchan:
		if os.Interrupt != s {
			verbose("Caught unpossible signal")
		}
		ret = -5
	}
	/* Gracefully quit IRC */
	if nil != irc {
		debug("Gracefully QUITting IRC")
		if err := irc.Quit(""); err != nil {
			verbose("Error encountered gracefully quitting "+
				"IRC: %v", err)
		}
	}
	/* Remove the pipe */
	if "" != rempname {
		debug("Removing %v", rempname)
		if err := os.Remove(rempname); nil != err {
			verbose("Unable to remove pipe %v: %v", rempname, err)
		}
	}

	os.Exit(ret)
}
func mymain() int {
	/* Get local hostname for flag default */
	n, err := os.Hostname()
	gc.nick = &n
	if nil != err {
		log.Printf("Unable to determine hostname: %v", err)
		*gc.nick = defaultnick
	} else {
		/* Only want the bit before the first . */
		*gc.nick = strings.SplitN(*gc.nick, ".", 2)[0]
	}

	/* Get options */
	gc.host = flag.String("host", "chat.freenode.net", "IRC server "+
		"hostname.")
	gc.port = flag.Uint("port", 7000, "IRC server port.")
	gc.ssl = flag.Bool("ssl", true, "Use SSL/TLS.")
	gc.sslname = flag.String("sslname", "", "Hostname expected on "+
		"server's SSL certificate.  If this is not specified, and "+
		"-ssl is, -host will be used.")
	gc.nick = flag.String("nick", *gc.nick, "IRC nickname.")
	gc.nums = flag.Bool("nums", true, "Append random numbers to the "+
		"nick.  Even if this is not given, numbers may still be "+
		"added in case of a nick conflict (which can happen in some "+
		"cases if -wait is too short).  The numbers will change "+
		"every time a new connection is established.")
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
	gc.qmsg = flag.String("qmsg", "https://github.com/kd5pbo/ircstatus",
		"Message to send when closing connection to IRC server.")
	gc.pipe = flag.String("pipe", "-", "Pipe from which to read.  This "+
		"can be \"-\" to indicate stdin, \"nick\" to cause a pipe "+
		"(i.e. fifo) to be created in "+os.TempDir()+" with the "+
		"name of the initial nick, or a path (like /tmp/ircstatus) "+
		"where one will be created if none exists.  Only text data "+
		"should be sent on this pipe.  Data will be buffered until "+
		"a newline (or \\r\\n) is read.  Lines too long for one "+
		"message will be split into smaller lines.  In some cases, "+
		"with extremely long channel names, certain multi-byte "+
		"unicode characters may be replaced with a '?'.  If "+
		"-pipe=nick is given, the created pipe will be removed upon "+
		"exit.")
	gc.flush = flag.Bool("flush", true, "Discard all data on the pipe "+
		"that existed before starting.  Ignored for -pipe=-.")
	gc.wait = flag.Duration("wait", time.Duration(10)*time.Second,
		"Time to wait after a failed connection attempt or failed "+
			"open of -pipe.")
	gc.senddelay = flag.Duration("senddelay", time.Second, "Time to "+
		"delay between lines sent to avoid flooding.")
	gc.verbose = flag.Bool("verbose", false, "Print some non-error output.")
	gc.debug = flag.Bool("debug", false, "Print more non-error "+
		"output.  Implies -verbose.  This should be used with care "+
		"as it could leak passwords.")
	gc.savehelp = flag.String("savehelp", "", "Does nothing but write "+
		"this help text to a file.")
	gc.rxproto = flag.Bool("rxproto", false, "Log received IRC protocol "+
		"messages.")
	gc.timeout = flag.Duration("timeout", 2*time.Minute, "Reconnect to "+
		"the IRC server if no messages has been received in this long.")
	gc.txlines = flag.Bool("txlines", false, "Log lines sent to IRC "+
		"server")
	flag.Parse()
	/* Set more precision if -debug */
	if *gc.debug {
		log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	}
	debug("Local hostname: %v", n)

	/* Only save the help if requested */
	if "" != *gc.savehelp {
		return saveHelp(*gc.savehelp)
	}

	/* Make sure the port is in the right range */
	if *gc.port > math.MaxUint16 {
		fmt.Printf("Port %v is larger than %v.\n", *gc.port,
			math.MaxUint16)
		return -3
	}

	/* Compile regular expressions */
	re.NickInUse = regexp.MustCompile(reNickInUse)
	re.ChannelJoined = regexp.MustCompile(reChannelJoined)

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

	/* SSL hostname, if not specified */
	if *gc.ssl && "" == *gc.sslname {
		*gc.sslname = *gc.host
	}

	/* Channels (or channel-containing structs) for select */
	var pipe *Pipe = nil

	/* Kill IRC connection before exit */
	defer func() {
		if nil != irc {
			verbose("Quitting IRC gracefully")
			irc.Quit("")
		}
	}()

	/* True if we need to make a new IRC or pipe */
	newIRC := true
	newPipe := true

	/* Buffer for lines to be sent in case the connection dies */
	var txbuf *string = nil

	/* True when we're actually ready to send IRC messages */
	ircReady := false

	/* Nick from first IRC connection for use if -pname=nick */
	onick := ""

	/* Main program loop */
	for {
		/* Get a channel for IRC messages */
		if newIRC {
			/* Not ready to send messages */
			ircReady = false

			/* Work out the prefixes */
			txp := ""
			rxp := ""
			if *gc.rxproto {
				rxp = "IRC->"
			}
			if *gc.txlines {
				txp = "->IRC"
			}
			/* Try to connect and get a channel */
			irc = minimalirc.New(
				*gc.host, uint16(*gc.port), /* Server */
				*gc.ssl, *gc.sslname, /* Use SSL (or not) */
				*gc.nick, *gc.uname, *gc.rname) /* ID */
			/* Numbers after the nick */
			irc.RandomNumbers = *gc.nums
			/* Auth */
			irc.IdNick = *gc.idnick
			irc.IdPass = *gc.idpass
			/* Channel */
			irc.Channel = *gc.channel
			irc.Chanpass = *gc.chanpass
			/* Log all messages */
			irc.Txp = txp
			irc.Rxp = rxp
			/* Send pongs */
			irc.Pongs = true
			/* Quit message */
			irc.QuitMessage = *gc.qmsg
			/* Set our own idea of pings */
			irc.Timeout = *gc.timeout
			/* If it fails, try again in a bit */
			if err := irc.Connect(); nil != err {
				verbose("Unable to connect to IRC server "+
					"%v (retry in %v): %v",
					*gc.host, *gc.wait, err)
				newIRC = true
				time.Sleep(*gc.wait)
				continue
			}
			newIRC = false
		}
		/* Get a channel for the pipe when IRC is ready */
		if ircReady && (nil == pipe || newPipe) {
			/* Get the real nick */
			if "nick" == *gc.pipe && "" == onick {
				/* Try to get the server's idea of the nick */
				onick = irc.SNick()
				/* If it fails, revert to the original nick */
				if "" == onick {
					onick = *gc.nick
				}
			}

			var err error = nil
			pipe, err = makePipe(*gc.pipe, onick, *gc.flush)
			/* Retry if we have an error */
			if nil != err {
				verbose("Error opening pipe %v (retry in "+
					"%v): %v", *gc.pipe, *gc.wait, err)
				time.Sleep(*gc.wait)
				newPipe = true
				continue
			}
			debug("Using pipe: %v", pipe.Pname)
			/* Remove pipe if we made it before exit */
			if "nick" == *gc.pipe {
				rempname = pipe.Pname
			}
		}

		/* Try to send txbuf before the select */
		if nil != txbuf {
			if err := irc.Privmsg(*txbuf, ""); nil != err {
				verbose("Error sending buffered message: %v",
					err)
			}
			/* Try again in a bit */
			newIRC = true
			continue
		}

		/* Handle an event */
		newPipe, newIRC, ircReady, txbuf, err = handleEvent(pipe, irc,
			ircReady, txbuf)
		if io.EOF == err && nil != pipe && "-" == pipe.Pname {
			/* End of stdin */
			return 0
		} else if err != nil {
			verbose("Error handling an event: %v", err)
			return -1
		}
	}
}

/* Wait for something to happen, handle it */
func handleEvent(pipe *Pipe, irc *minimalirc.IRC, iircReady bool,
	itxbuf *string) (newPipe bool, newIRC bool,
	ircReady bool, txbuf *string, err error) {

	/* We actually use output arguments */
	ircReady = iircReady
	txbuf = itxbuf

	/* Set the pipe channel in the select to nil if we've not yet got in
	the IRC channel */
	var p <-chan string
	if !ircReady || nil == pipe {
		p = nil
	} else {
		p = pipe.R
	}

	/* KQueueish select */
	select {
	case l, ok := <-p: /* Line to send */
		/* Handle a closed pipe */
		if !ok {
			err = <-pipe.E
			/* If it's stdin's EOF, we're done */
			if "-" == pipe.Pname && io.EOF == err {
				break
			}
			err = errors.New(fmt.Sprintf("Error reading from "+
				"pipe: %v", err))
			newPipe = true
		}
		/* Store the line in the TX buffer */
		txbuf = &l

		/* Work out the max size of a message */
		max := irc.PrivmsgSize("")

		/* Put the strings into an array */
		txarr := ArrayOfShortStrings(l, max)

		/* Send message to IRC server */
		for _, m := range txarr {
			if err = irc.Privmsg(m, ""); nil != err {
				err = errors.New(fmt.Sprintf("Error sending "+
					"message: %v", err))
				irc.Quit("")
				newIRC = true
				break
			}
			/* Delay after sending a picture */
			time.Sleep(*gc.senddelay)
		}
		/* If the message(s) sent ok, clear the TX buffer and sleep to
		avoid flooding. */
		if nil == err {
			txbuf = nil
			/* Sleep a bit to avoid flooding */
			time.Sleep(*gc.senddelay)
		}
	case l, ok := <-irc.C: /* Message from IRC server */
		/* Check if connection died */
		if !ok {
			/* Get the error */
			err := <-irc.E
			/* Try to close the connection, for just in
			case */
			if e := irc.Quit(*gc.qmsg); e != nil {
				debug("Error closing connection to "+
					"the IRC server: %v", e)
			}
			verbose("IRC server error (reconnect in "+
				"%v): %v", *gc.wait, err)
			/* Signal to make a new one next time */
			newIRC = true
		}
		/* Check if we've joined a channel */
		if re.ChannelJoined.MatchString(l) {
			debug("Joined a channel: %v", l)
			ircReady = true
		}
		/* Retry the nick if it's in use */
		if re.NickInUse.MatchString(l) {
			verbose("Nick is in use, will try another")
			irc.RandomNumbers = true
			if err = irc.Handshake(); err != nil {
				err = errors.New(fmt.Sprintf("unable to "+
					"retry handshake: %v", err))
				newIRC = true
				break
			}
		}
	}
	return
}

/* ArrayOfShortStrings splits s into an array of strings of length no more than
l bytes, keeping runes together. */
func ArrayOfShortStrings(s string, l int) []string {
	/* Easy case, string fits */
	if len(s) <= l {
		return []string{s}
	}
	/* Come up with a guess for the number of smaller strings */
	nsmaller := int(math.Ceil(float64(len(s) / l)))
	/* Make an array with a capacity of double that, just in case */
	o := make([]string, 0, 2*nsmaller)
	/* Split s into runes */
	r := []rune(s)
	/* Working string */
	w := ""
	/* Index to r */
	for i := 0; i < len(r); i++ {
		/* If rune is larger than the string size, replace with ? */
		if len(string(r[i])) > l {
			r[i] = '?'
		}
		/* If adding the current rune to the current string would be
		too big, save it and start a new one */
		if len(w+string(r[i])) > l {
			o = append(o, w)
			w = ""
		}
		w += string(r[i])
	}
	/* Append the final working string */
	return append(o, w)
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
