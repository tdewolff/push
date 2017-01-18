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

	parser, err := NewParser("example.com/", nil, URIHandlerFunc(func(uri string) error {
		test.String(t, uri, "/res")
		return nil
	}))
	test.Error(t, err, nil)

	r = Reader(r, parser, "text/html", "/request")
	_, err = io.Copy(ioutil.Discard, r)
	test.Error(t, err, nil)
}

func TestList(t *testing.T) {
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

	uris, err := List("example.com/", nil, r, "text/html", "/request")
	test.Error(t, err, nil)

	sort.Strings(uris)
	test.String(t, strings.Join(uris, ","), "/frame.html,/image.svg,/style.css")
}
