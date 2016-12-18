#<a name="push"></a> Push [![GoDoc](http://godoc.org/github.com/tdewolff/push?status.svg)](http://godoc.org/github.com/tdewolff/push)

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
### New
First argument is the base URI for which to serve pushes. The second argument is the resource directory to scan resources recursively, leave empty to disable.
``` go
p := push.NewParser("/", "static/")
```

### ResponseWriter
Wrap an existing `http.ResponseWriter` so that it pushes resources automatically:
``` go
http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
	if pushWriter, err := p.ResponseWriter(w, r); err == nil {
		defer pushWriter.Close() // Close returns an error...
		w = pushWriter
	}

	// ...
}
```

### Push
`Push` pushes resources to `pusher`. It is the underlying functionality of ResponseWriter.
``` go
err := p.Push(r, "localhost", "/index.html", pusher, pushOpts)
```

### Reader
Wrap a reader and obtain the URIs from a channel:
``` go
var uriChan chan string

func openIndex() io.Reader {
	r, _ := os.Open("index.html")

	return p.Reader(r, "localhost", "/index.html", uriChan)
}

// somewhere else...
go func() {
	for uri := range uriChan {
		// process uri
	}
}()

// don't forget to close uriChan somewhere
```

### List
`List` the resource URIs found:
``` go
uris, err := p.List(r, "localhost", "/index.html")
if err != nil {
	panic(err)
}
```

### Parse
`ParseAll` and `Parse` are low-level function to parse files and return the resource URIs. `ParseAll` parses recursively (if `Parser.dir` is not empty) and `Parse` parses the reader and uses a callback to return URIs:
``` go
err := p.ParseAll(r, "localhost", "/index.html", uriChan)

err := p.Parse(r, "localhost", "/index.html", func(uri string) error {
	// ...
})
```

## Example
See [example](https://github.com/tdewolff/push/tree/master/example), it shows how a 1.6s (http) can be reduced to 0.4s (https).

## License
Released under the [MIT license](LICENSE.md).

[1]: http://golang.org/ "Go Language"
