package push

import (
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"path"
	"sync"
)

// ErrNoPusher is returned when the ResponseWriter does not implement the Pusher interface.
var ErrNoPusher = errors.New("ResponseWriter is not a Pusher")

// ErrRecursivePush is returned when the request was initiated by a push. This is determined via the X-Pushed header.
var ErrRecursivePush = errors.New("recursive push")

////////////////

type Pusher struct {
	pusher http.Pusher
	opts   *http.PushOptions
}

func NewPusher(pusher http.Pusher, opts *http.PushOptions) *Pusher {
	return &Pusher{pusher, opts}
}

func (p *Pusher) URI(uri string) error {
	return p.pusher.Push(uri, p.opts)
}

////////////////

func Middleware(next http.Handler, lookup *Lookup, opts *http.PushOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pw, err := ResponseWriter(w, r, lookup, opts)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		next.ServeHTTP(pw, r)
		pw.Close()
	})
}

// ResponseWriterCloser makes sure that all data has been written on calling Close (can be blocking).
type ResponseWriterCloser struct {
	http.ResponseWriter

	pw  *io.PipeWriter
	wg  sync.WaitGroup
	err error
}

func (w *ResponseWriterCloser) Write(b []byte) (int, error) {
	return w.pw.Write(b)
}

func (w *ResponseWriterCloser) Close() error {
	w.pw.Close()
	w.wg.Wait()
	return w.err
}

// ResponseWriter wraps a ResponseWriter interface. It parses anything written to the returned ResponseWriter and pushes local resources to the client.
// ResponseWriter can only return ErrNoPusher, ErrRecursivePush or ErrNoParser errors.
// Parsing errors are returned by Close on the writer. The writer must be closed explicitly.
func ResponseWriter(w http.ResponseWriter, r *http.Request, lookup *Lookup, opts *http.PushOptions) (*ResponseWriterCloser, error) {
	if r.Header.Get("X-Pushed") == "1" {
		// r.Header.Del("X-Pushed") // data race with read at net/http.(*http2sorter).Keys()
		return nil, ErrRecursivePush
	}

	httpPusher, ok := w.(http.Pusher)
	if !ok {
		return nil, ErrNoPusher
	}
	if opts == nil {
		opts = &http.PushOptions{"", http.Header{}}
	}
	opts.Header.Set("X-Pushed", "1")

	mimetype, ok := ExtToMimetype[path.Ext(r.RequestURI)]
	if !ok {
		return nil, ErrNoParser
	}

	pusher := NewPusher(httpPusher, opts)
	parser, err := NewParser(lookup, r.RequestURI)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	responseWriterCloser := &ResponseWriterCloser{w, pw, sync.WaitGroup{}, nil}
	responseWriterCloser.wg.Add(1)
	go func() {
		tr := io.TeeReader(pr, w)
		if err := parser.Parse(tr, mimetype, pusher); err != nil {
			io.Copy(ioutil.Discard, tr)
			responseWriterCloser.err = err
		}
		pr.Close()
		responseWriterCloser.wg.Done()
	}()
	return responseWriterCloser, nil
}
