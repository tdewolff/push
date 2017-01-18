package push

import (
	"errors"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	"path"
	"sync"
)

// ErrRecursivePush is returned when the request was initiated by a push. This is determined via the X-Pushed header.
var ErrRecursivePush = errors.New("recursive push")

// ExtToMimetype is an extension -> mimetype mapping used in ResponseWriter when the Content-Type header is not set.
var ExtToMimetype = map[string]string{
	"":      "text/html",
	".html": "text/html",
	".css":  "text/css",
	".svg":  "image/svg+xml",
}

type P struct {
	baseURL string
	opener  FileOpener
	cache   Cache
}

func New(baseURL string, opener FileOpener, cache Cache) *P {
	return &P{baseURL, opener, cache}
}

type pushingWriter struct {
	pw  *io.PipeWriter
	wg  sync.WaitGroup
	err error
}

func (w *pushingWriter) Write(b []byte) (int, error) {
	return w.pw.Write(b)
}

func (w *pushingWriter) Close() error {
	w.pw.Close()
	w.wg.Wait()
	return w.err
}

func Writer(w io.Writer, parser *Parser, mimetype, uri string) *pushingWriter {
	pr, pw := io.Pipe()
	writer := &pushingWriter{pw, sync.WaitGroup{}, nil}
	writer.wg.Add(1)
	go func() {
		defer writer.wg.Done()

		tr := io.TeeReader(pr, w)
		if err := parser.Parse(tr, mimetype, uri); err != nil {
			io.Copy(ioutil.Discard, tr) // drain pr to cause writes through TeeReader
			writer.err = err
		}
		pr.Close()
	}()
	return writer
}

// ResponseWriterCloser makes sure that all data has been written on calling Close (can be blocking).
type ResponseWriterCloser interface {
	http.ResponseWriter
	Close() error
}

type nopResponseWriter struct {
	http.ResponseWriter
}

func (_ *nopResponseWriter) Close() error {
	return nil
}

type pushingResponseWriter struct {
	http.ResponseWriter

	writer   *pushingWriter
	parser   *Parser
	mimetype string
	uri      string
}

func (w *pushingResponseWriter) Write(b []byte) (int, error) {
	if w.writer == nil {
		// first write
		if mediatype := w.ResponseWriter.Header().Get("Content-Type"); mediatype != "" {
			if mimetype, _, err := mime.ParseMediaType(mediatype); err != nil {
				w.mimetype = mimetype
			}
		}
		w.writer = Writer(w.ResponseWriter, w.parser, w.mimetype, w.uri)
	}
	return w.writer.Write(b)
}

func (w *pushingResponseWriter) Close() error {
	if w.writer != nil {
		return w.writer.Close()
	}
	return nil
}

// ResponseWriter wraps a ResponseWriter interface. It parses anything written to the returned ResponseWriter and pushes local resources to the client. If FileOpener is not nil, it will read and parse the referenced URIs recursively. If Cache is not nil, it will cache the URIs found and use it on subsequent requests.
// ResponseWriter can only return ErrNoPusher, ErrRecursivePush or ErrNoParser errors.
// Parsing errors are returned by Close on the writer. The writer must be closed explicitly.
func (p *P) ResponseWriter(w http.ResponseWriter, r *http.Request) (ResponseWriterCloser, error) {
	if r.Header.Get("X-Pushed") == "1" {
		return &nopResponseWriter{w}, ErrRecursivePush
	}

	pusher, err := NewPushHandlerFromResponseWriter(w)
	if err != nil {
		return &nopResponseWriter{w}, err
	}

	var uriHandler URIHandler
	if p.cache != nil {
		if resources, ok := p.cache.Get(r.RequestURI); ok {
			for _, uri := range resources {
				if err = pusher.URI(uri); err != nil {
					return &nopResponseWriter{w}, err
				}
			}
			return &nopResponseWriter{w}, nil
		}

		p.cache.Del(r.RequestURI)
		uriHandler = URIHandlerFunc(func(uri string) error {
			p.cache.Add(r.RequestURI, uri)
			return pusher.URI(uri)
		})
	} else {
		uriHandler = pusher
	}

	parser, err := NewParser(p.baseURL, p.opener, uriHandler)
	if err != nil {
		return &nopResponseWriter{w}, err
	}

	mimetype, _ := ExtToMimetype[path.Ext(r.RequestURI)]
	return &pushingResponseWriter{w, nil, parser, mimetype, r.RequestURI}, nil
}

// Middleware wraps an http.Handler and pushes local resources to the client. If FileOpener is not nil, it will read and parse the referenced URIs recursively. If Cache is not nil, it will cache the URIs found and use it on subsequent requests.
func (p *P) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pw, _ := p.ResponseWriter(w, r)
		next.ServeHTTP(pw, r)
		pw.Close()
	})
}

// List parses r with mimetype and served by uri. It returns a list of local resource URIs. If FileOpener is not nil, it will read and parse the referenced URIs recursively.
func List(baseURL string, opener FileOpener, r io.Reader, mimetype, uri string) ([]string, error) {
	h := NewListHandler()
	parser, err := NewParser(baseURL, opener, h)
	if err != nil {
		return h.URIs, err
	}
	if err = parser.Parse(r, mimetype, uri); err != nil {
		return h.URIs, err
	}
	return h.URIs, nil
}

// Reader wraps an io.Reader that parses r with mimetype and served by uri. Any reads done at the returned reader will be parsed concurrently.
func Reader(r io.Reader, parser *Parser, mimetype, uri string) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		r = io.TeeReader(r, pw)
		if err := parser.Parse(r, mimetype, uri); err != nil {
			pw.CloseWithError(err)
		} else {
			pw.Close()
		}
	}()
	return pr
}
