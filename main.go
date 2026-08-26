// knative-ctl installs and uninstalls Knative Serving and Kourier on
// whatever cluster the current kubeconfig points at, by shelling out to
// kubectl against the upstream release manifests.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

const defaultVersion = "knative-v1.23.0"

type manifest struct {
	name string
	url  string
}

func servingCRDs(version string) manifest {
	return manifest{
		name: "knative serving CRDs",
		url:  fmt.Sprintf("https://github.com/knative/serving/releases/download/%s/serving-crds.yaml", version),
	}
}

func servingCore(version string) manifest {
	return manifest{
		name: "knative serving core",
		url:  fmt.Sprintf("https://github.com/knative/serving/releases/download/%s/serving-core.yaml", version),
	}
}

func kourier(version string) manifest {
	return manifest{
		name: "kourier",
		url:  fmt.Sprintf("https://github.com/knative-extensions/net-kourier/releases/download/%s/kourier.yaml", version),
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	action := os.Args[1]
	rest := os.Args[2:]

	var err error
	switch action {
	case "install", "uninstall":
		err = runLifecycle(action, rest)
	case "canary":
		err = runCanary(rest)
	case "bluegreen":
		err = runBlueGreen(rest)
	case "rollback":
		err = runRollback(rest)
	default:
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runLifecycle(action string, args []string) error {
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}
	target := args[0]
	version := defaultVersion
	if len(args) > 1 && args[1] == "--version" && len(args) > 2 {
		version = args[2]
	}
	if action == "install" {
		return install(target, version)
	}
	return uninstall(target, version)
}

func usage() {
	fmt.Println(`knative-ctl - Knative Serving + Kourier lifecycle and release automation

Platform lifecycle. "all" installs/uninstalls everything below in
dependency order (knative, kourier, metallb, cert-manager, ingress) --
--version only applies to knative/kourier, the rest are pinned versions:
  knative-ctl install   knative|kourier|metallb|cert-manager|ingress|all [--version knative-vX.Y.Z]
  knative-ctl uninstall knative|kourier|metallb|cert-manager|ingress|all [--version knative-vX.Y.Z]

ingress makes nginx-ingress the single external LoadBalancer in front of
Kourier (infra/ingress/README.md) -- needs kourier installed first.

Release flows (operate on a knative Service already deployed):
  knative-ctl canary    <service> <image> [--steps 10,50,100] [--interval 20s] [--namespace default]
  knative-ctl bluegreen <service> <image> [--namespace default]
  knative-ctl rollback  <service> <revision> [--namespace default]

Defaults to ` + defaultVersion + ` for --version if omitted.`)
}

func install(target, version string) error {
	switch target {
	case "knative":
		return installKnative(version)
	case "kourier":
		return installKourier(version)
	case "metallb":
		return installMetalLB()
	case "cert-manager":
		return installCertManager()
	case "ingress":
		return installIngress()
	case "all":
		// Dependency order: knative before kourier (kourier needs its
		// CRDs/webhook), kourier before ingress (ingress reverts its
		// Service), metallb/cert-manager before ingress (its manifest
		// references MetalLB's LoadBalancer type and cert-manager's TLS
		// secret). metallb and cert-manager don't depend on each other.
		for _, step := range []func() error{
			func() error { return installKnative(version) },
			func() error { return installKourier(version) },
			installMetalLB,
			installCertManager,
			installIngress,
		} {
			if err := step(); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown install target %q (want knative|kourier|metallb|cert-manager|ingress|all)", target)
	}
}

func uninstall(target, version string) error {
	switch target {
	case "knative":
		return uninstallKnative(version)
	case "kourier":
		return uninstallKourier(version)
	case "metallb":
		return uninstallMetalLB()
	case "cert-manager":
		return uninstallCertManager()
	case "ingress":
		return uninstallIngress()
	case "all":
		// Reverse of install's order.
		for _, step := range []func() error{
			uninstallIngress,
			uninstallCertManager,
			uninstallMetalLB,
			func() error { return uninstallKourier(version) },
			func() error { return uninstallKnative(version) },
		} {
			if err := step(); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown uninstall target %q (want knative|kourier|metallb|cert-manager|ingress|all)", target)
	}
}

func installKnative(version string) error {
	crds := servingCRDs(version)
	if err := applyWithRetry(crds); err != nil {
		return err
	}
	if err := kubectlWait("--for=condition=Established", "--timeout=60s", "-f", crds.url); err != nil {
		return fmt.Errorf("waiting for %s to establish: %w", crds.name, err)
	}

	core := servingCore(version)
	if err := applyWithRetry(core); err != nil {
		return err
	}

	for _, deploy := range []string{"activator", "autoscaler", "controller", "webhook"} {
		if err := kubectlRun("rollout", "status", "deployment/"+deploy, "-n", "knative-serving", "--timeout=120s"); err != nil {
			return fmt.Errorf("waiting for deployment/%s: %w", deploy, err)
		}
	}

	fmt.Println("knative serving installed and ready")
	return nil
}

func installKourier(version string) error {
	m := kourier(version)
	if err := applyWithRetry(m); err != nil {
		return err
	}
	// net-kourier-controller is deployed into knative-serving by kourier.yaml,
	// not kourier-system -- only the gateway lands there.
	if err := kubectlRun("rollout", "status", "deployment/net-kourier-controller", "-n", "knative-serving", "--timeout=120s"); err != nil {
		return fmt.Errorf("waiting for deployment/net-kourier-controller: %w", err)
	}
	if err := kubectlRun("rollout", "status", "deployment/3scale-kourier-gateway", "-n", "kourier-system", "--timeout=120s"); err != nil {
		return fmt.Errorf("waiting for deployment/3scale-kourier-gateway: %w", err)
	}

	if err := kubectlRun("patch", "configmap/config-network", "-n", "knative-serving",
		"--type", "merge", "-p", `{"data":{"ingress-class":"kourier.ingress.networking.knative.dev"}}`); err != nil {
		return fmt.Errorf("setting kourier as the ingress class: %w", err)
	}

	fmt.Println("kourier installed and set as the default ingress class")
	return nil
}

func uninstallKnative(version string) error {
	if err := deleteManifest(servingCore(version)); err != nil {
		return err
	}
	if err := deleteManifest(servingCRDs(version)); err != nil {
		return err
	}
	fmt.Println("knative serving uninstalled")
	return nil
}

func uninstallKourier(version string) error {
	if err := deleteManifest(kourier(version)); err != nil {
		return err
	}
	fmt.Println("kourier uninstalled")
	return nil
}

// applyWithRetry applies a manifest, retrying once on failure. A single
// retry is enough in practice: applying serving-core can hit a transient
// "timed out waiting for the condition" on the Certificate CRD while the
// API server is still catching up on a resource-constrained node.
func applyWithRetry(m manifest) error {
	fmt.Printf("applying %s (%s)\n", m.name, m.url)
	err := kubectlRun("apply", "-f", m.url)
	if err == nil {
		return nil
	}
	fmt.Printf("apply of %s failed, retrying once: %v\n", m.name, err)
	time.Sleep(5 * time.Second)
	if err := kubectlRun("apply", "-f", m.url); err != nil {
		return fmt.Errorf("applying %s: %w", m.name, err)
	}
	return nil
}

func deleteManifest(m manifest) error {
	fmt.Printf("deleting %s (%s)\n", m.name, m.url)
	if err := kubectlRun("delete", "-f", m.url, "--ignore-not-found=true"); err != nil {
		return fmt.Errorf("deleting %s: %w", m.name, err)
	}
	return nil
}

func kubectlRun(args ...string) error {
	cmd := exec.Command("kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func kubectlWait(args ...string) error {
	return kubectlRun(append([]string{"wait"}, args...)...)
}
