package syslog

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"text/template"
	"time"

	"golang.org/x/net/proxy"

	"github.com/segmentio/ecs-logs/lib"
)

const DefaultTemplate = "<{{.PRIVAL}}>{{.TIMESTAMP}} {{.GROUP}}[{{.STREAM}}]: {{.MSG}}"

// Global writer pool - channel style.
// When NewWriter is called, a writer is taken from the pool if available.
// Otherwise a new writer is created. Since each writer is configured
// using the same environment variables, any writer can be used by any caller.
// When writer.Close() is called, the writer is put back into the pool if
// no errors have occured, or closed and discarded otherwise.
var (
	writerPool = make(chan *writer, 100) // 100 is arbitrary.

	// Useful for testing
	enablePooling  = false
	newConnections uint64
)

type WriterConfig struct {
	Network    string
	Address    string
	Template   string
	TimeFormat string
	Tag        string
	TLS        *tls.Config
	SocksProxy string
}

func NewWriter(group, stream string) (lib.Writer, error) {
	if enablePooling {
		// Retrieve a writer from the global pool, if possible.
		var w *writer
		select {
		case w = <-writerPool:
			return w, nil
		default:
			// No writers immediately available; make a new one.
		}
	}

	atomic.AddUint64(&newConnections, 1)

	var c WriterConfig

	if s := os.Getenv("SYSLOG_URL"); len(s) != 0 {
		u, err := url.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("invalid syslog URL: %s", err)
		}

		c.Network = u.Scheme
		c.Address = u.Host
	}

	c.Template = os.Getenv("SYSLOG_TEMPLATE")
	c.TimeFormat = os.Getenv("SYSLOG_TIME_FORMAT")

	return DialWriter(c)
}

func DialWriter(config WriterConfig) (w lib.Writer, err error) {
	var netopts []string
	var addropts []string

	if len(config.Network) != 0 {
		netopts = []string{config.Network}
	} else if len(config.Address) == 0 || strings.HasPrefix(config.Address, "/") {
		// When starting with a '/' we assume it's gonna be a file path,
		// otherwise we fallback to trying a TLS connection so we don't
		// implicitly send logs over an unsecured link.
		config.Network = "unix"
		netopts = []string{"unixgram", "unix"}
	} else {
		netopts = []string{"tls"}
	}

	if len(config.Address) != 0 {
		addropts = []string{config.Address}
	} else if strings.HasPrefix(config.Network, "unix") {
		// This was copied from the standard log/syslog package, they do the same
		// and try to guess at runtime which socket syslogd is using.
		addropts = []string{"/dev/log", "/var/run/syslog", "/var/run/log"}
	} else {
		// The config doesn't point to a unix domain socket, falling back to trying
		// to connect to syslogd over a network interface.
		addropts = []string{"localhost:514"}
	}

	var backend io.WriteCloser

connect:
	for _, n := range netopts {
		for _, a := range addropts {
			if backend, err = dialWriter(n, a, config.TLS, config.SocksProxy); err == nil {
				break connect
			}
		}
	}

	if err != nil {
		return
	}

	w = newWriter(writerConfig{
		backend:    backend,
		template:   config.Template,
		timeFormat: config.TimeFormat,
		tag:        config.Tag,
	})
	return
}

type writerConfig struct {
	backend    io.WriteCloser
	template   string
	timeFormat string
	tag        string
}

func newWriter(config writerConfig) *writer {
	var out func(*writer, message) error
	var flush func() error

	if len(config.timeFormat) == 0 {
		config.timeFormat = time.Stamp
	}

	if len(config.template) == 0 {
		config.template = DefaultTemplate
	}

	switch b := config.backend.(type) {
	case bufferedWriter:
		out, flush = (*writer).directWrite, b.Flush
	default:
		out, flush = (*writer).bufferedWrite, func() error { return nil }
	}

	return &writer{
		writerConfig: config,
		flush:        flush,
		out:          out,
		tpl:          newWriterTemplate(config.template),
	}
}

func newWriterTemplate(format string) *template.Template {
	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	t := template.New("syslog")
	template.Must(t.Parse(format))
	return t
}

type writer struct {
	writerConfig
	buf   bytes.Buffer
	tpl   *template.Template
	out   func(*writer, message) error
	flush func() error

	// Global writer pool state
	dead bool
}

func (w *writer) Close() (err error) {
	if w.dead {
		return w.backend.Close()
	}

	if enablePooling {
		// w is still fine, put it back in the pool for reuse.
		// If the pool is full, discard this writer.
		select {
		case writerPool <- w:
			return nil
		default:
		}
	}

	return w.backend.Close()
}

func (w *writer) WriteMessageBatch(batch lib.MessageBatch) error {
	for _, msg := range batch {
		if err := w.write(msg); err != nil {
			w.dead = true
			return err
		}
	}
	if err := w.flush(); err != nil {
		w.dead = true
		return err
	}
	return nil
}

func (w *writer) WriteMessage(msg lib.Message) error {
	if err := w.write(msg); err != nil {
		w.dead = true
		return err
	}
	if err := w.flush(); err != nil {
		w.dead = true
		return err
	}

	return nil
}

func (w *writer) write(msg lib.Message) (err error) {
	m := message{
		PRIVAL:    int(msg.Event.Level-1) + 8, // +8 is for user-level messages facility
		HOSTNAME:  msg.Event.Info.Host,
		MSGID:     msg.Event.Info.ID,
		GROUP:     msg.Group,
		STREAM:    msg.Stream,
		TIMESTAMP: msg.Event.Time.Format(w.timeFormat),
		TAG:       w.tag,
	}

	if len(m.HOSTNAME) == 0 {
		m.HOSTNAME = "-"
	}

	if len(m.MSGID) == 0 {
		m.MSGID = "-"
	}

	if msg.Event.Info.PID == 0 {
		m.PROCID = "-"
	} else {
		m.PROCID = strconv.Itoa(msg.Event.Info.PID)
	}

	m.MSG = msg.Event.String()
	return w.out(w, m)
}

func (w *writer) directWrite(m message) (err error) {
	return w.tpl.Execute(w.backend, m)
}

func (w *writer) bufferedWrite(m message) (err error) {
	w.buf.Reset()
	w.tpl.Execute(&w.buf, m)
	_, err = w.backend.Write(w.buf.Bytes())
	return
}

type message struct {
	PRIVAL    int
	HOSTNAME  string
	PROCID    string
	MSGID     string
	GROUP     string
	STREAM    string
	TAG       string
	MSG       string
	TIMESTAMP string
}

type bufferedWriter interface {
	Flush() error
}

type bufferedConn struct {
	buf  *bufio.Writer
	conn net.Conn
}

func (c bufferedConn) Close() error                { return c.conn.Close() }
func (c bufferedConn) Flush() error                { return c.buf.Flush() }
func (c bufferedConn) Write(b []byte) (int, error) { return c.buf.Write(b) }

func dialWriter(network, address string, config *tls.Config, socksProxy string) (w io.WriteCloser, err error) {
	var conn, rawConn net.Conn
	var dial func(string, string) (net.Conn, error)
	var socksDialer proxy.Dialer

	if network == "tls" {
		network, dial = "tcp", func(network, address string) (net.Conn, error) {
			return tls.Dial(network, address, config)
		}
	} else {
		dial = net.Dial
	}

	if socksProxy != "" {
		if socksDialer, err = proxy.SOCKS5(network, socksProxy, nil, proxy.Direct); err != nil {
			return
		}

		dial = func(network, address string) (conn net.Conn, err error) {
			if config == nil {
				conn, err = socksDialer.Dial(network, address)
			} else {
				rawConn, err = socksDialer.Dial(network, address)
				if err != nil {
					return nil, err
				}

				tlsConn := tls.Client(rawConn, config)
				if err = tlsConn.Handshake(); err == nil {
					conn = tlsConn
				}
			}
			return
		}
	}

	for attempt := 1; true; attempt++ {
		if conn, err = dial(network, address); err == nil {
			break
		}

		if attempt == 3 {
			return
		}

		err = nil
		time.Sleep(1 * time.Second)
	}

	if err == nil {
		switch network {
		case "udp", "udp4", "udp6", "unixgram", "unixpacket":
			w = conn
		default:
			w = bufferedConn{
				conn: conn,
				buf:  bufio.NewWriter(conn),
			}
		}
	}

	return
}
