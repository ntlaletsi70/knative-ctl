// github-receiver accepts a raw GitHub webhook POST and forwards it into a
// Knative Broker as a CloudEvent (binary HTTP content mode: ce-* headers +
// the original JSON body untouched). Knative's GitHubSource extension
// would do this too, but that's a whole extra controller to install on a
// cluster that's already tight on CPU -- this is ~100 lines instead.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	addr := getenv("ADDR", ":8080")
	brokerURL := getenv("BROKER_URL", "http://broker-ingress.knative-eventing.svc.cluster.local/knative-demo/default")
	secret := os.Getenv("GITHUB_WEBHOOK_SECRET")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handleWebhook(w, r, brokerURL, secret)
	})
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("github-receiver listening on %s, forwarding to %s", addr, brokerURL)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func handleWebhook(w http.ResponseWriter, r *http.Request, brokerURL, secret string) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "reading body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if secret != "" {
		if !validSignature(body, r.Header.Get("X-Hub-Signature-256"), secret) {
			log.Printf("rejected: invalid signature")
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	event := r.Header.Get("X-GitHub-Event")
	if event == "" {
		event = "unknown"
	}
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	if deliveryID == "" {
		deliveryID = strconv.FormatInt(time.Now().UnixNano(), 10)
	}

	req, err := http.NewRequest(http.MethodPost, brokerURL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "building broker request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Ce-Specversion", "1.0")
	req.Header.Set("Ce-Id", deliveryID)
	req.Header.Set("Ce-Source", "github-webhook-receiver")
	req.Header.Set("Ce-Type", "dev.github."+event)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("forwarding to broker failed: %v", err)
		http.Error(w, "forwarding to broker: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	log.Printf("event=%s delivery=%s -> broker status=%d", event, deliveryID, resp.StatusCode)
	w.WriteHeader(resp.StatusCode)
	fmt.Fprintf(w, "forwarded dev.github.%s (delivery %s) to broker: %d\n", event, deliveryID, resp.StatusCode)
}

func validSignature(body []byte, sigHeader, secret string) bool {
	const prefix = "sha256="
	if len(sigHeader) <= len(prefix) || sigHeader[:len(prefix)] != prefix {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sigHeader[len(prefix):]), []byte(expected))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
