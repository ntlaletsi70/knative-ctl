package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// How far back each snapshot looks, and how many traces it asks Zipkin
// for. Under a load test there are far more than traceLimit traces in
// the window, so what comes back is a sample -- which is why the
// frontend shows each revision's share of traffic, not a request rate.
const (
	traceWindow = 30 * time.Second
	traceLimit  = 500
	traceRows   = 25
)

// Only the fields we read from Zipkin's v2 span JSON.
type zipkinSpan struct {
	TraceID       string `json:"traceId"`
	ID            string `json:"id"`
	ParentID      string `json:"parentId"`
	Name          string `json:"name"`
	Timestamp     int64  `json:"timestamp"` // microseconds since epoch
	Duration      int64  `json:"duration"`  // microseconds
	LocalEndpoint struct {
		ServiceName string `json:"serviceName"`
	} `json:"localEndpoint"`
	Tags map[string]string `json:"tags"`
}

type traceSummary struct {
	ID         string   `json:"id"`
	Timestamp  int64    `json:"timestamp"` // milliseconds since epoch
	DurationMs float64  `json:"durationMs"`
	Spans      int      `json:"spans"`
	Request    string   `json:"request"`
	Status     string   `json:"status"`
	Hops       []string `json:"hops"`
	Event      string   `json:"event,omitempty"`
	// How long the activator held the request waiting for a pod: the
	// visible cost of a scale-from-zero.
	ColdStartMs float64 `json:"coldStartMs,omitempty"`
	Error       bool    `json:"error"`
}

type revisionStats struct {
	Service  string  `json:"service"`
	Revision string  `json:"revision"`
	Count    int     `json:"count"`
	Share    float64 `json:"share"` // of this service's requests in the window
	AvgMs    float64 `json:"avgMs"`
	MaxMs    float64 `json:"maxMs"`
	Errors   int     `json:"errors"`
}

type trafficSnapshot struct {
	WindowSeconds int             `json:"windowSeconds"`
	Sampled       bool            `json:"sampled"`
	Total         int             `json:"total"`
	Revisions     []revisionStats `json:"revisions"`
	Traces        []traceSummary  `json:"traces"`
}

func fetchTraffic(zipkinURL string) (*trafficSnapshot, error) {
	q := url.Values{}
	q.Set("lookback", strconv.FormatInt(traceWindow.Milliseconds(), 10))
	q.Set("limit", strconv.Itoa(traceLimit))
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(zipkinURL + "/api/v2/traces?" + q.Encode())
	if err != nil {
		return nil, fmt.Errorf("zipkin unreachable at %s: %w", zipkinURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zipkin returned %s", resp.Status)
	}
	var traces [][]zipkinSpan
	if err := json.NewDecoder(resp.Body).Decode(&traces); err != nil {
		return nil, fmt.Errorf("decoding zipkin response: %w", err)
	}

	snap := &trafficSnapshot{
		WindowSeconds: int(traceWindow.Seconds()),
		Sampled:       len(traces) >= traceLimit,
		Revisions:     []revisionStats{},
		Traces:        []traceSummary{},
	}
	stats := map[string]*revisionStats{}
	perService := map[string]int{}

	for _, spans := range traces {
		// A trace that is one span from the gateway and nothing else is
		// one of Knative's own readiness probes, not a request.
		if len(spans) < 2 {
			continue
		}
		sum := summarize(spans)
		snap.Traces = append(snap.Traces, sum)
		snap.Total++

		// One queue-proxy server span per request a revision served.
		for _, s := range spans {
			rev := s.Tags["kn.revision.name"]
			if rev == "" || s.Tags["container.name"] != "queue-proxy" || s.Tags["http.response.status_code"] == "" || s.Tags["client.address"] == "" {
				continue
			}
			st := stats[rev]
			if st == nil {
				st = &revisionStats{Service: s.Tags["kn.service.name"], Revision: rev}
				stats[rev] = st
			}
			ms := float64(s.Duration) / 1000
			st.Count++
			st.AvgMs += ms
			if ms > st.MaxMs {
				st.MaxMs = ms
			}
			if isErrorStatus(s.Tags["http.response.status_code"]) {
				st.Errors++
			}
			perService[st.Service]++
		}
	}

	for _, st := range stats {
		st.AvgMs /= float64(st.Count)
		st.Share = float64(st.Count) / float64(perService[st.Service])
		snap.Revisions = append(snap.Revisions, *st)
	}
	sort.Slice(snap.Revisions, func(i, j int) bool {
		a, b := snap.Revisions[i], snap.Revisions[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return a.Revision < b.Revision
	})
	sort.Slice(snap.Traces, func(i, j int) bool { return snap.Traces[i].Timestamp > snap.Traces[j].Timestamp })
	if len(snap.Traces) > traceRows {
		snap.Traces = snap.Traces[:traceRows]
	}
	return snap, nil
}

func summarize(spans []zipkinSpan) traceSummary {
	sort.Slice(spans, func(i, j int) bool { return spans[i].Timestamp < spans[j].Timestamp })
	root := spans[0]
	for _, s := range spans {
		if s.ParentID == "" {
			root = s
			break
		}
	}

	sum := traceSummary{
		ID:         root.TraceID,
		Timestamp:  root.Timestamp / 1000,
		DurationMs: float64(root.Duration) / 1000,
		Spans:      len(spans),
		Status:     firstTag(root.Tags, "http.status_code", "http.response.status_code"),
		Request:    firstTag(root.Tags, "http.method", "http.request.method") + " " + firstTag(root.Tags, "http.url"),
		Hops:       []string{},
	}
	sum.Error = isErrorStatus(sum.Status)

	for _, s := range spans {
		// A hop is a revision where there is one, otherwise the
		// reporting component. Consecutive spans from the same hop
		// collapse into it; a hop that recurs later (the gateway and
		// activator do, once per Knative Service on the path) is kept.
		hop := s.Tags["kn.revision.name"]
		if hop == "" || s.Tags["container.name"] != "queue-proxy" {
			hop = s.LocalEndpoint.ServiceName
		}
		if n := len(sum.Hops); hop != "" && (n == 0 || sum.Hops[n-1] != hop) {
			sum.Hops = append(sum.Hops, hop)
		}
		if t := s.Tags["cloudevents.type"]; t != "" {
			sum.Event = t
		}
		if s.Name == "throttler_try" {
			if ms := float64(s.Duration) / 1000; ms > sum.ColdStartMs {
				sum.ColdStartMs = ms
			}
		}
		if isErrorStatus(firstTag(s.Tags, "http.response.status_code", "http.status_code")) {
			sum.Error = true
		}
	}
	// Anything under this is the activator's ordinary bookkeeping, not a
	// wait for a pod to come up.
	if sum.ColdStartMs < 100 {
		sum.ColdStartMs = 0
	}
	return sum
}

func firstTag(tags map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := tags[k]; v != "" {
			return v
		}
	}
	return ""
}

func isErrorStatus(code string) bool {
	n, err := strconv.Atoi(code)
	return err == nil && n >= 500
}

// trafficStreamHandler pushes a traffic snapshot every two seconds, same
// polling-over-SSE shape as podsStreamHandler.
func trafficStreamHandler(zipkinURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			snap, err := fetchTraffic(zipkinURL)
			if err != nil {
				fmt.Fprintf(w, "event: unavailable\ndata: %s\n\n", err.Error())
			} else {
				b, _ := json.Marshal(snap)
				fmt.Fprintf(w, "data: %s\n\n", b)
			}
			flusher.Flush()

			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
			}
		}
	}
}

// zipkinProxy serves Zipkin's own UI under /zipkin/ on the dashboard, so
// a trace row can link straight to its full waterfall without a second
// port-forward. No path rewriting needed: Zipkin already serves its UI,
// and the API calls that UI makes, under /zipkin/.
func zipkinProxy(zipkinURL string) (http.Handler, error) {
	target, err := url.Parse(zipkinURL)
	if err != nil {
		return nil, fmt.Errorf("parsing --zipkin %q: %w", zipkinURL, err)
	}
	return httputil.NewSingleHostReverseProxy(target), nil
}
