// echo-logger is a generic Trigger subscriber: logs whatever CloudEvent
// it receives and returns 200. The same image backs multiple Knative
// Services (push-handler, pr-handler, ...) -- what each one actually
// receives is determined by its Trigger's ce-type filter, not by
// anything in this binary.
package main

import (
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	name := os.Getenv("SUBSCRIBER_NAME")
	if name == "" {
		name = "echo-logger"
	}
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		log.Printf("[%s] received ce-type=%s ce-id=%s ce-source=%s body=%s",
			name, r.Header.Get("Ce-Type"), r.Header.Get("Ce-Id"), r.Header.Get("Ce-Source"), string(body))
		w.WriteHeader(http.StatusOK)
	})
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("%s listening on %s", name, addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
