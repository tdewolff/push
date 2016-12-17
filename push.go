package push

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"

	"github.com/tdewolff/buffer"
	"github.com/tdewolff/parse"
	"github.com/tdewolff/parse/css"
	"github.com/tdewolff/parse/html"
	"github.com/tdewolff/parse/svg"
	"github.com/tdewolff/parse/xml"
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
	opts := &http.PushOptions{Header: http.Header{"X-Pushed": {"1"}}}

	if r.Header.Get("X-Pushed") == "1" {
		// r.Header.Del("X-Pushed") // data race with read at net/http.(*http2sorter).Keys()
		return nil, ErrRecursivePush
	}

	reqURL, err := HostAndURIToURL(r.Host, r.RequestURI)
	if err != nil {
		return nil, err
	}
	mimetype, err := URIToMimetype(r.RequestURI)
	if err != nil || mimetype != "text/html" && mimetype != "text/css" && mimetype != "image/svg+xml" {
		return nil, ErrNoParser
	}

	pr, pw := io.Pipe()
	pipeResponseWriter := &pipedResponseWriter{w, pw, sync.WaitGroup{}, nil}
	pipeResponseWriter.wg.Add(1)
	go func() {
		tr := io.TeeReader(pr, w)
		if _, err = p.Push(tr, reqURL, mimetype, pusher, opts); err != nil {
			drainReader(tr)
			pipeResponseWriter.err = err
		}
		pr.Close()
		pipeResponseWriter.wg.Done()
	}()
	return pipeResponseWriter, nil
}

// Push pushes any resource URIs found in r to pusher. It reads resources recursively. Whether a resource is local is determined by reqURL. It accepts only text/html and text/css as mimetypes.
func (p *Push) Push(r io.Reader, reqURL *url.URL, mimetype string, pusher http.Pusher, opts *http.PushOptions) ([]string, error) {
	uris := []string{}

	var uriParser URIParser
	if p.dir == "" {
		uriParser = func(uri string) error {
			pushErr := pusher.Push(uri, opts)
			uris = append(uris, uri)
			return pushErr
		}
	} else {
		wg := sync.WaitGroup{}
		uriChan := make(chan string, 5)
		wgUriChan := sync.WaitGroup{}
		defer func() {
			wg.Wait()
			close(uriChan)
			wgUriChan.Wait()
		}()

		// process all extracted URIs
		wgUriChan.Add(1)
		go func() {
			defer wgUriChan.Done()

			for uri := range uriChan {
				uris = append(uris, uri)
			}
		}()

		// recursively read and parse the referenced URIs
		uriParser = func(uri string) error {
			pushErr := pusher.Push(uri, opts)
			uriChan <- uri

			// do not block current parser
			wg.Add(1)
			go func() {
				defer wg.Done()

				file, err := p.dir.Open(uri)
				if err != nil {
					return
				}

				resourceReqURL, err := HostAndURIToURL(reqURL.Host, uri)
				if err != nil {
					return
				}
				resourceMimetype, err := URIToMimetype(uri)
				if err != nil {
					return
				}

				if err := p.Parse(file, resourceReqURL, resourceMimetype, uriParser); err != nil {
					return
				}
			}()
			return pushErr
		}
	}

	if err := p.Parse(r, reqURL, mimetype, uriParser); err != nil {
		return uris, err
	}
	return uris, nil
}

// URIParser is a callback definition that is called when a resource URI is found.
type URIParser func(string) error

// Reader reads from r and returns a reader that will send any local resource URI to uriParser. Any reads done at the returned reader will concurrently be parsed for resource URIs. Whether a resource is local is determined by reqURL. It accepts only text/html and text/css as mimetypes.
func (p *Push) Reader(r io.Reader, url string, mimetype string, uriParser URIParser) io.Reader {
	reqURL, err := HostAndURIToURL("", url)
	if err != nil {
		return r
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

// Parse parses reads from r and sends any local resource URI to uriParser. Whether a resource is local is determined by reqURL. It accepts only text/html and text/css as mimetypes.
func (p *Push) Parse(r io.Reader, reqURL *url.URL, mimetype string, uriParser URIParser) error {
	if mimetype == "text/html" {
		return p.ParseHTML(r, reqURL, uriParser)
	} else if mimetype == "text/css" {
		return p.ParseCSS(r, reqURL, uriParser, false)
	} else if mimetype == "image/svg+xml" {
		return p.ParseSVG(r, reqURL, uriParser)
	}
	return ErrNoParser
}

// ParseHTML parses r as an HTML file and sends any local resource URI to uriParser. Whether a resource is local is determined by reqURL.
func (p *Push) ParseHTML(r io.Reader, reqURL *url.URL, uriParser URIParser) error {
	var tag html.Hash

	lexer := html.NewLexer(r)
	for {
		tt, data := lexer.Next()
		switch tt {
		case html.ErrorToken:
			if lexer.Err() == io.EOF {
				return nil
			}
			return lexer.Err()
		case html.StartTagToken:
			tag = html.ToHash(lexer.Text())
			for {
				attrTokenType, _ := lexer.Next()
				if attrTokenType != html.AttributeToken {
					break
				}

				if attr := html.ToHash(lexer.Text()); attr == html.Style || attr == html.Src || attr == html.Srcset || attr == html.Poster || attr == html.Data || attr == html.Href && tag == html.Link {
					attrVal := lexer.AttrVal()
					if len(attrVal) > 1 && (attrVal[0] == '"' || attrVal[0] == '\'') {
						attrVal = parse.TrimWhitespace(attrVal[1 : len(attrVal)-1])
					}

					if attr == html.Style {
						if err := p.ParseCSS(buffer.NewReader(attrVal), reqURL, uriParser, true); err != nil {
							return err
						}
					} else {
						if attr == html.Srcset {
							for _, uri := range parseSrcset(attrVal) {
								if err := p.parseURL(uri, reqURL, uriParser); err != nil {
									return err
								}
							}
						} else {
							if err := p.parseURL(string(attrVal), reqURL, uriParser); err != nil {
								return err
							}
						}
					}
				}
			}
		case html.SvgToken:
			if err := p.ParseSVG(buffer.NewReader(data), reqURL, uriParser); err != nil {
				return err
			}
		case html.TextToken:
			if tag == html.Style {
				if err := p.ParseCSS(buffer.NewReader(data), reqURL, uriParser, false); err != nil {
					return err
				}
			} else if tag == html.Iframe {
				if err := p.ParseHTML(buffer.NewReader(data), reqURL, uriParser); err != nil {
					return err
				}
			}
		}
		lexer.Free(len(data))
	}
}

func parseSrcset(b []byte) []string {
	uris := []string{}
	n := len(b)
	start := 0
	for i := 0; i < n; i++ {
		if b[i] == ',' {
			uris = append(uris, parseSrcsetCandidate(b[start:i]))
			start = i + 1
		}
	}
	return append(uris, parseSrcsetCandidate(b[start:]))
}

func parseSrcsetCandidate(b []byte) string {
	n := len(b)
	start := 0
	for i := 0; i < n; i++ {
		if !parse.IsWhitespace(b[i]) {
			start = i
			break
		}
	}
	end := n
	for i := start; i < n; i++ {
		if parse.IsWhitespace(b[i]) {
			end = i
			break
		}
	}
	return string(b[start:end])
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
				if val.TokenType == css.URLToken && len(val.Data) > 5 {
					url := val.Data[4 : len(val.Data)-1]
					if len(url) > 2 && (url[0] == '"' || url[0] == '\'') {
						url = url[1 : len(url)-1]
					}
					if !bytes.HasPrefix(url, []byte("data:")) {
						if err := p.parseURL(string(url), reqURL, uriParser); err != nil {
							return err
						}
					}
				}
			}
		}
	}
}

// ParseSVG parses r as an SVG file and sends any local resource URI to uriParser. Whether a resource is local is determined by reqURL.
func (p *Push) ParseSVG(r io.Reader, reqURL *url.URL, uriParser URIParser) error {
	var tag svg.Hash

	lexer := xml.NewLexer(r)
	for {
		tt, data := lexer.Next()
		switch tt {
		case xml.ErrorToken:
			if lexer.Err() == io.EOF {
				return nil
			}
			return lexer.Err()
		case xml.StartTagToken:
			tag = svg.ToHash(lexer.Text())
			for {
				attrTokenType, _ := lexer.Next()
				if attrTokenType != xml.AttributeToken {
					break
				}

				if attr := svg.ToHash(lexer.Text()); attr == svg.Style || (tag == svg.Image || tag == svg.Script || tag == svg.FeImage || tag == svg.Color_Profile || tag == svg.Use) && (attr == svg.Href || parse.Equal(lexer.Text(), []byte("xlink:href"))) {
					attrVal := lexer.AttrVal()
					if len(attrVal) > 1 && (attrVal[0] == '"' || attrVal[0] == '\'') {
						attrVal = parse.ReplaceMultipleWhitespace(parse.TrimWhitespace(attrVal[1 : len(attrVal)-1]))
					}

					if attr == svg.Style {
						if err := p.ParseCSS(buffer.NewReader(attrVal), reqURL, uriParser, true); err != nil {
							return err
						}
					} else {
						if err := p.parseURL(string(attrVal), reqURL, uriParser); err != nil {
							return err
						}
					}
				}
			}
		case xml.TextToken:
			if tag == svg.Style {
				if err := p.ParseCSS(buffer.NewReader(data), reqURL, uriParser, false); err != nil {
					return err
				}
			}
		}
		lexer.Free(len(data))
	}
}

func (p *Push) parseURL(rawURI string, reqURL *url.URL, uriParser URIParser) error {
	uri, err := HostAndURIToURL("", rawURI)
	if err != nil {
		return err
	}

	if uri.Host != "" && uri.Host != reqURL.Host {
		return nil
	}

	resolvedURI := reqURL.ResolveReference(uri)
	if strings.HasPrefix(resolvedURI.Path, p.baseURI) {
		if err = uriParser(resolvedURI.Path); err != nil {
			return err
		}
	}
	return nil
}

func HostAndURIToURL(host, uri string) (*url.URL, error) {
	reqURL, err := url.Parse(path.Join(host, uri))
	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(reqURL.Path, "localhost") || strings.HasPrefix(reqURL.Path, "127.0.0.1") {
		reqURL.Host = reqURL.Path[0:9]
		reqURL.Path = reqURL.Path[9:]
	}
	return reqURL, nil
}

func URIToMimetype(uri string) (string, error) {
	mimetype := "text/html"
	if dot := strings.IndexByte(uri, '.'); dot > -1 {
		mediatype := mime.TypeByExtension(uri[dot:])

		var err error
		if mimetype, _, err = mime.ParseMediaType(mediatype); err != nil {
			return "", err
		}
	}
	return mimetype, nil
}

func drainReader(r io.Reader) {
	b := make([]byte, 1024)
	for {
		if _, err := r.Read(b); err != nil {
			break
		}
	}
}
