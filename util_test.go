package push

import (
	"bytes"
	"io"
	"io/ioutil"
	"sort"
	"strings"
	"testing"

	"github.com/tdewolff/test"
)

func TestReader(t *testing.T) {
	var r io.Reader
	r = bytes.NewBufferString(`<img src="/res">`)

	lookup := NewLookup("example.com", "/")
	parser, _ := NewParser(lookup, "/request")
	r = Reader(parser, r, "text/html", URIHandlerFunc(func(uri string) error {
		test.String(t, uri, "/res")
		return nil
	}))

	io.Copy(ioutil.Discard, r)
}

func TestListSimple(t *testing.T) {
	r := bytes.NewBufferString(`
	<html>
		<head>
			<link rel="stylesheet" href="/style.css">
		</head>
		<body>
			<img src="/image.svg">
			<iframe src="/frame.html"></iframe>
		</body>
	</html>`)

	lookup := NewLookup("example.com", "/")
	parser, _ := NewParser(lookup, "/request")

	uris, _ := List(parser, r, "text/html")
	sort.Strings(uris)
	test.String(t, strings.Join(uris, ","), "/frame.html,/image.svg,/style.css")
}

func TestListRecursive(t *testing.T) {
	r := bytes.NewBufferString(`
	<html>
		<head>
			<link rel="stylesheet" href="/style.css">
		</head>
		<body>
			<img src="/image.svg">
			<iframe src="/frame.html"></iframe>
		</body>
	</html>`)

	resources := map[string]struct {
		mimetype string
		content  string
	}{
		"/frame.html": {"text/html", `<img src="/header.jpg">`},
		"/style.css":  {"text/css", `a { background-image: url("/background.jpg"); }`},
		"/image.svg":  {"image/svg+xml", `<image href="/img1.jpg" xlink:href="/img2.jpg"></image>`},
	}
	lookup := NewRecursiveLookup("example.com", "/", FileOpenerFunc(func(uri string) (io.Reader, string, error) {
		res := resources[uri]
		return bytes.NewBufferString(res.content), res.mimetype, nil
	}))
	parser, _ := NewParser(lookup, "/request")

	uris, _ := List(parser, r, "text/html")
	sort.Strings(uris)
	test.String(t, strings.Join(uris, ","), "/background.jpg,/frame.html,/header.jpg,/image.svg,/img1.jpg,/img2.jpg,/style.css")
}
