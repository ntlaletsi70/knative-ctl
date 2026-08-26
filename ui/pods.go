package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"time"
)

type podSnapshot struct {
	Name     string `json:"name"`
	Service  string `json:"service"`
	Revision string `json:"revision"`
	Phase    string `json:"phase"`
	Ready    string `json:"ready"`
	Restarts int    `json:"restarts"`
	AgeSecs  int64  `json:"ageSeconds"`
}

// Only the fields we need from `kubectl get pods -o json` -- encoding/json
// ignores the rest, so this stays a plain struct rather than pulling in
// k8s.io/api just for type definitions (same "shell out, don't link
// against client-go" approach as the rest of this repo).
type kubectlPodList struct {
	Items []struct {
		Metadata struct {
			Name              string            `json:"name"`
			Labels            map[string]string `json:"labels"`
			CreationTimestamp time.Time         `json:"creationTimestamp"`
		} `json:"metadata"`
		Status struct {
			Phase             string `json:"phase"`
			ContainerStatuses []struct {
				Ready        bool `json:"ready"`
				RestartCount int  `json:"restartCount"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

func fetchPods(namespace string) ([]podSnapshot, error) {
	out, err := exec.Command("kubectl", "get", "pods", "-n", namespace, "-o", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl get pods -n %s: %w", namespace, err)
	}
	var list kubectlPodList
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, err
	}
	snaps := make([]podSnapshot, 0, len(list.Items))
	for _, it := range list.Items {
		ready, restarts := 0, 0
		for _, cs := range it.Status.ContainerStatuses {
			if cs.Ready {
				ready++
			}
			restarts += cs.RestartCount
		}
		snaps = append(snaps, podSnapshot{
			Name:     it.Metadata.Name,
			Service:  it.Metadata.Labels["serving.knative.dev/service"],
			Revision: it.Metadata.Labels["serving.knative.dev/revision"],
			Phase:    it.Status.Phase,
			Ready:    fmt.Sprintf("%d/%d", ready, len(it.Status.ContainerStatuses)),
			Restarts: restarts,
			AgeSecs:  int64(time.Since(it.Metadata.CreationTimestamp).Seconds()),
		})
	}
	return snaps, nil
}

// podsStreamHandler pushes a full pod-list snapshot every second. Polling
// rather than `kubectl get -w` -- watch mode's JSON output isn't
// line-delimited, and Knative's cold starts/scale-downs are seconds-scale,
// well within a 1s poll's resolution.
func podsStreamHandler(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "knative-demo"
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		snaps, err := fetchPods(namespace)
		if err != nil {
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		} else {
			b, _ := json.Marshal(snaps)
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
