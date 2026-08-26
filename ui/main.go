// A small dashboard for knative-ctl: trigger canary/bluegreen/rollback
// releases and watch pods scale up/down live, both over Server-Sent
// Events. Shells out to kubectl (pod polling) and the knative-ctl binary
// itself (releases) rather than re-implementing either -- same approach
// main.go/release.go already use, and keeps release logic in one place.
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
)

//go:embed static
var staticFS embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "listen address (defaults to localhost-only)")
	kctlPath := flag.String("knative-ctl", "./knative-ctl", "path to the knative-ctl binary")
	flag.Parse()

	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticSub)))
	mux.HandleFunc("/api/pods/stream", podsStreamHandler)
	mux.HandleFunc("/api/release/stream", releaseStreamHandler(*kctlPath))

	log.Printf("knative-ctl dashboard on http://%s (knative-ctl: %s)", *addr, *kctlPath)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
