//go:build darwin

package networkwatcher

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/chipmk/docker-mac-net-connect/version"
)

// SetupVM runs the ephemeral setup container inside the Docker Desktop VM
// to create the WireGuard interface and configure iptables rules.
// Returns any VM routes discovered by the setup container (e.g. cni0 routes
// for k8s pod networking).
func SetupVM(
	ctx context.Context,
	dockerCli *client.Client,
	serverPort int,
	hostPeerIp string,
	vmPeerIp string,
	hostPrivateKey wgtypes.Key,
	vmPrivateKey wgtypes.Key,
) ([]string, error) {
	imageName := fmt.Sprintf("%s:%s", version.SetupImage, version.Version)

	_, err := dockerCli.ImageInspect(ctx, imageName)
	if err != nil {
		fmt.Printf("Image doesn't exist locally. Pulling...\n")

		pullStream, err := dockerCli.ImagePull(ctx, imageName, image.PullOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to pull setup image: %w", err)
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
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	err = dockerCli.ContainerStart(ctx, resp.ID, container.StartOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	var stdoutBuf bytes.Buffer
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

		// Tee stdout so we can both display logs and parse structured output.
		stdoutWriter := io.MultiWriter(os.Stdout, &stdoutBuf)
		_, err = stdcopy.StdCopy(stdoutWriter, os.Stderr, reader)
		if err != nil {
			return err
		}

		return nil
	}(); err != nil {
		return nil, err
	}

	fmt.Println("Setup container complete")

	// Parse VM_ROUTE= lines from the setup container's stdout.
	var vmRoutes []string
	scanner := bufio.NewScanner(&stdoutBuf)
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "VM_ROUTE="); ok {
			vmRoutes = append(vmRoutes, after)
		}
	}

	return vmRoutes, nil
}

// ListNetworks lists existing Docker networks and calls onAdd for each
// local network subnet.
func ListNetworks(
	ctx context.Context,
	cli *client.Client,
	onAdd func(cidr, name string),
) error {
	networks, err := cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list Docker networks: %w", err)
	}

	for _, n := range networks {
		for _, config := range n.IPAM.Config {
			if n.Scope == "local" && isIPv4CIDR(config.Subnet) {
				onAdd(config.Subnet, n.Name)
			}
		}
	}

	return nil
}

// WatchEvents watches Docker network create/destroy events and calls
// onAdd/onRemove for subnet changes. Blocks until the event stream
// disconnects or the context is cancelled.
func WatchEvents(
	ctx context.Context,
	cli *client.Client,
	onAdd func(cidr, name string),
	onRemove func(cidr, name string),
) error {
	// Track networks for destroy events (need cached IPAM config
	// since the network is gone by the time we get the event).
	knownNetworks := map[string]network.Inspect{}

	// Seed known networks from current state.
	networks, err := cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list Docker networks: %w", err)
	}
	for _, n := range networks {
		knownNetworks[n.ID] = n
	}

	msgs, errsChan := cli.Events(ctx, events.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("type", "network"),
			filters.Arg("event", "create"),
			filters.Arg("event", "destroy"),
		),
	})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errsChan:
			return fmt.Errorf("docker event stream error: %w", err)
		case msg := <-msgs:
			if msg.Type == "network" && msg.Action == "create" {
				n, err := cli.NetworkInspect(ctx, msg.Actor.ID, network.InspectOptions{})
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to inspect new Docker network: %v\n", err)
					continue
				}

				knownNetworks[n.ID] = n

				for _, config := range n.IPAM.Config {
					if n.Scope == "local" && isIPv4CIDR(config.Subnet) {
						onAdd(config.Subnet, n.Name)
					}
				}
				continue
			}

			if msg.Type == "network" && msg.Action == "destroy" {
				n, exists := knownNetworks[msg.Actor.ID]
				if !exists {
					fmt.Fprintf(os.Stderr, "Unknown Docker network with ID %s. No routes will be removed.\n", msg.Actor.ID)
					continue
				}

				for _, config := range n.IPAM.Config {
					if n.Scope == "local" && isIPv4CIDR(config.Subnet) {
						onRemove(config.Subnet, n.Name)
					}
				}
				delete(knownNetworks, msg.Actor.ID)
				continue
			}
		}
	}
}
