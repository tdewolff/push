package push

import "io"

// ExtToMimetype allows customization of the extension -> mimetype mapping.
var ExtToMimetype = map[string]string{
	"":      "text/html",
	".html": "text/html",
	".css":  "text/css",
	".svg":  "image/svg+xml",
}

// Reader parses r recursively and returns a reader that will send local resource URIs over uriChan. Any reads done at the returned reader will concurrently be parsed. Whether a resource is local is determined by host + uri.
func Reader(parser *Parser, r io.Reader, mimetype string, uriHandler URIHandler) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		r = io.TeeReader(r, pw)
		if err := parser.Parse(r, mimetype, uriHandler); err != nil {
			pw.CloseWithError(err)
		} else {
			pw.Close()
		}
	}()
	return pr
}

type listSimple struct {
	uris []string
}

func (l *listSimple) URI(uri string) error {
	l.uris = append(l.uris, uri)
	return nil
}

type listRecursive struct {
	uriChan chan string
}

func (l *listRecursive) URI(uri string) error {
	l.uriChan <- uri
	return nil
}

// List parses r recursively and returns a list of local resource URIs. Whether a resource is local is determined by host + uri.
func List(parser *Parser, r io.Reader, mimetype string) ([]string, error) {
	if parser.IsRecursive() {
		uriHandler := &listRecursive{make(chan string, 5)}

		var err error
		go func() {
			err = parser.Parse(r, mimetype, uriHandler)
			close(uriHandler.uriChan)
		}()

		uris := []string{}
		for uri := range uriHandler.uriChan {
			uris = append(uris, uri)
		}
		return uris, err
	} else {
		uriHandler := &listSimple{[]string{}}
		if err := parser.Parse(r, mimetype, uriHandler); err != nil {
			return uriHandler.uris, err
		}
		return uriHandler.uris, nil
	}
}
