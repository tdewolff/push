package push

import (
	"bytes"
	"errors"
	"io"
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

// ErrNoParser is returned when the mimetype has no parser specified.
var ErrNoParser = errors.New("mimetype has no parser")

// ExtToMimetype allows customization of the extension -> mimetype mapping.
var ExtToMimetype = map[string]string{
	"":      "text/html",
	".html": "text/html",
	".css":  "text/css",
	".svg":  "image/svg+xml",
}

// URIParser is a callback definition that is called when a resource URI is found.
type URIParser func(string) error

////////////////

type Parser struct {
	baseURI string
	dir     http.Dir
}

// NewParser returns a new Parser.
// baseURI defines the prefix an URI must have to be considered as a local resource.
// dir defines a path where to find these resources, so it can read, parse and push them to the client. Leave dir empty to disable recursive parsing.
func NewParser(baseURI string, dir http.Dir) *Parser {
	return &Parser{baseURI, dir}
}

// List parses r recursively and returns a list of local resource URIs. Whether a resource is local is determined by host + uri.
func (p *Parser) List(r io.Reader, host, uri string) ([]string, error) {
	var parseErr error
	uriChan := make(chan string, 5)
	go func() {
		parseErr = p.ParseAll(r, host, uri, uriChan)
		close(uriChan)
	}()

	uris := []string{}
	for uri := range uriChan {
		uris = append(uris, uri)
	}
	return uris, parseErr
}

// ParseAll parses r recursively and sends local resource URIs over uriChan. Whether a resource is local is determined by host + uri.
func (p *Parser) ParseAll(r io.Reader, host, uri string, uriChan chan<- string) error {
	var uriParser URIParser
	if p.dir == "" {
		uriParser = func(uri string) error {
			uriChan <- uri
			return nil
		}
	} else {
		wg := sync.WaitGroup{}
		defer wg.Wait()

		// recursively read and parse the referenced URIs
		uriParser = func(uri string) error {
			uriChan <- uri

			// do not block current parser
			wg.Add(1)
			go func() {
				defer wg.Done()

				file, err := p.dir.Open(uri)
				if err != nil {
					return
				}

				if err := p.Parse(file, host, uri, uriParser); err != nil {
					return
				}
			}()
			return nil
		}
	}
	return p.Parse(r, host, uri, uriParser)
}

// Parse parses r and calls uriParser when a local resource URI is found. Whether a resource is local is determined by host + uri.
func (p *Parser) Parse(r io.Reader, host, uri string, uriParser URIParser) error {
	reqURL, err := url.Parse(path.Join(host, uri))
	if err != nil {
		return err
	}
	if strings.HasPrefix(reqURL.Path, "localhost") || strings.HasPrefix(reqURL.Path, "127.0.0.1") {
		reqURL.Host = reqURL.Path[0:9]
		reqURL.Path = reqURL.Path[9:]
	}

	mimetype := ExtToMimetype[path.Ext(uri)]
	if mimetype == "text/html" {
		return p.parseHTML(r, reqURL, uriParser)
	} else if mimetype == "text/css" {
		return p.parseCSS(r, reqURL, uriParser, false)
	} else if mimetype == "image/svg+xml" {
		return p.parseSVG(r, reqURL, uriParser)
	}
	return ErrNoParser
}

// parseHTML parses r as an HTML file and sends any local resource URI to uriParser.
func (p *Parser) parseHTML(r io.Reader, reqURL *url.URL, uriParser URIParser) error {
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
						if err := p.parseCSS(buffer.NewReader(attrVal), reqURL, uriParser, true); err != nil {
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
			if err := p.parseSVG(buffer.NewReader(data), reqURL, uriParser); err != nil {
				return err
			}
		case html.TextToken:
			if tag == html.Style {
				if err := p.parseCSS(buffer.NewReader(data), reqURL, uriParser, false); err != nil {
					return err
				}
			} else if tag == html.Iframe {
				if err := p.parseHTML(buffer.NewReader(data), reqURL, uriParser); err != nil {
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

// parseCSS parses r as a CSS file and sends any local resource URI to uriParser.
func (p *Parser) parseCSS(r io.Reader, reqURL *url.URL, uriParser URIParser, isInline bool) error {
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

// parseSVG parses r as an SVG file and sends any local resource URI to uriParser.
func (p *Parser) parseSVG(r io.Reader, reqURL *url.URL, uriParser URIParser) error {
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
						if err := p.parseCSS(buffer.NewReader(attrVal), reqURL, uriParser, true); err != nil {
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
				if err := p.parseCSS(buffer.NewReader(data), reqURL, uriParser, false); err != nil {
					return err
				}
			}
		}
		lexer.Free(len(data))
	}
}

func (p *Parser) parseURL(rawRefURL string, reqURL *url.URL, uriParser URIParser) error {
	refURL, err := url.Parse(rawRefURL)
	if err != nil {
		return err
	}
	if strings.HasPrefix(refURL.Path, "localhost") || strings.HasPrefix(refURL.Path, "127.0.0.1") {
		refURL.Host = refURL.Path[0:9]
		refURL.Path = refURL.Path[9:]
	}

	if refURL.Host != "" && refURL.Host != reqURL.Host {
		return nil
	}

	resolvedURI := reqURL.ResolveReference(refURL)
	if strings.HasPrefix(resolvedURI.Path, p.baseURI) {
		if err = uriParser(resolvedURI.Path); err != nil {
			return err
		}
	}
	return nil
}
