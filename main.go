//go:build darwin

package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/ipc"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
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

	tun, err := tun.CreateTUN("utun", device.DefaultMTU)
	if err != nil {
		fmt.Errorf("Failed to create TUN device: %v", err)
		os.Exit(ExitSetupFailed)
	}

	interfaceName, err := tun.Name()
	if err != nil {
		fmt.Errorf("Failed to get TUN device name: %v", err)
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

	device := device.NewDevice(tun, conn.NewDefaultBind(), logger)

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
			conn, err := uapi.Accept()
			if err != nil {
				errs <- err
				return
			}
			go device.IpcHandle(conn)
		}
	}()

	logger.Verbosef("UAPI listener started")

	// Wireguard configuration

	c, err := wgctrl.New()
	if err != nil {
		logger.Errorf("Failed to create new wgctrl client: %v", err)
		os.Exit(ExitSetupFailed)
	}

	defer c.Close()

	serverPrivateKey, err := wgtypes.ParseKey("sEjL0NvY8fuHpQkTCYbnItuawe5LBxjqruK6WObmJHg=")
	if err != nil {
		logger.Errorf("Failed to generate server private key: %v", err)
		os.Exit(ExitSetupFailed)
	}
	logger.Verbosef("Server Private Key: %s\n", serverPrivateKey.String())

	peerPrivateKey, err := wgtypes.ParseKey("AIwSWU9veYZ2FvEG+V/sSh3DAKF3SbXCkgUHULUuNWc=")
	if err != nil {
		logger.Errorf("Failed to generate peer private key: %v", err)
		os.Exit(ExitSetupFailed)
	}

	logger.Verbosef("Peer Private Key: %s\n", peerPrivateKey.String())
	logger.Verbosef("Server Public Key: %s\n", serverPrivateKey.PublicKey().String())

	_, wildcardIpNet, err := net.ParseCIDR("0.0.0.0/0")
	if err != nil {
		logger.Errorf("Failed to parse CIDR: %v", err)
		os.Exit(ExitSetupFailed)
	}

	_, peerIpNet, err := net.ParseCIDR("10.33.33.2/32")
	if err != nil {
		logger.Errorf("Failed to parse CIDR: %v", err)
		os.Exit(ExitSetupFailed)
	}

	peer := wgtypes.PeerConfig{
		PublicKey: peerPrivateKey.PublicKey(),
		AllowedIPs: []net.IPNet{
			*wildcardIpNet,
			*peerIpNet,
		},
	}

	port := 3333
	err = c.ConfigureDevice(interfaceName, wgtypes.Config{
		ListenPort: &port,
		PrivateKey: &serverPrivateKey,
		Peers:      []wgtypes.PeerConfig{peer},
	})
	if err != nil {
		fmt.Errorf("Failed to configure Wireguard device: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	cmd := exec.Command("ifconfig", interfaceName, "inet", "10.33.33.1/32", "10.33.33.2")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	if err != nil {
		logger.Errorf("Failed to set interface address with ifconfig: %v. %v", err, out.String())
		os.Exit(ExitSetupFailed)
	}

	logger.Verbosef("Interface %s created\n", interfaceName)

	cmd = exec.Command("route", "-q", "-n", "add", "-host", "10.33.33.2", "-interface", interfaceName)
	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		logger.Errorf("Failed to add route: %v. %v", err, out.String())
		os.Exit(ExitSetupFailed)
	}

	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		logger.Errorf("Failed to create Docker client: %v", err)
		os.Exit(ExitSetupFailed)
	}

	ctx := context.Background()

	networks, err := cli.NetworkList(ctx, types.NetworkListOptions{})
	if err != nil {
		logger.Errorf("Failed to list Docker networks: %v", err)
		os.Exit(ExitSetupFailed)
	}

	for _, network := range networks {
		for _, config := range network.IPAM.Config {
			if network.Scope == "local" {
				logger.Verbosef("Adding route for %s -> %s (%s)\n", config.Subnet, interfaceName, network.Name)

				cmd = exec.Command("route", "-q", "-n", "add", "-inet", config.Subnet, "-interface", interfaceName)
				cmd.Stdout = &out
				err = cmd.Run()
				if err != nil {
					logger.Errorf("Failed to add route: %v. %v", err, out.String())
					os.Exit(ExitSetupFailed)
				}
			}
		}
	}

	logger.Verbosef("Setting up Wireguard on Docker Desktop VM\n")

	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image: "docker-mac-net-connect",
	}, &container.HostConfig{
		AutoRemove:  true,
		NetworkMode: "host",
		CapAdd:      []string{"NET_ADMIN"},
	}, nil, nil, "wireguard-setup")
	if err != nil {
		logger.Errorf("Failed to create container: %v", err)
		os.Exit(ExitSetupFailed)
	}

	err = cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
	if err != nil {
		logger.Errorf("Failed to start container: %v", err)
		os.Exit(ExitSetupFailed)
	}

	logger.Verbosef("Wireguard server listening\n")

	logger.Verbosef("Docker event listening\n")
	msgs, errsChan := cli.Events(ctx, types.EventsOptions{})

	go func() {
		for {
			select {
			case err := <-errsChan:
				logger.Errorf("Error: %v\n", err)
			case msg := <-msgs:
				logger.Verbosef("%v %v: %v\n", msg.Type, msg.Action, msg.From)
			}
		}
	}()

	// wait for program to terminate

	signal.Notify(term, syscall.SIGTERM)
	signal.Notify(term, os.Interrupt)

	select {
	case <-term:
	case <-errs:
	case <-device.Wait():
	}

	// clean up

	uapi.Close()
	device.Close()

	logger.Verbosef("Shutting down\n")
}
