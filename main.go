//go:build darwin

package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
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
	interfaceName := "utun"

	logLevel := func() int {
		switch os.Getenv("LOG_LEVEL") {
		case "verbose", "debug":
			return device.LogLevelVerbose
		case "error":
			return device.LogLevelError
		case "silent":
			return device.LogLevelSilent
		}
		return device.LogLevelError
	}()

	// open TUN device (or use supplied fd)

	tun, err := func() (tun.Device, error) {
		tunFdStr := os.Getenv(ENV_WG_TUN_FD)
		if tunFdStr == "" {
			return tun.CreateTUN(interfaceName, device.DefaultMTU)
		}

		// construct tun device from supplied fd

		fd, err := strconv.ParseUint(tunFdStr, 10, 32)
		if err != nil {
			return nil, err
		}

		err = syscall.SetNonblock(int(fd), true)
		if err != nil {
			return nil, err
		}

		file := os.NewFile(uintptr(fd), "")
		return tun.CreateTUNFromFile(file, device.DefaultMTU)
	}()

	if err == nil {
		realInterfaceName, err2 := tun.Name()
		if err2 == nil {
			interfaceName = realInterfaceName
		}
	}

	logger := device.NewLogger(
		logLevel,
		fmt.Sprintf("(%s) ", interfaceName),
	)

	if err != nil {
		logger.Errorf("Failed to create TUN device: %v", err)
		os.Exit(ExitSetupFailed)
	}

	// open UAPI file (or use supplied fd)

	fileUAPI, err := func() (*os.File, error) {
		uapiFdStr := os.Getenv(ENV_WG_UAPI_FD)
		if uapiFdStr == "" {
			return ipc.UAPIOpen(interfaceName)
		}

		// use supplied fd

		fd, err := strconv.ParseUint(uapiFdStr, 10, 32)
		if err != nil {
			return nil, err
		}

		return os.NewFile(uintptr(fd), ""), nil
	}()

	if err != nil {
		logger.Errorf("UAPI listen error: %v", err)
		os.Exit(ExitSetupFailed)
		return
	}

	device := device.NewDevice(tun, conn.NewDefaultBind(), logger)

	logger.Verbosef("Device started")

	errs := make(chan error)
	term := make(chan os.Signal, 1)

	uapi, err := ipc.UAPIListen(interfaceName, fileUAPI)
	if err != nil {
		logger.Errorf("Failed to listen on uapi socket: %v", err)
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
		log.Fatalf("failed to open wgctrl: %v", err)
	}
	defer c.Close()

	tunName, err := tun.Name()
	if err != nil {
		log.Fatalf("failed to get tun name: %v", err)
	}

	serverPrivateKey, err := wgtypes.ParseKey("sEjL0NvY8fuHpQkTCYbnItuawe5LBxjqruK6WObmJHg=")
	if err != nil {
		log.Fatalf("failed to generate server private key: %v", err)
	}
	fmt.Printf("Server Private Key: %s\n", serverPrivateKey.String())

	peerPrivateKey, err := wgtypes.ParseKey("AIwSWU9veYZ2FvEG+V/sSh3DAKF3SbXCkgUHULUuNWc=")
	if err != nil {
		log.Fatalf("failed to generate peer private key: %v", err)
	}

	fmt.Printf("Peer Private Key: %s\n", peerPrivateKey.String())
	fmt.Printf("Server Public Key: %s\n", serverPrivateKey.PublicKey().String())

	_, wildcardIpNet, err := net.ParseCIDR("0.0.0.0/0")
	if err != nil {
		fmt.Printf("could not parse IPNet: %v\n", err)
	}
	_, peerIpNet, err := net.ParseCIDR("10.33.33.2/32")
	if err != nil {
		fmt.Printf("could not parse IPNet: %v\n", err)
	}

	peer := wgtypes.PeerConfig{
		PublicKey: peerPrivateKey.PublicKey(),
		AllowedIPs: []net.IPNet{
			*wildcardIpNet,
			*peerIpNet,
		},
	}

	port := 3333
	c.ConfigureDevice(tunName, wgtypes.Config{
		ListenPort: &port,
		PrivateKey: &serverPrivateKey,
		Peers:      []wgtypes.PeerConfig{peer},
	})

	cmd := exec.Command("ifconfig", tunName, "inet", "10.33.33.1/32", "10.33.33.2")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	if err != nil {
		logger.Errorf("ifconfig error: %v. %v", err, out.String())
		os.Exit(ExitSetupFailed)
		return
	}

	fmt.Printf("interface %s created\n", tunName)

	cmd = exec.Command("route", "-q", "-n", "add", "-host", "10.33.33.2", "-interface", tunName)
	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		logger.Errorf("route add error: %v. %v", err, out.String())
		os.Exit(ExitSetupFailed)
		return
	}

	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	networks, err := cli.NetworkList(ctx, types.NetworkListOptions{})
	if err != nil {
		panic(err)
	}

	for _, network := range networks {
		for _, config := range network.IPAM.Config {
			if network.Scope == "local" {
				fmt.Printf("adding route for %s -> %s (%s)\n", config.Subnet, tunName, network.Name)

				cmd = exec.Command("route", "-q", "-n", "add", "-inet", config.Subnet, "-interface", tunName)
				cmd.Stdout = &out
				err = cmd.Run()
				if err != nil {
					logger.Errorf("route add error: %v. %v", err, out.String())
					os.Exit(ExitSetupFailed)
					return
				}
			}
		}
	}

	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image: "docker-mac-net-connect",
	}, &container.HostConfig{
		AutoRemove:  true,
		NetworkMode: "host",
		CapAdd:      []string{"NET_ADMIN"},
	}, nil, nil, "wireguard-setup")
	if err != nil {
		panic(err)
	}

	fmt.Println("setting up wireguard on docker desktop vm")

	if err := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		panic(err)
	}

	fmt.Println("wireguard server listening")

	fmt.Println("docker event listening")
	msgs, errsChan := cli.Events(ctx, types.EventsOptions{})

	go func() {
		for {
			select {
			case err := <-errsChan:
				fmt.Printf("Error: %v\n", err)
			case msg := <-msgs:
				fmt.Printf("%v %v: %v\n", msg.Type, msg.Action, msg.From)
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

	logger.Verbosef("Shutting down")
}
