package main

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"os/exec"
)

// releaseStreamHandler shells out to the knative-ctl binary itself for
// canary/bluegreen/rollback, rather than re-implementing the traffic-patch
// logic here -- one place for release behavior, this dashboard is just a
// trigger + live view on top of it.
func releaseStreamHandler(kctlPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		args, err := releaseArgs(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		fmt.Fprintf(w, "data: $ knative-ctl %s\n\n", joinArgs(args))
		flusher.Flush()

		cmd := exec.CommandContext(r.Context(), kctlPath, args...)
		pr, pw, err := os.Pipe()
		if err != nil {
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
			flusher.Flush()
			return
		}
		cmd.Stdout = pw
		cmd.Stderr = pw

		if err := cmd.Start(); err != nil {
			pw.Close()
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
			flusher.Flush()
			return
		}

		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
			pw.Close()
		}()

		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			fmt.Fprintf(w, "data: %s\n\n", scanner.Text())
			flusher.Flush()
		}

		status := "ok"
		if err := <-done; err != nil {
			status = "error: " + err.Error()
		}
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", status)
		flusher.Flush()
	}
}

// releaseArgs validates the request and builds the exact argv passed to
// exec.Command -- never through a shell, so there's no injection surface
// regardless of what a client sends as service/image/etc.
func releaseArgs(r *http.Request) ([]string, error) {
	q := r.URL.Query()
	action := q.Get("action")
	service := q.Get("service")
	namespace := q.Get("namespace")
	if namespace == "" {
		namespace = "knative-demo"
	}
	if service == "" {
		return nil, fmt.Errorf("service is required")
	}

	switch action {
	case "canary":
		image := q.Get("image")
		if image == "" {
			return nil, fmt.Errorf("image is required")
		}
		args := []string{"canary", service, image, "--namespace", namespace}
		if steps := q.Get("steps"); steps != "" {
			args = append(args, "--steps", steps)
		}
		if interval := q.Get("interval"); interval != "" {
			args = append(args, "--interval", interval)
		}
		return args, nil
	case "bluegreen":
		image := q.Get("image")
		if image == "" {
			return nil, fmt.Errorf("image is required")
		}
		return []string{"bluegreen", service, image, "--namespace", namespace}, nil
	case "rollback":
		revision := q.Get("revision")
		if revision == "" {
			return nil, fmt.Errorf("revision is required")
		}
		return []string{"rollback", service, revision, "--namespace", namespace}, nil
	default:
		return nil, fmt.Errorf("unknown action %q (want canary|bluegreen|rollback)", action)
	}
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
