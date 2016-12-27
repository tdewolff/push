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

////////////////

// Parser parses resources and calls uriHandler for all found URIs.
type Parser struct {
	baseURL    *url.URL
	uriHandler URIHandler

	// recursive
	opener FileOpener
	wg     sync.WaitGroup
}

// NewParser returns a new Parser. rawBaseURL defines the prefix an URL must have to be considered a local resource. If FileOpener is not nil, it will read and parse the referenced URIs recursively.
func NewParser(rawBaseURL string, opener FileOpener, uriHandler URIHandler) (*Parser, error) {
	if !strings.Contains(rawBaseURL, "//") && rawBaseURL != "" && rawBaseURL[0] != '/' {
		rawBaseURL = "//" + rawBaseURL
	}
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil {
		return nil, err
	}
	return &Parser{baseURL, uriHandler, opener, sync.WaitGroup{}}, nil
}

// IsRecursive returns true when the URIs within documents are aso read and parsed.
func (p *Parser) IsRecursive() bool {
	return p.opener != nil
}

// Parse parses r with mimetype and served by uri. When Parser is recursive, it will be blocking until all resources are parsed.
func (p *Parser) Parse(r io.Reader, mimetype, uri string) error {
	if p.IsRecursive() {
		defer p.wg.Wait()
	}
	return p.parse(r, mimetype, uri)
}

func (p *Parser) parse(r io.Reader, mimetype, uri string) error {
	reqURL, err := url.Parse(uri)
	if err != nil {
		return err
	}

	if mimetype == "text/html" {
		return p.parseHTML(r, reqURL)
	} else if mimetype == "text/css" {
		return p.parseCSS(r, reqURL, false)
	} else if mimetype == "image/svg+xml" {
		return p.parseSVG(r, reqURL)
	}
	return ErrNoParser
}

////////////////

func (p *Parser) parseHTML(r io.Reader, reqURL *url.URL) error {
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
						if err := p.parseCSS(buffer.NewReader(attrVal), reqURL, true); err != nil {
							return err
						}
					} else {
						if attr == html.Srcset {
							for _, uri := range parseSrcset(attrVal) {
								if err := p.parseURL(uri, reqURL); err != nil {
									return err
								}
							}
						} else {
							if err := p.parseURL(string(attrVal), reqURL); err != nil {
								return err
							}
						}
					}
				}
			}
		case html.SvgToken:
			if err := p.parseSVG(buffer.NewReader(data), reqURL); err != nil {
				return err
			}
		case html.TextToken:
			if tag == html.Style {
				if err := p.parseCSS(buffer.NewReader(data), reqURL, false); err != nil {
					return err
				}
			} else if tag == html.Iframe {
				if err := p.parseHTML(buffer.NewReader(data), reqURL); err != nil {
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

func (p *Parser) parseCSS(r io.Reader, reqURL *url.URL, isInline bool) error {
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
						if err := p.parseURL(string(url), reqURL); err != nil {
							return err
						}
					}
				}
			}
		}
	}
}

func (p *Parser) parseSVG(r io.Reader, reqURL *url.URL) error {
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
						if err := p.parseCSS(buffer.NewReader(attrVal), reqURL, true); err != nil {
							return err
						}
					} else {
						if err := p.parseURL(string(attrVal), reqURL); err != nil {
							return err
						}
					}
				}
			}
		case xml.TextToken:
			if tag == svg.Style {
				if err := p.parseCSS(buffer.NewReader(data), reqURL, false); err != nil {
					return err
				}
			}
		}
		lexer.Free(len(data))
	}
}

func (p *Parser) parseURL(rawResURL string, reqURL *url.URL) error {
	resURL, err := url.Parse(rawResURL)
	if err != nil {
		return err
	}

	if resURL.Host != "" && p.baseURL.Host != "" && resURL.Host != p.baseURL.Host {
		return nil
	}

	resolvedURI := reqURL.ResolveReference(resURL)
	if strings.HasPrefix(resolvedURI.Path, p.baseURL.Path) {
		uri := resolvedURI.Path
		if p.IsRecursive() {
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()

				r, mimetype, err := p.opener.Open(uri)
				if err != nil {
					return
				}

				if err := p.parse(r, mimetype, uri); err != nil {
					return
				}
			}()
		}
		if err = p.uriHandler.URI(uri); err != nil {
			return err
		}
	}
	return nil
}
