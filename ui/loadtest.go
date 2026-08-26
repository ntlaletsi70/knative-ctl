package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// loadtestStreamHandler fires concurrent requests at a Knative Service
// through kourier-internal, the same path demo/1-traffic-spike/run-demo.sh
// exercises with a shell loop of curl workers -- done natively here with
// goroutines instead of shelling out, since this is already a Go HTTP
// server. Only reaches anything when knative-ui itself runs in-cluster
// (ui/service.yaml): kourier-internal.kourier-system.svc.cluster.local
// isn't reachable from outside the cluster, so this errors cleanly, not
// silently, when run via the local `./knative-ui` binary.
func loadtestStreamHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	service := q.Get("service")
	namespace := q.Get("namespace")
	if namespace == "" {
		namespace = "knative-demo"
	}
	if service == "" {
		http.Error(w, "service is required", http.StatusBadRequest)
		return
	}

	workers := 15
	if v := q.Get("workers"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 50 {
			http.Error(w, "workers must be 1-50", http.StatusBadRequest)
			return
		}
		workers = n
	}
	duration := 25 * time.Second
	if v := q.Get("duration"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 120 {
			http.Error(w, "duration must be 1-120 seconds", http.StatusBadRequest)
			return
		}
		duration = time.Duration(n) * time.Second
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	host := fmt.Sprintf("%s.%s.svc.cluster.local", service, namespace)
	target := "http://kourier-internal.kourier-system.svc.cluster.local/"

	fmt.Fprintf(w, "data: firing %d workers at %s for %s\n\n", workers, host, duration)
	flusher.Flush()

	ctx, cancel := context.WithTimeout(r.Context(), duration)
	defer cancel()

	var total, errs int64
	client := &http.Client{Timeout: 5 * time.Second}

	for i := 0; i < workers; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
				if err != nil {
					return
				}
				req.Host = host
				resp, err := client.Do(req)
				if err != nil {
					atomic.AddInt64(&errs, 1)
					continue
				}
				resp.Body.Close()
				atomic.AddInt64(&total, 1)
				if resp.StatusCode >= 400 {
					atomic.AddInt64(&errs, 1)
				}
			}
		}()
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	start := time.Now()

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(w, "data: t+%ds: %d requests sent, %d errors\n\n",
				int(time.Since(start).Seconds()), atomic.LoadInt64(&total), atomic.LoadInt64(&errs))
			fmt.Fprintf(w, "event: done\ndata: %d total, %d errors\n\n",
				atomic.LoadInt64(&total), atomic.LoadInt64(&errs))
			flusher.Flush()
			return
		case <-ticker.C:
			fmt.Fprintf(w, "data: t+%ds: %d requests sent, %d errors\n\n",
				int(time.Since(start).Seconds()), atomic.LoadInt64(&total), atomic.LoadInt64(&errs))
			flusher.Flush()
		}
	}
}
