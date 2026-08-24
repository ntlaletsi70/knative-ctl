// Canary and blue-green release flows for a single Knative Service,
// built directly on ksvc.spec.traffic revision splitting -- no extra
// controller (Argo Rollouts/Flagger) needed.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type trafficTarget struct {
	RevisionName string `json:"revisionName,omitempty"`
	Percent      int    `json:"percent"`
	Tag          string `json:"tag,omitempty"`
}

// reorderArgs moves every --flag [value] pair to the front, leaving
// non-flag tokens as trailing positional args. The stdlib flag package
// stops parsing at the first non-flag token, which silently dropped every
// flag placed after "<service> <image>" -- exactly the order documented
// for canary/bluegreen/rollback (and the order deploy.yml invokes them
// in), so e.g. --namespace was always ignored in favor of its "default"
// zero value. None of this CLI's flags are booleans, so treating the
// token right after every "-"-prefixed arg as its value is safe.
func reorderArgs(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

func runCanary(args []string) error {
	fs := flag.NewFlagSet("canary", flag.ExitOnError)
	steps := fs.String("steps", "10,50,100", "comma-separated traffic percentages to shift to the new revision")
	interval := fs.Duration("interval", 20*time.Second, "how long to hold each step before advancing")
	namespace := fs.String("namespace", "default", "namespace of the service")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) < 2 {
		return fmt.Errorf("usage: knative-ctl canary <service> <image> [--steps 10,50,100] [--interval 20s] [--namespace default]")
	}
	service, image := positional[0], positional[1]

	percents, err := parseSteps(*steps)
	if err != nil {
		return err
	}

	oldRev, err := latestReadyRevision(*namespace, service)
	if err != nil {
		return fmt.Errorf("looking up current revision: %w", err)
	}
	fmt.Printf("current revision (100%% traffic): %s\n", oldRev)

	// Pin traffic explicitly to the current revision before touching the
	// template. If the service was still tracking latestRevision:true (its
	// default, untouched state), patching the image would otherwise
	// auto-promote the brand new, unvalidated revision to 100% immediately.
	if err := setTraffic(*namespace, service, []trafficTarget{{RevisionName: oldRev, Percent: 100}}); err != nil {
		return fmt.Errorf("pinning current revision before rollout: %w", err)
	}

	newRev, err := deployNewRevision(*namespace, service, image, oldRev)
	if err != nil {
		return err
	}
	fmt.Printf("new revision ready: %s\n", newRev)

	for i, p := range percents {
		fmt.Printf("step %d/%d: shifting %d%% traffic to %s (%d%% stays on %s)\n", i+1, len(percents), p, newRev, 100-p, oldRev)
		targets := []trafficTarget{
			{RevisionName: newRev, Percent: p, Tag: "canary"},
			{RevisionName: oldRev, Percent: 100 - p, Tag: "stable"},
		}
		if err := setTraffic(*namespace, service, targets); err != nil {
			return err
		}
		if i < len(percents)-1 {
			time.Sleep(*interval)
		}
	}

	fmt.Printf("canary complete: %s now serving 100%% traffic, roll back any time with:\n  knative-ctl rollback %s %s --namespace %s\n", newRev, service, oldRev, *namespace)
	return nil
}

func runBlueGreen(args []string) error {
	fs := flag.NewFlagSet("bluegreen", flag.ExitOnError)
	namespace := fs.String("namespace", "default", "namespace of the service")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) < 2 {
		return fmt.Errorf("usage: knative-ctl bluegreen <service> <image> [--namespace default]")
	}
	service, image := positional[0], positional[1]

	oldRev, err := latestReadyRevision(*namespace, service)
	if err != nil {
		return fmt.Errorf("looking up current revision: %w", err)
	}
	fmt.Printf("current (blue) revision: %s\n", oldRev)

	// Pin traffic explicitly to blue before touching the template, for the
	// same reason as canary: otherwise a service still on latestRevision:true
	// would flip 100% to the untested green revision the instant its image
	// is patched, defeating the "dark deploy" guarantee.
	if err := setTraffic(*namespace, service, []trafficTarget{{RevisionName: oldRev, Percent: 100}}); err != nil {
		return fmt.Errorf("pinning current revision before rollout: %w", err)
	}

	newRev, err := deployNewRevision(*namespace, service, image, oldRev)
	if err != nil {
		return err
	}
	fmt.Printf("new (green) revision ready, deployed dark at 0%% traffic: %s\n", newRev)

	fmt.Println("cutting over: 100% traffic -> green, blue retained at 0% for instant rollback")
	targets := []trafficTarget{
		{RevisionName: newRev, Percent: 100, Tag: "green"},
		{RevisionName: oldRev, Percent: 0, Tag: "blue"},
	}
	if err := setTraffic(*namespace, service, targets); err != nil {
		return err
	}

	fmt.Printf("blue-green cutover complete: %s live, roll back any time with:\n  knative-ctl rollback %s %s --namespace %s\n", newRev, service, oldRev, *namespace)
	return nil
}

func runRollback(args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ExitOnError)
	namespace := fs.String("namespace", "default", "namespace of the service")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) < 2 {
		return fmt.Errorf("usage: knative-ctl rollback <service> <revision> [--namespace default]")
	}
	service, revision := positional[0], positional[1]

	fmt.Printf("rolling back %s to %s: 100%% traffic\n", service, revision)
	targets := []trafficTarget{
		{RevisionName: revision, Percent: 100, Tag: "rollback"},
	}
	return setTraffic(*namespace, service, targets)
}

// deployNewRevision patches the service's container image (leaving every
// other field of the pod spec untouched via a JSON patch rather than a
// merge patch, which would clobber the container array), pins traffic at
// 0% to the just-superseded revision so it keeps serving while the new one
// rolls out, waits for the new revision to become Ready, and returns its
// name.
func deployNewRevision(namespace, service, image, oldRev string) (string, error) {
	// Always assign a fresh revision name, via "add" rather than "replace"
	// so this works whether the current revision's name was auto-generated
	// (metadata.name unset) or explicitly pinned (e.g. by one of the static
	// example manifests): a "replace" on an unset path fails, and reusing
	// an existing pinned name while changing the image is rejected by
	// Knative's webhook as an illegal mutation of an immutable revision.
	newRevName := fmt.Sprintf("%s-%d", service, time.Now().Unix())
	patch := fmt.Sprintf(`[{"op":"replace","path":"/spec/template/spec/containers/0/image","value":%q},{"op":"add","path":"/spec/template/metadata/name","value":%q}]`, image, newRevName)
	if err := kubectlRun("patch", "ksvc", service, "-n", namespace, "--type", "json", "-p", patch); err != nil {
		return "", fmt.Errorf("patching image: %w", err)
	}

	newRev, err := pollLatestCreatedRevision(namespace, service, oldRev)
	if err != nil {
		return "", err
	}

	if err := kubectlRun("wait", "--for=condition=Ready", "revision/"+newRev, "-n", namespace, "--timeout=180s"); err != nil {
		return "", fmt.Errorf("waiting for revision/%s to become ready: %w", newRev, err)
	}
	return newRev, nil
}

func setTraffic(namespace, service string, targets []trafficTarget) error {
	body, err := json.Marshal(map[string]any{
		"spec": map[string]any{"traffic": targets},
	})
	if err != nil {
		return err
	}
	if err := kubectlRun("patch", "ksvc", service, "-n", namespace, "--type", "merge", "-p", string(body)); err != nil {
		return fmt.Errorf("patching traffic split: %w", err)
	}
	return nil
}

func latestReadyRevision(namespace, service string) (string, error) {
	out, err := kubectlOutput("get", "ksvc", service, "-n", namespace, "-o", "jsonpath={.status.latestReadyRevisionName}")
	if err != nil {
		return "", err
	}
	rev := strings.TrimSpace(out)
	if rev == "" {
		return "", fmt.Errorf("service %s/%s has no ready revision yet", namespace, service)
	}
	return rev, nil
}

// pollLatestCreatedRevision waits for status.latestCreatedRevisionName to
// change away from previous, since the field only updates once the
// controller has reconciled the new spec.
func pollLatestCreatedRevision(namespace, service, previous string) (string, error) {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		out, err := kubectlOutput("get", "ksvc", service, "-n", namespace, "-o", "jsonpath={.status.latestCreatedRevisionName}")
		if err == nil {
			rev := strings.TrimSpace(out)
			if rev != "" && rev != previous {
				return rev, nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("timed out waiting for a new revision to appear")
}

func parseSteps(raw string) ([]int, error) {
	parts := strings.Split(raw, ",")
	steps := make([]int, 0, len(parts))
	for _, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, fmt.Errorf("invalid step %q: %w", p, err)
		}
		if v < 1 || v > 100 {
			return nil, fmt.Errorf("step %d out of range 1-100", v)
		}
		steps = append(steps, v)
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("no steps given")
	}
	if steps[len(steps)-1] != 100 {
		steps = append(steps, 100)
	}
	return steps, nil
}

func kubectlOutput(args ...string) (string, error) {
	cmd := exec.Command("kubectl", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, out.String())
	}
	return out.String(), nil
}
