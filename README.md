#<a name="push"></a> Push [![Build Status](https://travis-ci.org/tdewolff/push.svg?branch=master)](https://travis-ci.org/tdewolff/push) [![GoDoc](http://godoc.org/github.com/tdewolff/push?status.svg)](http://godoc.org/github.com/tdewolff/push) [![Coverage Status](https://coveralls.io/repos/github/tdewolff/push/badge.svg?branch=master)](https://coveralls.io/github/tdewolff/push?branch=master)

Push is a package that uses HTTP2 to push resources to the client as it parses content. By parsing HTML, CSS and SVG it extracts referenced resource URIs and pushes them towards the client, which is quicker than waiting for the client to parse and request those resources.

## Installation
You need Go1.8 (from tip for example).

Run the following command

	go get github.com/tdewolff/push

or add the following import and run the project with `go get`
``` go
import (
	"github.com/tdewolff/push"
)
```

## Parsers
### HTML
Parses
- `<style>...</style>` as CSS
- `<x style="...">` as inline CSS
- `<iframe>...</iframe>` as HTML
- `<svg>...</svg>` as SVG

Extracts URIs from
- `<link href="...">`
- `<script src="...">`
- `<img src="...">`
- `<img srcset="..., ...">`
- `<object data="...">`
- `<source src="...">`
- `<audio src="...">`
- `<video src="...">`
- `<track src="...">`
- `<embed src="...">`
- `<input src="...">`
- `<iframe src="...">`

### CSS
Parses
- ~~`url(data:image/svg+xml,...)` as SVG~~ data URI SVGs are not allowed to load external resources

Extracts URIs from
- `url("...")`

### SVG
Parses
- `<style>...</style>` as CSS
- `<x style="...">` as inline CSS

Extracts URIs from
- `<script href="..." xlink:href="...">`
- `<image href="..." xlink:href="...">`
- `<feImage href="..." xlink:href="...">`
- `<color-profile href="..." xlink:href="...">`
- `<use href="..." xlink:href="...">`

## Usage
You can use `NewLookup` (or `NewParser`) to parse a file as-is. Use `NewRecursiveLookup` (or `NewRecursiveParser`) to parse the file and also read and parse the content of all referenced URIs.

### Middleware
``` go
lookup := push.NewLookup("example.com", "/") // host and base URI

http.HandleFunc("/", push.Middleware(lookup, nil, func(w http.ResponseWriter, r *http.Request) {
	// ...
}))
```

### ResponseWriter
Wrap an existing `http.ResponseWriter` so that it pushes resources automatically:
``` go
lookup := push.NewLookup("example.com", "/") // host and base URI

http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
	if pushWriter, err := push.ResponseWriter(w, r, lookup, nil); err == nil {
		defer pushWriter.Close() // Close returns an error...
		w = pushWriter
	}

	// ...
})
```

### Reader
Wrap a reader and obtain the URIs from a channel:
``` go
var uriChan chan string

func openIndex() io.Reader {
	r, _ := os.Open("index.html")

	parser, err := push.NewRecursiveParser("example.com", "/", push.FileOpenerFunc(func(uri string) (io.Reader, string, error) {
		// open file for uri
		return r, mimetype, nil
	}), "/index.html")
	if err != nil {
		panic(err)
	}

	return p.Reader(r, "localhost", "/index.html", push.URIHandlerFunc(func(uri string) error {
		// is called asynchronously when using a recursive parser
		fmt.Println(uri)
		return nil
	}))
}
```

### List
List the resource URIs found:
``` go
r, _ := os.Open("index.html")

parser, err := push.NewParser("example.com", "/", "/index.html")
if err != nil {
	panic(err)
}

uris, err := push.List(parser, r, "text/html")
if err != nil {
	panic(err)
}
```

### Push
`Push` pushes resources to `pusher`. It is the underlying functionality of `ResponseWriter`.
``` go
httpPusher, ok := w.(http.Pusher)
if ok {
	pusher := NewPusher(httpPusher, &http.PushOptions{"", http.Header{}})

	parser, err := push.NewParser("example.com", "/", "index.html")
	if err != nil {
		panic(err)
	}

	tr := io.TeeReader(r, w)
	err := parser.Parse(r, "text/html", pusher)
}
```

## Example
See [example](https://github.com/tdewolff/push/tree/master/example), it shows how a webserver with artificial 50ms delay per request can have the page load time reduced from 1.6s (http) to 0.4s (https).

## License
Released under the [MIT license](LICENSE.md).

[1]: http://golang.org/ "Go Language"
