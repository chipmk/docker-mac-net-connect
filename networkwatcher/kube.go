//go:build darwin

package networkwatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	corev1 "k8s.io/api/core/v1"
)

// KubeWatcher watches Kubernetes Nodes and ServiceCIDRs for CIDR changes.
type KubeWatcher struct {
	clientset *kubernetes.Clientset
	vmRoutes  []string // CNI routes discovered from the VM (fallback for empty spec.podCIDR)
}

// NewKubeWatcher creates a KubeWatcher pinned to the given kubeconfig context.
// kubeContext must be non-empty - use ResolveKubeContext to snapshot the
// context at session start.
// It verifies the API server is on localhost (local cluster) and
// reachable before returning. Returns an error if k8s is not available.
func NewKubeWatcher(homeDir string, kubeContext string) (*KubeWatcher, error) {
	kubeconfigPath := filepath.Join(homeDir, ".kube", "config")

	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{CurrentContext: kubeContext},
	)

	config, err := loader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	// Only connect to local clusters. Remote clusters would have CIDRs
	// for networks that aren't on the local VM tunnel.
	serverURL, err := url.Parse(config.Host)
	if err != nil {
		return nil, fmt.Errorf("parse API server URL: %w", err)
	}
	host := serverURL.Hostname()
	if !isLocalhostAddr(host) {
		return nil, fmt.Errorf("skipping remote kubernetes cluster at %s", host)
	}

	fmt.Printf("Using kubeconfig context: %s (server: %s)\n", kubeContext, config.Host)

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	// Verify connectivity by listing nodes.
	_, err = clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("kubernetes API unreachable: %w", err)
	}

	return &KubeWatcher{clientset: clientset}, nil
}

// ResolveKubeContext returns the current kubeconfig context name.
// Used to snapshot the context at session start so it stays pinned
// even if the user switches contexts later.
func ResolveKubeContext(homeDir string) string {
	kubeconfigPath := filepath.Join(homeDir, ".kube", "config")
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{},
	)
	rawConfig, err := loader.RawConfig()
	if err != nil {
		return ""
	}
	return rawConfig.CurrentContext
}

// RunKubeWatcher retries connecting to k8s within a session. If k8s is
// unavailable it polls every 30s. When connected, it watches CIDRs until
// the watch errors out, then retries. Blocks until ctx is cancelled.
// vmRoutes are CNI routes from the VM, used as fallback for empty spec.podCIDR.
// kubeContext is the pinned context name for this session.
func RunKubeWatcher(
	ctx context.Context,
	homeDir string,
	kubeContext string,
	vmRoutes []string,
	onAdd func(cidr, name string),
	onRemove func(cidr, name string),
) {
	for {
		kw, err := NewKubeWatcher(homeDir, kubeContext)
		if err != nil {
			fmt.Printf("Kubernetes not available: %v\n", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				continue
			}
		}

		kw.vmRoutes = vmRoutes
		fmt.Println("Kubernetes detected, watching pod/service CIDRs")
		if err := kw.run(ctx, onAdd, onRemove); err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Printf("Kubernetes watcher error: %v\n", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// run watches Nodes and ServiceCIDRs, calling onAdd/onRemove as CIDRs
// change. Blocks until the context is cancelled or an unrecoverable error
// occurs.
func (kw *KubeWatcher) run(
	ctx context.Context,
	onAdd func(cidr, name string),
	onRemove func(cidr, name string),
) error {
	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := kw.watchNodes(ctx, onAdd, onRemove); err != nil {
			if ctx.Err() == nil {
				errCh <- fmt.Errorf("node watcher: %w", err)
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := kw.watchServiceCIDRs(ctx, onAdd, onRemove); err != nil {
			if ctx.Err() == nil {
				errCh <- fmt.Errorf("service CIDR watcher: %w", err)
			}
		}
	}()

	// Wait for context cancellation or first error.
	select {
	case <-ctx.Done():
		wg.Wait()
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (kw *KubeWatcher) watchNodes(
	ctx context.Context,
	onAdd func(cidr, name string),
	onRemove func(cidr, name string),
) error {
	// Initial list.
	nodeList, err := kw.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}

	knownCIDRs := map[string]string{} // node name -> podCIDR

	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		cidr := node.Spec.PodCIDR
		if cidr != "" && isIPv4CIDR(cidr) {
			knownCIDRs[node.Name] = cidr
			onAdd(cidr, "k8s-pod/"+node.Name)
		}
	}

	// If no node had spec.podCIDR set (e.g. Docker Desktop), fall back
	// to CNI routes discovered from the VM's routing table.
	if len(knownCIDRs) == 0 && len(kw.vmRoutes) > 0 {
		fmt.Println("No spec.podCIDR on nodes, using VM routes for pod CIDRs")
		for _, cidr := range kw.vmRoutes {
			if isIPv4CIDR(cidr) {
				onAdd(cidr, "k8s-pod/vm-route")
			}
		}
	}

	// Watch from the list's resource version.
	resourceVersion := nodeList.ResourceVersion

	for {
		watcher, err := kw.clientset.CoreV1().Nodes().Watch(ctx, metav1.ListOptions{
			ResourceVersion: resourceVersion,
		})
		if err != nil {
			return fmt.Errorf("watch nodes: %w", err)
		}

		for event := range watcher.ResultChan() {
			node, ok := event.Object.(*corev1.Node)
			if !ok {
				// Could be a Status object on 410 Gone.
				if event.Type == watch.Error {
					watcher.Stop()
					break
				}
				continue
			}

			switch event.Type {
			case watch.Added, watch.Modified:
				newCIDR := node.Spec.PodCIDR
				if !isIPv4CIDR(newCIDR) {
					continue
				}
				oldCIDR, existed := knownCIDRs[node.Name]

				if newCIDR != oldCIDR {
					if existed {
						onRemove(oldCIDR, "k8s-pod/"+node.Name)
					}
					knownCIDRs[node.Name] = newCIDR
					onAdd(newCIDR, "k8s-pod/"+node.Name)
				}

			case watch.Deleted:
				if cidr, exists := knownCIDRs[node.Name]; exists {
					onRemove(cidr, "k8s-pod/"+node.Name)
					delete(knownCIDRs, node.Name)
				}
			}
		}

		// Watch channel closed - re-list to get fresh resource version
		// (handles 410 Gone).
		if ctx.Err() != nil {
			return ctx.Err()
		}

		nodeList, err = kw.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("re-list nodes: %w", err)
		}
		resourceVersion = nodeList.ResourceVersion
	}
}

func (kw *KubeWatcher) watchServiceCIDRs(
	ctx context.Context,
	onAdd func(cidr, name string),
	onRemove func(cidr, name string),
) error {
	// Try ServiceCIDR API (k8s 1.31+ beta, 1.33+ GA).
	cidrs, err := kw.discoverServiceCIDRsFromAPI(ctx)
	if err == nil {
		for _, cidr := range cidrs {
			onAdd(cidr, "k8s-service")
		}
		return kw.watchServiceCIDRResource(ctx, onAdd, onRemove)
	}

	// ServiceCIDR API not available (k8s < 1.31). We can't reliably
	// discover the service CIDR without it - skip service routing.
	fmt.Println("ServiceCIDR API not available, skipping service CIDR routing")
	<-ctx.Done()
	return ctx.Err()
}

// discoverServiceCIDRsFromAPI tries the ServiceCIDR resource API.
func (kw *KubeWatcher) discoverServiceCIDRsFromAPI(ctx context.Context) ([]string, error) {
	// ServiceCIDR is in networking.k8s.io/v1 (GA in 1.33+) or v1beta1 (1.31-1.32).
	// Use raw REST client since the typed client may not include ServiceCIDR
	// depending on the client-go version.
	data, err := kw.clientset.RESTClient().
		Get().
		AbsPath("/apis/networking.k8s.io/v1/servicecidrs").
		DoRaw(ctx)
	if err != nil {
		// Try beta.
		data, err = kw.clientset.RESTClient().
			Get().
			AbsPath("/apis/networking.k8s.io/v1beta1/servicecidrs").
			DoRaw(ctx)
		if err != nil {
			return nil, fmt.Errorf("ServiceCIDR API not available: %w", err)
		}
	}

	var result serviceCIDRList
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse ServiceCIDR response: %w", err)
	}

	var cidrs []string
	for _, item := range result.Items {
		for _, cidr := range item.Spec.CIDRs {
			if isIPv4CIDR(cidr) {
				cidrs = append(cidrs, cidr)
			}
		}
	}

	if len(cidrs) == 0 {
		return nil, fmt.Errorf("no ServiceCIDRs found")
	}

	return cidrs, nil
}

// watchServiceCIDRResource watches ServiceCIDR resources for changes.
func (kw *KubeWatcher) watchServiceCIDRResource(
	ctx context.Context,
	onAdd func(cidr, name string),
	onRemove func(cidr, name string),
) error {
	knownCIDRs := map[string][]string{} // resource name -> CIDRs

	// Seed known CIDRs from initial list.
	data, err := kw.clientset.RESTClient().
		Get().
		AbsPath("/apis/networking.k8s.io/v1/servicecidrs").
		DoRaw(ctx)
	if err != nil {
		data, err = kw.clientset.RESTClient().
			Get().
			AbsPath("/apis/networking.k8s.io/v1beta1/servicecidrs").
			DoRaw(ctx)
		if err != nil {
			<-ctx.Done()
			return ctx.Err()
		}
	}

	var list serviceCIDRList
	if err := json.Unmarshal(data, &list); err != nil {
		<-ctx.Done()
		return ctx.Err()
	}
	for _, item := range list.Items {
		var v4 []string
		for _, cidr := range item.Spec.CIDRs {
			if isIPv4CIDR(cidr) {
				v4 = append(v4, cidr)
			}
		}
		if len(v4) > 0 {
			knownCIDRs[item.Metadata.Name] = v4
		}
	}

	// Watch for changes. Use the raw API since typed watchers may not
	// support ServiceCIDR.
	apiPath := "/apis/networking.k8s.io/v1/servicecidrs"
	resourceVersion := list.Metadata.ResourceVersion

	for {
		req := kw.clientset.RESTClient().
			Get().
			AbsPath(apiPath).
			Param("watch", "true").
			Param("resourceVersion", resourceVersion)

		body, err := req.Stream(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// API might not be available, just wait.
			<-ctx.Done()
			return ctx.Err()
		}

		decoder := json.NewDecoder(body)
		for {
			var event serviceCIDRWatchEvent
			if err := decoder.Decode(&event); err != nil {
				_ = body.Close()
				break
			}

			switch event.Type {
			case "ADDED", "MODIFIED":
				oldCIDRs := knownCIDRs[event.Object.Metadata.Name]
				var newCIDRs []string
				for _, cidr := range event.Object.Spec.CIDRs {
					if isIPv4CIDR(cidr) {
						newCIDRs = append(newCIDRs, cidr)
					}
				}

				// Remove old CIDRs that are no longer present.
				for _, old := range oldCIDRs {
					found := false
					for _, n := range newCIDRs {
						if old == n {
							found = true
							break
						}
					}
					if !found {
						onRemove(old, "k8s-service")
					}
				}

				// Add new CIDRs.
				for _, n := range newCIDRs {
					found := false
					for _, old := range oldCIDRs {
						if n == old {
							found = true
							break
						}
					}
					if !found {
						onAdd(n, "k8s-service")
					}
				}

				knownCIDRs[event.Object.Metadata.Name] = newCIDRs

			case "DELETED":
				for _, cidr := range knownCIDRs[event.Object.Metadata.Name] {
					onRemove(cidr, "k8s-service")
				}
				delete(knownCIDRs, event.Object.Metadata.Name)
			}
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Re-list on stream close (410 Gone or disconnect).
		data, err = kw.clientset.RESTClient().
			Get().
			AbsPath(apiPath).
			DoRaw(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			<-ctx.Done()
			return ctx.Err()
		}
		if err := json.Unmarshal(data, &list); err != nil {
			<-ctx.Done()
			return ctx.Err()
		}
		resourceVersion = list.Metadata.ResourceVersion
	}
}

// isLocalhostAddr returns true if the given host (IP or hostname) resolves
// to a loopback address. Handles cases like Docker Desktop's
// "kubernetes.docker.internal" which resolves to 127.0.0.1.
func isLocalhostAddr(host string) bool {
	addrs, err := net.LookupHost(host)
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil && ip.IsLoopback() {
			return true
		}
	}
	return false
}

// Minimal types for ServiceCIDR API responses.

type serviceCIDRList struct {
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Items []serviceCIDR `json:"items"`
}

type serviceCIDR struct {
	Metadata struct {
		Name            string `json:"name"`
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Spec struct {
		CIDRs []string `json:"cidrs"`
	} `json:"spec"`
}

type serviceCIDRWatchEvent struct {
	Type   string      `json:"type"`
	Object serviceCIDR `json:"object"`
}
