package push

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/tdewolff/parse"
	"github.com/tdewolff/parse/css"
	"github.com/tdewolff/parse/html"
)

var ErrNoPusher = errors.New("ResponseWriter is not a Pusher")
var ErrNoParser = errors.New("mimetype has no parser")
var ErrRecursivePush = errors.New("recursive push")

type Errors []error

func (errs Errors) Error() string {
	if len(errs) == 1 {
		return errs[0].Error()
	}
	s := ""
	for i, err := range errs {
		if i > 0 {
			s += "\n"
		}
		s += err.Error()
	}
	return s
}

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
	pw *io.PipeWriter
	wg sync.WaitGroup
}

func (w *pipedResponseWriter) Write(b []byte) (int, error) {
	return w.pw.Write(b)
}

func (w *pipedResponseWriter) Close() error {
	err := w.pw.Close()
	w.wg.Wait()
	return err
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

	reqURL, err := url.Parse(r.Host + r.RequestURI)
	if err != nil {
		return nil, err
	}

	mimetype := "text/html"
	if dot := strings.IndexByte(r.RequestURI, '.'); dot > -1 {
		mediatype := mime.TypeByExtension(r.RequestURI[dot:])
		mimetype, _, err = mime.ParseMediaType(mediatype)
		if err != nil {
			return nil, err
		}
	}

	opts := &http.PushOptions{Header: http.Header{"X-Pushed": {"1"}}}

	pr, pw := io.Pipe()
	pipeResponseWriter := &pipedResponseWriter{w, pw, sync.WaitGroup{}}
	pipeResponseWriter.wg.Add(1)
	go func() {
		tr := io.TeeReader(pr, w)
		if _, err = p.Push(tr, reqURL, mimetype, pusher, opts); err != nil {
			drainReader(tr)
			pr.CloseWithError(err)
		} else {
			pr.Close()
		}
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

// Reader reads from r and returns a reader. Any reads done at the returned reader will concurrently be parsed for resource URIs. Whether a resource is local is determined by reqURL. It accepts only text/html and text/css as mimetypes.
func (p *Push) Reader(r io.Reader, reqURL *url.URL, mimetype string, uris chan<- string) io.Reader {
	uriParser := func(uri string) error {
		uris <- uri
		return nil
	}

	pr, pw := io.Pipe()
	go func() {
		r = io.TeeReader(r, pw)
		if err := p.Parse(r, reqURL, mimetype, uriParser); err != nil {
			pw.CloseWithError(err)
		} else {
			pw.Close()
		}
	}()
	return pr
}

// Push pushes any resource URIs found in r to pusher. Whether a resource is local is determined by reqURL. It accepts only text/html and text/css as mimetypes.
func (p *Push) Push(r io.Reader, reqURL *url.URL, mimetype string, pusher http.Pusher, opts *http.PushOptions) ([]string, error) {
	wg := sync.WaitGroup{}
	defer wg.Wait()

	uris := []string{}
	uriChan := make(chan string, 5)
	defer close(uriChan)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for uri := range uriChan {
			uris = append(uris, uri)
		}
	}()

	var uriParser URIParser
	uriParser = func(uri string) error {
		pushErr := pusher.Push(uri, opts)
		uriChan <- uri

		// recursively read and parse the referenced URIs
		if p.dir != "" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				file, err := p.dir.Open(uri)
				if err != nil {
					return
				}

				resourceReqURL, err := url.Parse(reqURL.Host + uri)
				if err != nil {
					return
				}

				resourceMimetype := "text/html"
				if dot := strings.IndexByte(uri, '.'); dot > -1 {
					mediatype := mime.TypeByExtension(uri[dot:])
					resourceMimetype, _, err = mime.ParseMediaType(mediatype)
					if err != nil {
						return
					}
				}

				if err := p.Parse(file, resourceReqURL, resourceMimetype, uriParser); err != nil {
					return
				}
			}()
		}
		return pushErr
	}

	if err := p.Parse(r, reqURL, mimetype, uriParser); err != nil {
		return uris, err
	}
	return uris, nil
}

// URIParser is a callback definition that is called when a resource URI is found.
type URIParser func(string) error

// Parse parses reads from r and sends any local resource URI to uriParser. Whether a resource is local is determined by reqURL. It accepts only text/html and text/css as mimetypes.
func (p *Push) Parse(r io.Reader, reqURL *url.URL, mimetype string, uriParser URIParser) error {
	if mimetype == "text/html" {
		return p.ParseHTML(r, reqURL, uriParser)
	} else if mimetype == "text/css" {
		return p.ParseCSS(r, reqURL, uriParser, false)
	}
	// TODO: SVG
	return ErrNoParser
}

// ParseHTML parses r as an HTML file and sends any local resource URI to uriParser. Whether a resource is local is determined by reqURL.
func (p *Push) ParseHTML(r io.Reader, reqURL *url.URL, uriParser URIParser) error {
	lexer := html.NewLexer(r)
	for {
		tt, _ := lexer.Next()
		switch tt {
		case html.ErrorToken:
			if lexer.Err() == io.EOF {
				return nil
			}
			return lexer.Err()
		case html.StartTagToken:
			hash := html.ToHash(lexer.Text())
			if hash == html.Link || hash == html.Script || hash == html.Img || hash == html.Object || hash == html.Source || hash == html.Audio || hash == html.Video || hash == html.Track || hash == html.Embed || hash == html.Input || hash == html.Iframe {
				for {
					attrTokenType, _ := lexer.Next()
					if attrTokenType != html.AttributeToken {
						break
					}
					attrHash := html.ToHash(lexer.Text())

					if attrHash == html.Src || attrHash == html.Srcset || attrHash == html.Poster || attrHash == html.Data || attrHash == html.Href && hash == html.Link {
						attrVal := lexer.AttrVal()
						if len(attrVal) > 1 && (attrVal[0] == '"' || attrVal[0] == '\'') {
							attrVal = parse.TrimWhitespace(attrVal[1 : len(attrVal)-1])
						}

						if attrHash == html.Srcset {
							// TODO
							fmt.Println(string(attrVal))
						} else {
							if err := p.parseURL(string(attrVal), reqURL, uriParser); err != nil {
								return err
							}
						}
					}
				}
			}
			// TODO: CSS style tag and attribute, SVG
		}
	}
}

// ParseCSS parses r as a CSS file and sends any local resource URI to uriParser. Whether a resource is local is determined by reqURL.
func (p *Push) ParseCSS(r io.Reader, reqURL *url.URL, uriParser URIParser, isInline bool) error {
	parser := css.NewParser(r, isInline)
	for {
		gt, _, _ := parser.Next()
		if gt == css.ErrorGrammar {
			if parser.Err() == io.EOF {
				return nil
			}
			return parser.Err()
		} else if gt == css.DeclarationGrammar {
			vals := parser.Values()
			for _, val := range vals {
				if val.TokenType == css.URLToken && len(val.Data) > 7 {
					url := val.Data[5 : len(val.Data)-2]
					if err := p.parseURL(string(url), reqURL, uriParser); err != nil {
						return err
					}
				}
			}
		}
	}
}

func (p *Push) parseURL(srcRawURL string, reqURL *url.URL, uriParser URIParser) error {
	srcURL, err := url.Parse(srcRawURL)
	if err != nil {
		return err
	}

	srcURL.Path = "/" + srcURL.Path
	// TODO: relative URLs, absolute URLs and prepending /
	if (srcURL.Host == "" || srcURL.Host == reqURL.Host) && strings.HasPrefix(srcURL.Path, p.baseURI) {
		if err = uriParser(srcURL.Path); err != nil {
			return err
		}
	}
	return nil
}
