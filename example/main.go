package main

import (
	"log"
	"net/http"
	"path"
	"time"

	"github.com/pkg/profile"
	"github.com/tdewolff/push"
)

func main() {
	defer profile.Start(profile.CPUProfile, profile.ProfilePath(".")).Stop()

	fileOpener := push.NewDefaultFileOpener("www")
	cache := push.NewDefaultCache()

	http.Handle("/", push.Middleware("example.com/", fileOpener, cache, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		http.ServeFile(w, r, path.Join("www", r.URL.Path))
	})))

	// http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
	// 	time.Sleep(50 * time.Millisecond)

	// 	if pushWriter, err := push.ResponseWriter(w, r, "example.com/", fileOpener, cache); err == nil {
	// 		defer func() {
	// 			if err := pushWriter.Close(); err != nil {
	// 				log.Println(err, r.RequestURI)
	// 			}
	// 		}()
	// 		w = pushWriter
	// 	} else if err != push.ErrNoParser && err != push.ErrRecursivePush {
	// 		log.Println(err, r.RequestURI)
	// 	}

	// 	http.ServeFile(w, r, path.Join("www", r.URL.Path))
	// })

	go func() {
		log.Fatal(http.ListenAndServe(":80", nil))
	}()
	log.Fatal(http.ListenAndServeTLS(":443", "localhost.crt", "localhost.key", nil))
}
