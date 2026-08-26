package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
)

type revisionInfo struct {
	Name    string `json:"name"`
	Image   string `json:"image"`
	Ready   bool   `json:"ready"`
	Created string `json:"created"`
}

type kubectlRevisionList struct {
	Items []struct {
		Metadata struct {
			Name              string `json:"name"`
			CreationTimestamp string `json:"creationTimestamp"`
		} `json:"metadata"`
		// Revision's spec embeds PodSpec fields (containers, etc.)
		// directly -- unlike Service/Configuration, there's no nested
		// spec.template.spec here.
		Spec struct {
			Containers []struct {
				Image string `json:"image"`
			} `json:"containers"`
		} `json:"spec"`
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

// revisionsHandler backs the image/revision datalists in the release
// form -- past revisions' images are exactly what's actually deployable
// (already built, already pulled once), more useful to pick from than
// typing a tag from memory.
func revisionsHandler(w http.ResponseWriter, r *http.Request) {
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

	out, err := exec.Command("kubectl", "get", "revision", "-n", namespace,
		"-l", "serving.knative.dev/service="+service, "-o", "json").Output()
	if err != nil {
		http.Error(w, fmt.Sprintf("kubectl get revision: %v", err), http.StatusInternalServerError)
		return
	}

	var list kubectlRevisionList
	if err := json.Unmarshal(out, &list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	revs := make([]revisionInfo, 0, len(list.Items))
	for _, it := range list.Items {
		ready := false
		for _, c := range it.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				ready = true
			}
		}
		image := ""
		if len(it.Spec.Containers) > 0 {
			image = it.Spec.Containers[0].Image
		}
		revs = append(revs, revisionInfo{
			Name:    it.Metadata.Name,
			Image:   image,
			Ready:   ready,
			Created: it.Metadata.CreationTimestamp,
		})
	}
	sort.Slice(revs, func(i, j int) bool { return revs[i].Created > revs[j].Created })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(revs)
}
