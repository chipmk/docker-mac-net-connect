//go:build darwin

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	dcontext "github.com/docker/go-sdk/context"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/ipc"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/chipmk/docker-mac-net-connect/networkmanager"
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

	// When running as root (e.g. via launchd), the docker config lives under
	// the console user's home directory. Set DOCKER_CONFIG so the context
	// resolver can find it.
	if os.Getenv("DOCKER_CONFIG") == "" {
		consoleUser, err := getConsoleUser()
		if err != nil {
			logger.Verbosef("Failed to get console user: %v\n", err)
		} else {
			u, err := user.Lookup(consoleUser)
			if err != nil {
				logger.Verbosef("Failed to lookup user %s: %v\n", consoleUser, err)
			} else {
				dockerConfig := filepath.Join(u.HomeDir, ".docker")
				if err := os.Setenv("DOCKER_CONFIG", dockerConfig); err != nil {
					logger.Verbosef("Failed to set DOCKER_CONFIG: %v\n", err)
				} else {
					logger.Verbosef("Set DOCKER_CONFIG to %s (console user: %s)\n", dockerConfig, consoleUser)
				}
			}
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

	go func() {
		for {
			logger.Verbosef("Setting up Wireguard on Docker Desktop VM\n")

			err = setupVm(ctx, cli, port, hostPeerIp, vmPeerIp, hostPrivateKey, vmPrivateKey)
			if err != nil {
				logger.Errorf("Failed to setup VM: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}

			networks, err := cli.NetworkList(ctx, network.ListOptions{})
			if err != nil {
				logger.Errorf("Failed to list Docker networks: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}

			for _, network := range networks {
				networkManager.ProcessDockerNetworkCreate(network, interfaceName)
			}

			logger.Verbosef("Watching Docker events\n")

			msgs, errsChan := cli.Events(ctx, events.ListOptions{
				Filters: filters.NewArgs(
					filters.Arg("type", "network"),
					filters.Arg("event", "create"),
					filters.Arg("event", "destroy"),
				),
			})

			for loop := true; loop; {
				select {
				case err := <-errsChan:
					logger.Errorf("Error: %v\n", err)
					loop = false
				case msg := <-msgs:
					// Add routes when new Docker networks are created
					if msg.Type == "network" && msg.Action == "create" {
						loopNetwork, err := cli.NetworkInspect(ctx, msg.Actor.ID, network.InspectOptions{})
						if err != nil {
							logger.Errorf("Failed to inspect new Docker network: %v", err)
							continue
						}

						networkManager.ProcessDockerNetworkCreate(loopNetwork, interfaceName)
						continue
					}

					// Delete routes when Docker networks are destroyed
					if msg.Type == "network" && msg.Action == "destroy" {
						loopNetwork, exists := networkManager.DockerNetworks[msg.Actor.ID]
						if !exists {
							logger.Errorf("Unknown Docker network with ID %s. No routes will be removed.")
							continue
						}

						networkManager.ProcessDockerNetworkDestroy(loopNetwork)
						continue
					}
				}
			}

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

func setupVm(
	ctx context.Context,
	dockerCli *client.Client,
	serverPort int,
	hostPeerIp string,
	vmPeerIp string,
	hostPrivateKey wgtypes.Key,
	vmPrivateKey wgtypes.Key,
) error {
	imageName := fmt.Sprintf("%s:%s", version.SetupImage, version.Version)

	_, err := dockerCli.ImageInspect(ctx, imageName)
	if err != nil {
		fmt.Printf("Image doesn't exist locally. Pulling...\n")

		pullStream, err := dockerCli.ImagePull(ctx, imageName, image.PullOptions{})
		if err != nil {
			return fmt.Errorf("failed to pull setup image: %w", err)
		}

		_, _ = io.Copy(os.Stdout, pullStream)
	}

	resp, err := dockerCli.ContainerCreate(ctx, &container.Config{
		Image: imageName,
		Env: []string{
			"SERVER_PORT=" + strconv.Itoa(serverPort),
			"HOST_PEER_IP=" + hostPeerIp,
			"VM_PEER_IP=" + vmPeerIp,
			"HOST_PUBLIC_KEY=" + hostPrivateKey.PublicKey().String(),
			"VM_PRIVATE_KEY=" + vmPrivateKey.String(),
		},
	}, &container.HostConfig{
		AutoRemove:  true,
		NetworkMode: "host",
		CapAdd:      []string{"NET_ADMIN"},
	}, nil, nil, "wireguard-setup")
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	// Run container to completion
	err = dockerCli.ContainerStart(ctx, resp.ID, container.StartOptions{})
	if err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	if err := func() error {
		reader, err := dockerCli.ContainerLogs(ctx, resp.ID, container.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Follow:     true,
		})
		if err != nil {
			return fmt.Errorf("failed to get logs for container %s: %w", resp.ID, err)
		}

		defer func() { _ = reader.Close() }()

		_, err = stdcopy.StdCopy(os.Stdout, os.Stderr, reader)
		if err != nil {
			return err
		}

		return nil
	}(); err != nil {
		return err
	}

	fmt.Println("Setup container complete")

	return nil
}

// getConsoleUser returns the username of the currently logged-in GUI user
// by checking the owner of /dev/console.
func getConsoleUser() (string, error) {
	info, err := os.Stat("/dev/console")
	if err != nil {
		return "", fmt.Errorf("stat /dev/console: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("unexpected stat type for /dev/console")
	}
	u, err := user.LookupId(strconv.FormatUint(uint64(stat.Uid), 10))
	if err != nil {
		return "", fmt.Errorf("lookup uid %d: %w", stat.Uid, err)
	}
	if u.Username == "root" {
		return "", fmt.Errorf("no console user logged in")
	}
	return u.Username, nil
}
