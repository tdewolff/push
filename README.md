#<a name="push"></a> Push [![GoDoc](http://godoc.org/github.com/tdewolff/push?status.svg)](http://godoc.org/github.com/tdewolff/push)

Push is a package that uses HTTP2 to push resources to the client as it parses content. By parsing HTML and CSS it extracts referenced resource URIs and pushes them towards the client, which is quicker than waiting for the client to parse and request those resources.

## Installation
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
``` go
pusher := push.New()
```

### ResponseWriter
``` go
http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
	if pushWriter, err := pusher.ResponseWriter(w, r); err == nil {
		defer pushWriter.Close()
		w = pushWriter
	}

	// ...
}
```

### Reader
``` go
func openIndex() io.Reader {
	r, _ := os.Open("index.html")

	r = pusher.Reader(r, "/index.html", "text/html", func(uri string) error {
		fmt.Println(uri)
		return nil
	})
	return r
}
```

## Example
See [example](https://github.com/tdewolff/push/tree/master/example), it shows how a 1.6s (http) can be reduced to 0.4s (https).

## License
Released under the [MIT license](LICENSE.md).

[1]: http://golang.org/ "Go Language"
