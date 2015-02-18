//
// Forked and simplified from http://golang.org/src/pkg/log/syslog/syslog.go
// Fork needed to set the proper hostname in the write() function
//

package syslogwriter

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
)

type tlsWriter struct {
	appId     string
	raddr     string
	scheme    string
	outputUrl *url.URL
	connected bool

	mu   sync.Mutex // guards conn
	conn net.Conn

	tlsConfig *tls.Config
}

func NewTlsWriter(outputUrl *url.URL, appId string, skipCertVerify bool) (w *tlsWriter, err error) {
	if outputUrl.Scheme != "syslog-tls" {
		return nil, errors.New(fmt.Sprintf("Invalid scheme %s, tlsWriter only supports syslog-tls", outputUrl.Scheme))
	}
	tlsConfig := &tls.Config{InsecureSkipVerify: skipCertVerify}
	return &tlsWriter{
		appId:     appId,
		outputUrl: outputUrl,
		raddr:     outputUrl.Host,
		connected: false,
		scheme:    outputUrl.Scheme,
		tlsConfig: tlsConfig,
	}, nil
}

func (w *tlsWriter) Connect() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	var err error
	if strings.Contains(w.scheme, "syslog-tls") {
		err = w.connectTLS()
	}
	if err == nil {
		w.SetConnected(true)
	}
	return err
}

func (w *tlsWriter) connectTLS() error {
	if w.conn != nil {
		// ignore err from close, it makes sense to continue anyway
		w.conn.Close()
		w.conn = nil
	}
	c, err := tls.Dial("tcp", w.raddr, w.tlsConfig)
	if err == nil {
		w.conn = c
	}
	return err
}

func (w *tlsWriter) WriteStdout(b []byte, source string, sourceId string, timestamp int64) (int, error) {
	return w.write(14, source, sourceId, string(b), timestamp)
}

func (w *tlsWriter) WriteStderr(b []byte, source string, sourceId string, timestamp int64) (int, error) {
	return w.write(11, source, sourceId, string(b), timestamp)
}

func (w *tlsWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conn != nil {
		err := w.conn.Close()
		w.conn = nil
		return err
	}
	return nil
}

func (w *tlsWriter) write(p int, source string, sourceId string, msg string, timestamp int64) (byteCount int, err error) {
	syslogMsg := createMessage(p, w.appId, source, sourceId, msg, timestamp)
	// Frame msg with Octet Counting: https://tools.ietf.org/html/rfc6587#section-3.4.1
	finalMsg := fmt.Sprintf("%d %s", len(syslogMsg), syslogMsg)

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conn != nil {
		byteCount, err = fmt.Fprint(w.conn, finalMsg)
	}
	return byteCount, err
}

func (w *tlsWriter) IsConnected() bool {
	return w.connected
}

func (w *tlsWriter) SetConnected(newValue bool) {
	w.connected = newValue
}