package push

import (
	"bytes"
	"errors"
	"io"
	"net/url"
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

// URIHandler is a callback definition that is called when a resource URI is found.
type URIHandler interface {
	URI(string) error
}

type URIHandlerFunc func(string) error

func (f URIHandlerFunc) URI(uri string) error {
	return f(uri)
}

type FileOpener interface {
	Open(string) (io.Reader, string, error)
}

type FileOpenerFunc func(string) (io.Reader, string, error)

func (f FileOpenerFunc) Open(uri string) (io.Reader, string, error) {
	return f(uri)
}

////////////////

type Lookup struct {
	host    string
	baseURI string
	opener  FileOpener
}

// NewParser returns a new Parser.
// baseURI defines the prefix an URI must have to be considered as a local resource.
// dir defines a path where to find these resources, so it can read, parse and push them to the client. Leave dir empty to disable recursive parsing.
func NewLookup(host, baseURI string) *Lookup {
	return &Lookup{host, baseURI, nil}
}

func NewRecursiveLookup(host, baseURI string, opener FileOpener) *Lookup {
	return &Lookup{host, baseURI, opener}
}

func (l *Lookup) IsRecursive() bool {
	return l.opener != nil
}

type Parser struct {
	*Lookup
	reqURL *url.URL

	wg sync.WaitGroup
}

func NewParser(host, baseURI string, uri string) (*Parser, error) {
	reqURL, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	reqURL.Host = host
	return &Parser{NewLookup(host, baseURI), reqURL, sync.WaitGroup{}}, nil
}

func NewRecursiveParser(host, baseURI string, opener FileOpener, uri string) (*Parser, error) {
	reqURL, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	reqURL.Host = host
	return &Parser{NewRecursiveLookup(host, baseURI, opener), reqURL, sync.WaitGroup{}}, nil
}

func NewParserFromLookup(lookup *Lookup, uri string) (*Parser, error) {
	reqURL, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	reqURL.Host = lookup.host
	return &Parser{lookup, reqURL, sync.WaitGroup{}}, nil
}

// Parse parses r and calls uriParser when a local resource URI is found. Whether a resource is local is determined by host + uri.
func (p *Parser) Parse(r io.Reader, mimetype string, uriHandler URIHandler) error {
	defer p.wg.Wait()
	if mimetype == "text/html" {
		return p.parseHTML(r, uriHandler)
	} else if mimetype == "text/css" {
		return p.parseCSS(r, uriHandler, false)
	} else if mimetype == "image/svg+xml" {
		return p.parseSVG(r, uriHandler)
	}
	return ErrNoParser
}

////////////////

// parseHTML parses r as an HTML file and sends any local resource URI to uriParser.
func (p *Parser) parseHTML(r io.Reader, uriHandler URIHandler) error {
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
						if err := p.parseCSS(buffer.NewReader(attrVal), uriHandler, true); err != nil {
							return err
						}
					} else {
						if attr == html.Srcset {
							for _, uri := range parseSrcset(attrVal) {
								if err := p.parseURL(uri, uriHandler); err != nil {
									return err
								}
							}
						} else {
							if err := p.parseURL(string(attrVal), uriHandler); err != nil {
								return err
							}
						}
					}
				}
			}
		case html.SvgToken:
			if err := p.parseSVG(buffer.NewReader(data), uriHandler); err != nil {
				return err
			}
		case html.TextToken:
			if tag == html.Style {
				if err := p.parseCSS(buffer.NewReader(data), uriHandler, false); err != nil {
					return err
				}
			} else if tag == html.Iframe {
				if err := p.parseHTML(buffer.NewReader(data), uriHandler); err != nil {
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
func (p *Parser) parseCSS(r io.Reader, uriHandler URIHandler, isInline bool) error {
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
						if err := p.parseURL(string(url), uriHandler); err != nil {
							return err
						}
					}
				}
			}
		}
	}
}

// parseSVG parses r as an SVG file and sends any local resource URI to uriParser.
func (p *Parser) parseSVG(r io.Reader, uriHandler URIHandler) error {
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
						if err := p.parseCSS(buffer.NewReader(attrVal), uriHandler, true); err != nil {
							return err
						}
					} else {
						if err := p.parseURL(string(attrVal), uriHandler); err != nil {
							return err
						}
					}
				}
			}
		case xml.TextToken:
			if tag == svg.Style {
				if err := p.parseCSS(buffer.NewReader(data), uriHandler, false); err != nil {
					return err
				}
			}
		}
		lexer.Free(len(data))
	}
}

func (p *Parser) parseURL(rawResURL string, uriHandler URIHandler) error {
	resURL, err := url.Parse(rawResURL)
	if err != nil {
		return err
	}

	if resURL.Host != "" && resURL.Host != p.reqURL.Host {
		return nil
	}

	resolvedURI := p.reqURL.ResolveReference(resURL)
	if strings.HasPrefix(resolvedURI.Path, p.baseURI) {
		uri := resolvedURI.Path
		uriHandler.URI(uri)
		if p.IsRecursive() {
			// recursively read and parse the referenced URIs
			// does not block the current parser
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()

				r, mimetype, err := p.opener.Open(uri)
				if err != nil {
					return
				}

				childParser, err := NewParser(p.Lookup, uri)
				if err != nil {
					return
				}

				if err := childParser.Parse(r, mimetype, uriHandler); err != nil {
					return
				}
			}()
		}
	}
	return nil
}
