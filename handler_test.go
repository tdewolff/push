package push

import (
	"bytes"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/tdewolff/test"
)

func TestListHandler(t *testing.T) {
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

	listHandler := NewListHandler()
	parser, err := NewParser("example.com/", nil, listHandler)
	test.Error(t, err, nil)

	err = parser.Parse(r, "text/html", "/request")
	test.Error(t, err, nil)

	sort.Strings(listHandler.URIs)
	test.String(t, strings.Join(listHandler.URIs, ","), "/frame.html,/image.svg,/style.css")
}

func TestRecursiveHandler(t *testing.T) {
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

	fileOpener := FileOpenerFunc(func(uri string) (io.Reader, string, error) {
		res := resources[uri]
		return bytes.NewBufferString(res.content), res.mimetype, nil
	})
	listHandler := NewListHandler()
	parser, err := NewParser("example.com/", fileOpener, listHandler)
	test.Error(t, err, nil)

	err = parser.Parse(r, "text/html", "/request")
	test.Error(t, err, nil)

	sort.Strings(listHandler.URIs)
	test.String(t, strings.Join(listHandler.URIs, ","), "/background.jpg,/frame.html,/header.jpg,/image.svg,/img1.jpg,/img2.jpg,/style.css")
}

type TestPusher struct {
	*ListHandler
}

func (p *TestPusher) Push(uri string, _ *http.PushOptions) error {
	return p.URI(uri)
}

func TestPushHandler(t *testing.T) {
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

	testPusher := &TestPusher{NewListHandler()}
	pushHandler := NewPushHandler(testPusher, nil)
	parser, err := NewParser("example.com/", nil, pushHandler)
	test.Error(t, err, nil)

	err = parser.Parse(r, "text/html", "/request")
	test.Error(t, err, nil)

	sort.Strings(testPusher.URIs)
	test.String(t, strings.Join(testPusher.URIs, ","), "/frame.html,/image.svg,/style.css")
}
