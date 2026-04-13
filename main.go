//go:build darwin

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/docker/docker/client"
	dcontext "github.com/docker/go-sdk/context"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/ipc"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/chipmk/docker-mac-net-connect/consoleuser"
	"github.com/chipmk/docker-mac-net-connect/networkmanager"
	"github.com/chipmk/docker-mac-net-connect/networkwatcher"
	"github.com/chipmk/docker-mac-net-connect/version"
)

const (
	ExitSetupSuccess = 0
	ExitSetupFailed  = 1
)

const (
	ENV_WG_TUN_FD             = "WG_TUN_FD"
	ENV_WG_UAPI_FD            = "WG_UAPI_FD"
	ENV_WG_PROCESS_FOREGROUND = "WG_PROCESS_FOREGROUND"
)

func main() {
	logLevel := func() int {
		switch os.Getenv("LOG_LEVEL") {
		case "verbose", "debug":
			return device.LogLevelVerbose
		case "error":
			return device.LogLevelError
		case "silent":
			return device.LogLevelSilent
		}
		return device.LogLevelVerbose
	}()

	fmt.Printf("docker-mac-net-connect version '%s'\n", version.Version)

	mainTun, err := tun.CreateTUN("utun", device.DefaultMTU)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create TUN device: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	interfaceName, err := mainTun.Name()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get TUN device name: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	logger := device.NewLogger(
		logLevel,
		fmt.Sprintf("(%s) ", interfaceName),
	)

	fileUAPI, err := ipc.UAPIOpen(interfaceName)

	if err != nil {
		logger.Errorf("UAPI listen error: %v", err)
		os.Exit(ExitSetupFailed)
	}

	mainDevice := device.NewDevice(mainTun, conn.NewDefaultBind(), logger)

	logger.Verbosef("Device started")

	errs := make(chan error)
	term := make(chan os.Signal, 1)

	uapi, err := ipc.UAPIListen(interfaceName, fileUAPI)
	if err != nil {
		logger.Errorf("Failed to listen on UAPI socket: %v", err)
		os.Exit(ExitSetupFailed)
	}

	go func() {
		for {
			uapiConn, err := uapi.Accept()
			if err != nil {
				errs <- err
				return
			}
			go mainDevice.IpcHandle(uapiConn)
		}
	}()

	logger.Verbosef("UAPI listener started")

	// Wireguard configuration

	hostPeerIp := "10.33.33.1"
	vmPeerIp := "10.33.33.2"

	c, err := wgctrl.New()
	if err != nil {
		logger.Errorf("Failed to create new wgctrl client: %v", err)
		os.Exit(ExitSetupFailed)
	}

	defer func() { _ = c.Close() }()

	hostPrivateKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		logger.Errorf("Failed to generate host private key: %v", err)
		os.Exit(ExitSetupFailed)
	}

	vmPrivateKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		logger.Errorf("Failed to generate VM private key: %v", err)
		os.Exit(ExitSetupFailed)
	}

	_, wildcardIpNet, err := net.ParseCIDR("0.0.0.0/0")
	if err != nil {
		logger.Errorf("Failed to parse wildcard CIDR: %v", err)
		os.Exit(ExitSetupFailed)
	}

	_, vmIpNet, err := net.ParseCIDR(vmPeerIp + "/32")
	if err != nil {
		logger.Errorf("Failed to parse VM peer CIDR: %v", err)
		os.Exit(ExitSetupFailed)
	}

	peer := wgtypes.PeerConfig{
		PublicKey: vmPrivateKey.PublicKey(),
		AllowedIPs: []net.IPNet{
			*wildcardIpNet,
			*vmIpNet,
		},
	}

	// Ephemeral port - the actual port is read back and passed to the setup container.
	port := 0
	err = c.ConfigureDevice(interfaceName, wgtypes.Config{
		ListenPort: &port,
		PrivateKey: &hostPrivateKey,
		Peers:      []wgtypes.PeerConfig{peer},
	})
	if err != nil {
		logger.Errorf("Failed to configure Wireguard device: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	wgDevice, err := c.Device(interfaceName)
	if err != nil {
		logger.Errorf("Failed to read Wireguard device: %v\n", err)
		os.Exit(ExitSetupFailed)
	}
	port = wgDevice.ListenPort
	logger.Verbosef("Listening on port %d\n", port)

	networkManager := networkmanager.New()

	_, stderr, err := networkManager.SetInterfaceAddress(hostPeerIp, vmPeerIp, interfaceName)
	if err != nil {
		logger.Errorf("Failed to set interface address with ifconfig: %v. %v", err, stderr)
		os.Exit(ExitSetupFailed)
	}

	logger.Verbosef("Interface %s created\n", interfaceName)

	// Resolve console user's home directory for config file lookups
	// when running as root (e.g. via launchd).
	homeDir, err := consoleuser.HomeDir()
	if err != nil {
		logger.Verbosef("Failed to resolve console user home: %v\n", err)
	}

	// Set DOCKER_CONFIG so the context resolver can find it.
	if os.Getenv("DOCKER_CONFIG") == "" && homeDir != "" {
		dockerConfig := filepath.Join(homeDir, ".docker")
		if err := os.Setenv("DOCKER_CONFIG", dockerConfig); err != nil {
			logger.Verbosef("Failed to set DOCKER_CONFIG: %v\n", err)
		} else {
			logger.Verbosef("Set DOCKER_CONFIG to %s\n", dockerConfig)
		}
	}

	var hostOpt client.Opt
	dockerHost, err := dcontext.CurrentDockerHost()
	if err != nil {
		logger.Verbosef("Failed to resolve Docker host from context: %v, falling back to env/default\n", err)
		hostOpt = client.FromEnv
	} else {
		logger.Verbosef("Using Docker host: %s\n", dockerHost)
		hostOpt = client.WithHost(dockerHost)
	}

	cli, err := client.NewClientWithOpts(hostOpt, client.WithAPIVersionNegotiation())
	if err != nil {
		logger.Errorf("Failed to create Docker client: %v", err)
		os.Exit(ExitSetupFailed)
	}

	logger.Verbosef("Wireguard server listening\n")

	ctx := context.Background()

	addRoute := func(cidr, name string) {
		fmt.Printf("Adding route for %s -> %s (%s)\n", cidr, interfaceName, name)
		_, stderr, err := networkManager.AddRoute(cidr, interfaceName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to add route: %v. %v\n", err, stderr)
		}
	}

	removeRoute := func(cidr, name string) {
		fmt.Printf("Deleting route for %s (%s)\n", cidr, name)
		_, stderr, err := networkManager.DeleteRoute(cidr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to delete route: %v. %v\n", err, stderr)
		}
	}

	go func() {
		for {
			// -- Session start --
			// Each iteration is a full Docker Desktop session. If Docker
			// restarts, everything in the VM is gone (WireGuard interface,
			// iptables rules, k8s API server), so we re-initialize everything.

			sessionCtx, cancelSession := context.WithCancel(ctx)

			logger.Verbosef("Setting up Wireguard on Docker Desktop VM\n")

			vmRoutes, err := networkwatcher.SetupVM(sessionCtx, cli, port, hostPeerIp, vmPeerIp, hostPrivateKey, vmPrivateKey)
			if err != nil {
				logger.Errorf("Failed to setup VM: %v", err)
				cancelSession()
				time.Sleep(5 * time.Second)
				continue
			}

			// Discover and route existing Docker networks.
			err = networkwatcher.ListNetworks(sessionCtx, cli, addRoute)
			if err != nil {
				logger.Errorf("Failed to list Docker networks: %v", err)
				cancelSession()
				time.Sleep(5 * time.Second)
				continue
			}

			// Start k8s watcher in background (scoped to this session).
			// Retries independently - k8s can start/stop separately from Docker.
			// Pin the kube context now so it stays coupled to this Docker session
			// even if the user switches kube contexts later.
			if homeDir != "" {
				if kubeContext := networkwatcher.ResolveKubeContext(homeDir); kubeContext != "" {
					go networkwatcher.RunKubeWatcher(sessionCtx, homeDir, kubeContext, vmRoutes, addRoute, removeRoute)
				} else {
					fmt.Println("No kubeconfig context set, skipping Kubernetes watcher")
				}
			}

			// Watch Docker events (blocks until disconnect).
			logger.Verbosef("Watching Docker events\n")

			err = networkwatcher.WatchEvents(sessionCtx, cli, addRoute, removeRoute)
			if err != nil {
				logger.Errorf("Docker watch error: %v", err)
			}

			// -- Session over --
			cancelSession()
			time.Sleep(5 * time.Second)
		}
	}()

	// Wait for program to terminate

	signal.Notify(term, syscall.SIGTERM)
	signal.Notify(term, os.Interrupt)

	select {
	case <-term:
	case <-errs:
	case <-mainDevice.Wait():
	}

	// Clean up

	_ = uapi.Close()
	mainDevice.Close()

	logger.Verbosef("Shutting down\n")
}
