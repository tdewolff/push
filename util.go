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

// Middleware wraps an http.Handler and pushes local resources to the client. If FileOpener is not nil, it will read and parse the referenced URIs recursively. If Cache is not nil, it will cache the URIs found and use it on subsequent requests.
func Middleware(baseURL string, opener FileOpener, cache Cache, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pw, err := ResponseWriter(w, r, baseURL, opener, cache)
		if err == nil {
			next.ServeHTTP(pw, r)
			pw.Close()
		} else if err == ErrNoParser || err == ErrRecursivePush {
			next.ServeHTTP(w, r)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}

// ResponseWriterCloser makes sure that all data has been written on calling Close (can be blocking).
type ResponseWriterCloser interface {
	Header() http.Header
	Write([]byte) (int, error)
	WriteHeader(int)
	Close() error
}

type nopResponseWriterCloser struct {
	http.ResponseWriter
}

func (w *nopResponseWriterCloser) Close() error {
	return nil
}

type pushingResponseWriterCloser struct {
	http.ResponseWriter

	pw  *io.PipeWriter
	wg  sync.WaitGroup
	err error
}

func (w *pushingResponseWriterCloser) Write(b []byte) (int, error) {
	return w.pw.Write(b)
}

func (w *pushingResponseWriterCloser) Close() error {
	w.pw.Close()
	w.wg.Wait()
	return w.err
}

// ResponseWriter wraps a ResponseWriter interface. It parses anything written to the returned ResponseWriter and pushes local resources to the client. If FileOpener is not nil, it will read and parse the referenced URIs recursively. If Cache is not nil, it will cache the URIs found and use it on subsequent requests.
// ResponseWriter can only return ErrNoPusher, ErrRecursivePush or ErrNoParser errors.
// Parsing errors are returned by Close on the writer. The writer must be closed explicitly.
func ResponseWriter(w http.ResponseWriter, r *http.Request, baseURL string, opener FileOpener, cache Cache) (ResponseWriterCloser, error) {
	//fmt.Println(time.Now(), "serv", r.RequestURI)

	if r.Header.Get("X-Pushed") == "1" {
		// r.Header.Del("X-Pushed") // data race with read at net/http.(*http2sorter).Keys()
		return &nopResponseWriterCloser{w}, ErrRecursivePush
	}

	pusher, err := NewPushHandlerFromResponseWriter(w)
	if err != nil {
		return &nopResponseWriterCloser{w}, err
	}

	var uriHandler URIHandler
	if cache != nil {
		if resources, ok := cache.Get(r.RequestURI); ok {
			for _, uri := range resources {
				if err = pusher.URI(uri); err != nil {
					return &nopResponseWriterCloser{w}, err
				}
			}
			return &nopResponseWriterCloser{w}, nil
		}

		cache.Del(r.RequestURI)
		uriHandler = URIHandlerFunc(func(uri string) error {
			cache.Add(r.RequestURI, uri)
			return pusher.URI(uri)
		})
	} else {
		uriHandler = pusher
	}

	parser, err := NewParser(baseURL, opener, uriHandler)
	if err != nil {
		return &nopResponseWriterCloser{w}, err
	}

	var mimetype string
	if mediatype := r.Header.Get("Content-Type"); mediatype != "" {
		mimetype, _, _ = mime.ParseMediaType(mediatype)
	} else {
		mimetype, _ = ExtToMimetype[path.Ext(r.RequestURI)]
	}
	if mimetype != "text/html" && mimetype != "text/css" && mimetype != "image/svg+xml" {
		return &nopResponseWriterCloser{w}, ErrNoParser
	}

	pr, pw := io.Pipe()
	wc := &pushingResponseWriterCloser{w, pw, sync.WaitGroup{}, nil}
	wc.wg.Add(1)
	go func() {
		defer wc.wg.Done()

		tr := io.TeeReader(pr, w)
		if err := parser.Parse(tr, mimetype, r.RequestURI); err != nil {
			io.Copy(ioutil.Discard, tr)
			wc.err = err
		}
		pr.Close()
	}()
	return wc, nil
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
func Reader(parser *Parser, r io.Reader, mimetype, uri string) io.Reader {
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
