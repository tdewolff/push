package push

import (
	"errors"
	"io"
	"net/http"
	"path"
	"sync"
)

var ErrNoPusher = errors.New("ResponseWriter is not a Pusher")
var ErrRecursivePush = errors.New("recursive push")

////////////////

type Push struct {
	baseURI string
	dir     http.Dir
}

func New(baseURI string, dir http.Dir) *Push {
	return &Push{baseURI, dir}
}

// pipedResponseWriter makes sure that all data has been written on calling Close (can be blocking).
type pipedResponseWriter struct {
	http.ResponseWriter
	pw  *io.PipeWriter
	wg  sync.WaitGroup
	err error
}

func (w *pipedResponseWriter) Write(b []byte) (int, error) {
	return w.pw.Write(b)
}

func (w *pipedResponseWriter) Close() error {
	w.pw.Close()
	w.wg.Wait()
	return w.err
}

// ResponseWriter wraps a ResponseWriter interface and pushes any resources to the client.
// Errors are returned by Close on the writer.
// The writer must be closed explicitly.
func (p *Push) ResponseWriter(w http.ResponseWriter, r *http.Request) (*pipedResponseWriter, error) {
	pusher, ok := w.(http.Pusher)
	if !ok {
		return nil, ErrNoPusher
	}

	if r.Header.Get("X-Pushed") == "1" {
		// r.Header.Del("X-Pushed") // data race with read at net/http.(*http2sorter).Keys()
		return nil, ErrRecursivePush
	}

	// when no parser exists, avoiding to start a goroutine with a TeeReader into drainReader
	if _, ok := ExtToMimetype[path.Ext(r.RequestURI)]; !ok {
		return nil, ErrNoParser
	}

	opts := &http.PushOptions{Header: http.Header{"X-Pushed": {"1"}}}

	pr, pw := io.Pipe()
	pipeResponseWriter := &pipedResponseWriter{w, pw, sync.WaitGroup{}, nil}
	pipeResponseWriter.wg.Add(1)
	go func() {
		tr := io.TeeReader(pr, w)
		if err := p.Push(tr, r.Host, r.RequestURI, pusher, opts); err != nil {
			drainReader(tr)
			pipeResponseWriter.err = err
		}
		pr.Close()
		pipeResponseWriter.wg.Done()
	}()
	return pipeResponseWriter, nil
}

func drainReader(r io.Reader) {
	b := make([]byte, 1024)
	for {
		if _, err := r.Read(b); err != nil {
			break
		}
	}
}

// Push pushes any resource URIs found in r to pusher. It reads resources recursively. Whether a resource is local is determined by host + uri.
func (p *Push) Push(r io.Reader, host, uri string, pusher http.Pusher, opts *http.PushOptions) error {
	var parseErr error
	uriChan := make(chan string, 5)
	go func() {
		parseErr = p.ParseAll(r, host, uri, uriChan)
		close(uriChan)
	}()

	var err error
	for uri := range uriChan {
		pushErr := pusher.Push(uri, opts)
		if err == nil {
			err = pushErr
		}
	}

	if parseErr != nil {
		return parseErr
	}
	return err
}

// Reader reads from r and returns a reader that will send any local resource URI to uriChan. Any reads done at the returned reader will concurrently be parsed for resource URIs. It reads resources recursively. Whether a resource is local is determined by host + uri.
func (p *Push) Reader(r io.Reader, host, uri string, uriChan chan<- string) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		r = io.TeeReader(r, pw)
		if err := p.ParseAll(r, host, uri, uriChan); err != nil {
			pw.CloseWithError(err)
		} else {
			pw.Close()
		}
	}()
	return pr
}
