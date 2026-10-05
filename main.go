// knative-ctl installs and uninstalls Knative Serving, Kourier, Eventing,
// Zipkin tracing, and the north-south stack (MetalLB, cert-manager,
// nginx-ingress) on whatever cluster the current kubeconfig points at, by
// shelling out to kubectl against the upstream release manifests.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

const defaultVersion = "knative-v1.23.0"

// How long to wait for any one Deployment to come up. Almost all of that
// is image pulls: on a fresh node nothing is cached, and a slow link can
// take ten minutes or more on a single large image.
const rolloutDeadline = 30 * time.Minute

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

// Eventing: core plus the two pieces the Broker in eventing-demo/ needs --
// an InMemoryChannel implementation and the multi-tenant channel-based
// Broker on top of it. Same release tag as Serving.
func eventingManifest(version, file, name string) manifest {
	return manifest{
		name: name,
		url:  fmt.Sprintf("https://github.com/knative/eventing/releases/download/%s/%s", version, file),
	}
}

func eventingCRDs(version string) manifest {
	return eventingManifest(version, "eventing-crds.yaml", "knative eventing CRDs")
}

func eventingCore(version string) manifest {
	return eventingManifest(version, "eventing-core.yaml", "knative eventing core")
}

func inMemoryChannel(version string) manifest {
	return eventingManifest(version, "in-memory-channel.yaml", "in-memory channel")
}

func mtChannelBroker(version string) manifest {
	return eventingManifest(version, "mt-channel-broker.yaml", "mt channel broker")
}

// Tracing. Every Knative component exports OTLP/HTTP straight to Zipkin's
// own OTLP collector endpoint -- see infra/observability/zipkin.yaml for
// why that's a zipkin-otel image and not stock Zipkin.
const zipkinOTLPEndpoint = "http://zipkin.observability.svc.cluster.local:9411/v1/traces"

func zipkin() manifest {
	return manifest{name: "zipkin", url: "infra/observability/zipkin.yaml"}
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
	case "trace-sampling":
		err = runTraceSampling(rest)
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

Platform lifecycle. "all" installs/uninstalls knative, kourier, metallb,
cert-manager, ingress and zipkin in dependency order -- eventing is its
own target, never part of "all" (it's the heaviest piece by far).
--version only applies to knative/kourier/eventing, the rest are pinned:
  knative-ctl install   knative|kourier|eventing|zipkin|metallb|cert-manager|ingress|all [--version knative-vX.Y.Z]
  knative-ctl uninstall knative|kourier|eventing|zipkin|metallb|cert-manager|ingress|all [--version knative-vX.Y.Z]

kourier is always ClusterIP-only: east-west traffic goes straight to
kourier-internal, and ingress puts nginx-ingress in front of that same
Service as the single external LoadBalancer (infra/ingress/README.md).

zipkin deploys Zipkin and points Serving, Kourier and (if installed)
Eventing at it (infra/observability/README.md). Sampling starts at 100%:
  knative-ctl trace-sampling <rate 0..1>

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
	case "eventing":
		return installEventing(version)
	case "zipkin":
		return installZipkin()
	case "all":
		// Dependency order: knative before kourier (kourier needs its
		// CRDs/webhook), kourier before ingress (ingress reverts its
		// Service), metallb/cert-manager before ingress (its manifest
		// references MetalLB's LoadBalancer type and cert-manager's TLS
		// secret). metallb and cert-manager don't depend on each other.
		// zipkin last, so its tracing config lands on everything above.
		for _, step := range []func() error{
			func() error { return installKnative(version) },
			func() error { return installKourier(version) },
			installMetalLB,
			installCertManager,
			installIngress,
			installZipkin,
		} {
			if err := step(); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown install target %q (want knative|kourier|eventing|zipkin|metallb|cert-manager|ingress|all)", target)
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
	case "eventing":
		return uninstallEventing(version)
	case "zipkin":
		return uninstallZipkin()
	case "all":
		// Reverse of install's order.
		for _, step := range []func() error{
			uninstallZipkin,
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
		return fmt.Errorf("unknown uninstall target %q (want knative|kourier|eventing|zipkin|metallb|cert-manager|ingress|all)", target)
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
		if err := waitDeployment("knative-serving", deploy); err != nil {
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
	// Kourier is never an external LoadBalancer here. Upstream kourier.yaml
	// (just applied above) declares its `kourier` Service that way, which
	// on a cluster with MetalLB immediately claims the first pool address
	// -- the one nginx-ingress is supposed to get. Everything reaches
	// Kourier through ClusterIP instead: east-west traffic and the
	// north-south Ingress both target kourier-internal. Done before
	// waiting on any rollout, so the window where it's a LoadBalancer is
	// seconds, and on every install, since re-applying upstream resets it.
	if err := applyWithRetry(kourierClusterIP()); err != nil {
		return err
	}

	// net-kourier-controller is deployed into knative-serving by kourier.yaml,
	// not kourier-system -- only the gateway lands there.
	if err := waitDeployment("knative-serving", "net-kourier-controller"); err != nil {
		return fmt.Errorf("waiting for deployment/net-kourier-controller: %w", err)
	}
	if err := waitDeployment("kourier-system", "3scale-kourier-gateway"); err != nil {
		return fmt.Errorf("waiting for deployment/3scale-kourier-gateway: %w", err)
	}

	if err := kubectlRun("patch", "configmap/config-network", "-n", "knative-serving",
		"--type", "merge", "-p", `{"data":{"ingress-class":"kourier.ingress.networking.knative.dev"}}`); err != nil {
		return fmt.Errorf("setting kourier as the ingress class: %w", err)
	}

	if zipkinInstalled() {
		if err := configureTracing("1"); err != nil {
			return err
		}
	}

	fmt.Println("kourier installed as the default ingress class, ClusterIP only (east-west: kourier-internal.kourier-system.svc.cluster.local)")
	return nil
}

func installEventing(version string) error {
	crds := eventingCRDs(version)
	if err := applyWithRetry(crds); err != nil {
		return err
	}
	if err := kubectlWait("--for=condition=Established", "--timeout=60s", "-f", crds.url); err != nil {
		return fmt.Errorf("waiting for %s to establish: %w", crds.name, err)
	}
	for _, m := range []manifest{eventingCore(version), inMemoryChannel(version), mtChannelBroker(version)} {
		if err := applyWithRetry(m); err != nil {
			return err
		}
	}
	for _, deploy := range eventingDeployments {
		if err := waitDeployment("knative-eventing", deploy); err != nil {
			return fmt.Errorf("waiting for deployment/%s: %w", deploy, err)
		}
	}
	// Same self-heal as kourier above: zipkin may have been installed
	// before eventing existed, in which case nothing has pointed
	// eventing's own config-observability at it yet.
	if zipkinInstalled() {
		if err := configureTracing("1"); err != nil {
			return err
		}
	}
	fmt.Println("knative eventing installed and ready (core, in-memory channel, mt channel broker)")
	return nil
}

func uninstallEventing(version string) error {
	for _, m := range []manifest{mtChannelBroker(version), inMemoryChannel(version), eventingCore(version), eventingCRDs(version)} {
		if err := deleteManifest(m); err != nil {
			return err
		}
	}
	fmt.Println("knative eventing uninstalled")
	return nil
}

var eventingDeployments = []string{
	"eventing-controller", "eventing-webhook",
	"imc-controller", "imc-dispatcher",
	"mt-broker-controller", "mt-broker-ingress", "mt-broker-filter",
}

func installZipkin() error {
	if err := applyWithRetry(zipkin()); err != nil {
		return err
	}
	if err := waitDeployment("observability", "zipkin"); err != nil {
		return fmt.Errorf("waiting for deployment/zipkin: %w", err)
	}
	if err := configureTracing("1"); err != nil {
		return err
	}
	fmt.Println("zipkin installed, tracing enabled -- open the UI with:\n  kubectl port-forward -n observability svc/zipkin 9411:9411   # http://localhost:9411")
	return nil
}

func uninstallZipkin() error {
	// Turn exporting off first, or every component keeps retrying a
	// collector that no longer exists.
	if err := configureTracing(""); err != nil {
		return err
	}
	if err := deleteManifest(zipkin()); err != nil {
		return err
	}
	fmt.Println("zipkin uninstalled, tracing disabled")
	return nil
}

func runTraceSampling(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: knative-ctl trace-sampling <rate 0..1>")
	}
	rate, err := strconv.ParseFloat(args[0], 64)
	if err != nil || rate < 0 || rate > 1 {
		return fmt.Errorf("sampling rate must be a number between 0 and 1, got %q", args[0])
	}
	if !zipkinInstalled() {
		return fmt.Errorf("zipkin not found -- run 'knative-ctl install zipkin' first")
	}
	return configureTracing(args[0])
}

func zipkinInstalled() bool {
	_, err := kubectlOutput("get", "deployment", "zipkin", "-n", "observability")
	return err == nil
}

func namespaceExists(name string) bool {
	_, err := kubectlOutput("get", "namespace", name)
	return err == nil
}

// configureTracing points every installed Knative component at Zipkin
// with the given sampling rate, or switches tracing off again when rate
// is "". Three separate ConfigMaps, because there are three separate
// things emitting spans: Serving (activator + each revision's
// queue-proxy), Kourier's Envoy gateway, and Eventing's data plane.
//
// The patched keys survive a later re-apply of the upstream manifests:
// those only ship an `_example` block, so kubectl's 3-way merge has no
// opinion on keys it never set.
func configureTracing(rate string) error {
	observability := fmt.Sprintf(`{"data":{"tracing-protocol":"http/protobuf","tracing-endpoint":%q,"tracing-sampling-rate":%q}}`, zipkinOTLPEndpoint, rate)
	kourier := observability
	if rate == "" {
		observability = `{"data":{"tracing-protocol":"none","tracing-endpoint":null,"tracing-sampling-rate":null}}`
		// config-kourier has no "none": an empty endpoint is its off switch.
		kourier = `{"data":{"tracing-endpoint":"","tracing-sampling-rate":null}}`
	}

	type target struct {
		namespace, configMap, patch string
		// Components that only read their tracing config at startup.
		// Revision pods aren't listed: queue-proxy gets its config when
		// the pod is created, so existing pods pick it up as they scale
		// to zero and back.
		restart []string
	}
	var targets []target
	if namespaceExists("knative-serving") {
		targets = append(targets, target{"knative-serving", "config-observability", observability, []string{"activator"}})
		if _, err := kubectlOutput("get", "configmap", "config-kourier", "-n", "knative-serving"); err == nil {
			targets = append(targets, target{"knative-serving", "config-kourier", kourier, []string{"net-kourier-controller"}})
		}
	}
	if namespaceExists("knative-eventing") {
		targets = append(targets, target{"knative-eventing", "config-observability", observability, []string{"imc-dispatcher", "mt-broker-ingress", "mt-broker-filter"}})
	}

	for _, t := range targets {
		if err := kubectlRun("patch", "configmap/"+t.configMap, "-n", t.namespace, "--type", "merge", "-p", t.patch); err != nil {
			return fmt.Errorf("patching %s/%s for tracing: %w", t.namespace, t.configMap, err)
		}
		for _, deploy := range t.restart {
			if _, err := kubectlOutput("get", "deployment", deploy, "-n", t.namespace); err != nil {
				continue
			}
			if err := kubectlRun("rollout", "restart", "deployment/"+deploy, "-n", t.namespace); err != nil {
				return fmt.Errorf("restarting deployment/%s: %w", deploy, err)
			}
			if err := waitDeployment(t.namespace, deploy); err != nil {
				return fmt.Errorf("waiting for deployment/%s: %w", deploy, err)
			}
		}
	}
	if rate == "" {
		fmt.Println("tracing disabled")
	} else {
		fmt.Printf("tracing -> %s (sampling rate %s)\n", zipkinOTLPEndpoint, rate)
	}
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
	if err := waitDeployment("metallb-system", "controller"); err != nil {
		return fmt.Errorf("waiting for deployment/controller: %w", err)
	}
	if err := kubectlRun("rollout", "status", "daemonset/speaker", "-n", "metallb-system", "--timeout=600s"); err != nil {
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
		if err := waitDeployment("cert-manager", deploy); err != nil {
			return fmt.Errorf("waiting for deployment/%s: %w", deploy, err)
		}
	}
	// The Certificate lives in ingress-nginx, next to the controller that
	// mounts its Secret -- but on a fresh cluster this runs before the
	// ingress target has created that namespace. Created imperatively,
	// not as part of tls-selfsigned.yaml, so that uninstalling
	// cert-manager can never delete the namespace nginx-ingress runs in.
	if !namespaceExists("ingress-nginx") {
		if err := kubectlRun("create", "namespace", "ingress-nginx"); err != nil {
			return fmt.Errorf("creating namespace ingress-nginx: %w", err)
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
	// Requires Kourier already installed -- nginx-ingress is the single
	// external LoadBalancer, and all it does is hand traffic to Kourier.
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
	if err := waitDeployment("ingress-nginx", "ingress-nginx-controller"); err != nil {
		return fmt.Errorf("waiting for deployment/ingress-nginx-controller: %w", err)
	}
	if err := applyWithRetry(kourierNorthSouthIngress()); err != nil {
		return err
	}
	fmt.Println("ingress-nginx installed for north-south, north-south Ingress -> kourier-internal applied")
	return nil
}

func uninstallIngress() error {
	if err := deleteManifest(kourierNorthSouthIngress()); err != nil {
		return err
	}
	if err := deleteManifest(ingressNginxCore()); err != nil {
		return err
	}
	fmt.Println("ingress-nginx uninstalled (kourier stays ClusterIP-only: nothing is reachable from outside the cluster until ingress is reinstalled)")
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

// waitDeployment blocks until a Deployment has fully rolled out. A bare
// `kubectl rollout status` isn't enough on a fresh node: it gives up the
// moment the Deployment passes its own progressDeadlineSeconds (10
// minutes by default), which one slow image pull does all by itself --
// so keep asking until rolloutDeadline. (`kubectl wait
// --for=condition=Available` isn't an alternative: a Deployment that
// tolerates one unavailable replica is "Available" with zero pods.)
func waitDeployment(namespace, name string) error {
	deadline := time.Now().Add(rolloutDeadline)
	for {
		// kubectlOutput, not kubectlRun: each failed attempt would
		// otherwise print its own error, every few seconds, for as long
		// as the pull takes.
		out, err := kubectlOutput("rollout", "status", "deployment/"+name, "-n", namespace, "--timeout=120s")
		if err == nil {
			fmt.Print(out)
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		fmt.Printf("still waiting for deployment/%s in %s\n", name, namespace)
		time.Sleep(30 * time.Second)
	}
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
