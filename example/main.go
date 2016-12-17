package main

import (
	"log"
	"net/http"
	"time"

	"github.com/tdewolff/push"
)

func main() {
	//defer profile.Start(profile.CPUProfile, profile.ProfilePath(".")).Stop()

	pusher := push.New("/", "www")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)

		if pushWriter, err := pusher.ResponseWriter(w, r); err == nil {
			defer func() {
				if err := pushWriter.Close(); err != nil {
					log.Print(err)
				}
			}()
			w = pushWriter
		} else if err != push.ErrRecursivePush && err != push.ErrNoPusher {
			log.Print(err)
		}

		http.ServeFile(w, r, "www"+r.URL.Path)
	})

	go func() {
		log.Fatal(http.ListenAndServe(":80", nil))
	}()
	log.Fatal(http.ListenAndServeTLS(":443", "localhost.crt", "localhost.key", nil))
}
