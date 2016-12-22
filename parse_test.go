package push

import (
	"bytes"
	"testing"

	"github.com/tdewolff/test"
)

func TestURLParser(t *testing.T) {
	urlParserTests := []struct {
		host     string
		baseURI  string
		uri      string
		input    string
		expected string
	}{
		{"example.com", "/", "/index.html", "http://example.com/header.jpg", "/header.jpg"},
		{"example.com", "/", "/index.html", "//example.com/header.jpg", "/header.jpg"},
		{"example.com", "/", "/index.html", "/header.jpg", "/header.jpg"},
		{"example.com", "/", "/index.html", "header.jpg", "/header.jpg"},
		{"www.example.com", "/", "/index.html", "http://example.com/header.jpg", ""},
		{"example.com", "/dir/", "/index.html", "http://example.com/header.jpg", ""},
		{"example.com", "/", "/dir/index.html", "http://example.com/header.jpg", "/header.jpg"},
		{"example.com", "/", "/dir/index.html", "header.jpg", "/dir/header.jpg"},
	}

	for _, tt := range urlParserTests {
		uri := ""
		parser, _ := NewParser(tt.host, tt.baseURI, tt.uri)
		parser.parseURL(tt.input, URIHandlerFunc(func(_uri string) error {
			uri = _uri
			return nil
		}))
		test.String(t, uri, tt.expected, tt.host, tt.baseURI, tt.uri)
	}
}

func TestParsers(t *testing.T) {
	parserTests := []struct {
		mimetype string
		input    string
	}{
		{"text/html", `<img src="/res">`},
		{"text/html", `<link href="/res">`},
		{"text/html", `<script src="/res"></script>`},
		{"text/html", `<img srcset=" /res , /res ">`},
		{"text/html", `<object data="/res">`},
		{"text/html", `<source src="/res">`},
		{"text/html", `<audio src="/res">`},
		{"text/html", `<video src="/res">`},
		{"text/html", `<track src="/res">`},
		{"text/html", `<embed src="/res">`},
		{"text/html", `<input src="/res">`},
		{"text/html", `<iframe src="/res"></iframe>`},

		{"text/css", `a { background-image: url("/res"); }`},

		{"image/svg+xml", `<image href="/res" xlink:href="/res"></image>`},
		{"image/svg+xml", `<script href="/res" xlink:href="/res"></script>`},
		{"image/svg+xml", `<feImage href="/res" xlink:href="/res"></feImage>`},
		{"image/svg+xml", `<color-profile href="/res" xlink:href="/res"></color-profile>`},
		{"image/svg+xml", `<use href="/res" xlink:href="/res"></use>`},

		// recursive
		{"text/html", `<style>a { background-image: url("/res"); }</style>`},
		{"text/html", `<x style="background-image: url('/res');">`},
		{"text/html", `<iframe><img src="/res"></iframe>`},
		{"text/html", `<svg><image href="/res"></image></svg>`},

		{"image/svg+xml", `<style>a { background-image: url("/res"); }</style>`},
		{"image/svg+xml", `<x style="background-image: url('/res');"></x>`},
	}

	for _, tt := range parserTests {
		r := bytes.NewBufferString(tt.input)

		parser, _ := NewParser("example.com", "/", "/request")
		parser.Parse(r, tt.mimetype, URIHandlerFunc(func(uri string) error {
			test.String(t, uri, "/res")
			return nil
		}))
	}
}
