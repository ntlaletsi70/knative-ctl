// knative-ctl installs and uninstalls Knative Serving, Kourier, and the
// north-south stack (MetalLB, cert-manager, nginx-ingress) on whatever
// cluster the current kubeconfig points at, by shelling out to kubectl
// against the upstream release manifests.
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

// North-south (metallb, cert-manager, ingress-nginx). Unmodified upstream
// releases (metallb, cert-manager) are fetched straight from their
// download URL, exactly like servingCore/kourier above -- no local
// vendored copy of something that isn't actually customized. Only
// genuinely-edited or original content lives under infra/ingress/:
// ingress-nginx (hand-edited, see infra/ingress/README.md for what
// changed and why), and the resources this repo authored itself
// (kourier-clusterip, tls-selfsigned, metallb-pool, kourier-northsouth-ingress).
const (
	metallbVersion     = "v0.16.0"
	certManagerVersion = "v1.21.1"
	infraDir           = "infra/ingress"
)

func metallbCore() manifest {
	return manifest{
		name: "metallb",
		url:  fmt.Sprintf("https://raw.githubusercontent.com/metallb/metallb/%s/config/manifests/metallb-native.yaml", metallbVersion),
	}
}

func metallbPool() manifest {
	return manifest{name: "metallb IP pool", url: infraDir + "/metallb-pool.yaml"}
}

func certManagerCore() manifest {
	return manifest{
		name: "cert-manager",
		url:  fmt.Sprintf("https://github.com/cert-manager/cert-manager/releases/download/%s/cert-manager.yaml", certManagerVersion),
	}
}

func tlsSelfsigned() manifest {
	return manifest{name: "self-signed ClusterIssuer + Certificate", url: infraDir + "/tls-selfsigned.yaml"}
}

func ingressNginxCore() manifest {
	return manifest{name: "ingress-nginx", url: infraDir + "/ingress-nginx-baremetal-v1.11.3.yaml"}
}

func kourierClusterIP() manifest {
	return manifest{name: "kourier ClusterIP override", url: infraDir + "/kourier-clusterip.yaml"}
}

func kourierNorthSouthIngress() manifest {
	return manifest{name: "north-south Ingress -> kourier-internal", url: infraDir + "/kourier-northsouth-ingress.yaml"}
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

func installMetalLB() error {
	if err := applyWithRetry(metallbCore()); err != nil {
		return err
	}
	if err := kubectlRun("rollout", "status", "deployment/controller", "-n", "metallb-system", "--timeout=120s"); err != nil {
		return fmt.Errorf("waiting for deployment/controller: %w", err)
	}
	if err := kubectlRun("rollout", "status", "daemonset/speaker", "-n", "metallb-system", "--timeout=120s"); err != nil {
		return fmt.Errorf("waiting for daemonset/speaker: %w", err)
	}
	// The IPAddressPool/L2Advertisement CRs need the controller's webhook
	// serving already (just waited for above); applyWithRetry's built-in
	// retry absorbs the rare case the CRDs themselves aren't established
	// yet.
	if err := applyWithRetry(metallbPool()); err != nil {
		return err
	}
	fmt.Println("metallb installed with the IP pool from " + infraDir + "/metallb-pool.yaml")
	return nil
}

func uninstallMetalLB() error {
	if err := deleteManifest(metallbPool()); err != nil {
		return err
	}
	if err := deleteManifest(metallbCore()); err != nil {
		return err
	}
	fmt.Println("metallb uninstalled")
	return nil
}

func installCertManager() error {
	if err := applyWithRetry(certManagerCore()); err != nil {
		return err
	}
	for _, deploy := range []string{"cert-manager", "cert-manager-webhook", "cert-manager-cainjector"} {
		if err := kubectlRun("rollout", "status", "deployment/"+deploy, "-n", "cert-manager", "--timeout=120s"); err != nil {
			return fmt.Errorf("waiting for deployment/%s: %w", deploy, err)
		}
	}
	if err := applyWithRetry(tlsSelfsigned()); err != nil {
		return err
	}
	fmt.Println("cert-manager installed, self-signed ClusterIssuer + Certificate applied")
	return nil
}

func uninstallCertManager() error {
	if err := deleteManifest(tlsSelfsigned()); err != nil {
		return err
	}
	if err := deleteManifest(certManagerCore()); err != nil {
		return err
	}
	fmt.Println("cert-manager uninstalled")
	return nil
}

func installIngress() error {
	// Requires Kourier already installed -- this pattern makes nginx-ingress
	// the single external LoadBalancer instead, which only makes sense on
	// top of an existing Kourier install.
	if _, err := kubectlOutput("get", "svc", "kourier", "-n", "kourier-system"); err != nil {
		return fmt.Errorf("kourier service not found -- run 'knative-ctl install kourier' first")
	}
	if err := applyWithRetry(ingressNginxCore()); err != nil {
		return err
	}
	// The manifest's --default-ssl-certificate arg references a Secret
	// cert-manager creates. If cert-manager/tls haven't run yet, the pod
	// will sit retrying its volume mount until that Secret shows up --
	// same transient-then-self-healing pattern as its own webhook-cert
	// Secret, not a hang. Still worth a clear error up front rather than
	// a silent crash-loop for anyone skipping straight to this target.
	if _, err := kubectlOutput("get", "secret", "ingress-nginx-default-tls", "-n", "ingress-nginx"); err != nil {
		fmt.Println("note: TLS secret ingress-nginx/ingress-nginx-default-tls not found yet -- run 'knative-ctl install cert-manager' if you haven't; the controller pod will wait for it")
	}
	if err := kubectlRun("rollout", "status", "deployment/ingress-nginx-controller", "-n", "ingress-nginx", "--timeout=180s"); err != nil {
		return fmt.Errorf("waiting for deployment/ingress-nginx-controller: %w", err)
	}
	if err := applyWithRetry(kourierClusterIP()); err != nil {
		return err
	}
	if err := applyWithRetry(kourierNorthSouthIngress()); err != nil {
		return err
	}
	fmt.Println("ingress-nginx installed for north-south, kourier reverted to ClusterIP, north-south Ingress applied")
	return nil
}

func uninstallIngress() error {
	if err := deleteManifest(kourierNorthSouthIngress()); err != nil {
		return err
	}
	if err := deleteManifest(ingressNginxCore()); err != nil {
		return err
	}
	fmt.Println("ingress-nginx uninstalled (kourier's Service left as ClusterIP -- reinstall kourier if you want its own LoadBalancer back)")
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
