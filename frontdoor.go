// Install/uninstall for the front-door stack: MetalLB, cert-manager, and
// nginx-ingress. Same pattern as installKnative/installKourier in main.go
// -- apply with retry, wait for rollout. Unmodified upstream releases
// (metallb, cert-manager) are fetched straight from their download URL,
// exactly like servingCore/kourier do -- no local vendored copy of
// something that isn't actually customized. Only genuinely-edited or
// original content lives under infra/ingress/: ingress-nginx (hand-edited,
// see infra/ingress/README.md for what changed and why), and the
// resources this repo authored itself (kourier-clusterip, tls-selfsigned,
// metallb-pool).
package main

import "fmt"

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
	fmt.Println("ingress-nginx installed as the front door, kourier reverted to ClusterIP")
	return nil
}

func uninstallIngress() error {
	if err := deleteManifest(ingressNginxCore()); err != nil {
		return err
	}
	fmt.Println("ingress-nginx uninstalled (kourier's Service left as ClusterIP -- reinstall kourier if you want its own LoadBalancer back)")
	return nil
}
