package main

import (
	"log"
	"net/http"
	"time"

	"github.com/pkg/profile"
	"github.com/tdewolff/push"
)

func main() {
	defer profile.Start(profile.CPUProfile, profile.ProfilePath(".")).Stop()

	lookup := push.NewLookup("localhost", "/")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)

		if pushWriter, err := push.ResponseWriter(w, r, lookup, nil); err == nil {
			defer func() {
				if err := pushWriter.Close(); err != nil {
					log.Println(err, r.RequestURI)
				}
			}()
			w = pushWriter
		} else if err != push.ErrRecursivePush && err != push.ErrNoPusher {
			log.Println(err, r.RequestURI)
		}

		http.ServeFile(w, r, "www"+r.URL.Path)
	})

	go func() {
		log.Fatal(http.ListenAndServe(":80", nil))
	}()
	log.Fatal(http.ListenAndServeTLS(":443", "localhost.crt", "localhost.key", nil))
}
