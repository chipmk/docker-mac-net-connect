package networkmanager

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

type NetworkManager struct {
	DockerNetworks map[string]network.Inspect
}

func New() NetworkManager {
	return NetworkManager{
		DockerNetworks: map[string]network.Inspect{},
	}
}

// Set the point-to-point IP address configuration on a network interface.
func (manager *NetworkManager) SetInterfaceAddress(ip string, peerIp string, iface string) (string, string, error) {

	cmd := exec.Command("ifconfig", iface, "inet", ip+"/32", peerIp)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

// Add a route to the macOS routing table.
func (manager *NetworkManager) AddRoute(net string, iface string) (string, string, error) {

	cmd := exec.Command("route", "-q", "-n", "add", "-inet", net, "-interface", iface)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

// Delete a route from the macOS routing table.
func (manager *NetworkManager) DeleteRoute(net string) (string, string, error) {

	cmd := exec.Command("route", "-q", "-n", "delete", "-inet", net)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

func (manager *NetworkManager) ProcessDockerNetworkCreate(network network.Inspect, iface string) {
	manager.DockerNetworks[network.ID] = network

	for _, config := range network.IPAM.Config {
		if network.Scope == "local" && config.Subnet != "" {
			// Parse the subnet to check if it's IPv4
			_, ipNet, err := net.ParseCIDR(config.Subnet)
			if err != nil {
				fmt.Printf("Failed to parse CIDR %s: %v\n", config.Subnet, err)
				continue
			}

			// Only process IPv4 CIDRs, skip IPv6
			if ipNet.IP.To4() != nil {
				fmt.Printf("Adding route for %s -> %s (%s)\n", config.Subnet, iface, network.Name)

				_, stderr, err := manager.AddRoute(config.Subnet, iface)

				if err != nil {
					fmt.Printf("Failed to add route: %v. %v\n", err, stderr)
				}
			}
		}
	}
}

func (manager *NetworkManager) ProcessDockerNetworkDestroy(network network.Inspect) {
	for _, config := range network.IPAM.Config {
		if network.Scope == "local" && config.Subnet != "" {
			// Parse the subnet to check if it's IPv4
			_, ipNet, err := net.ParseCIDR(config.Subnet)
			if err != nil {
				fmt.Printf("Failed to parse CIDR %s: %v\n", config.Subnet, err)
				continue
			}

			// Only process IPv4 CIDRs, skip IPv6
			if ipNet.IP.To4() != nil {
				fmt.Printf("Deleting route for %s (%s)\n", config.Subnet, network.Name)

				_, stderr, err := manager.DeleteRoute(config.Subnet)

				if err != nil {
					fmt.Printf("Failed to delete route: %v. %v\n", err, stderr)
				}
			}
		}
	}
	delete(manager.DockerNetworks, network.ID)
}

func (manager *NetworkManager) GetDockerCIDRs(ctx context.Context, cli *client.Client) []string {
	var cidrs []string

	networks, err := cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		fmt.Printf("Failed to list Docker networks: %v\n", err)
		return cidrs
	}

	for _, dockerNet := range networks {
		if dockerNet.Scope == "local" {
			for _, config := range dockerNet.IPAM.Config {
				if config.Subnet != "" {
					// Parse the subnet to check if it's IPv4
					_, ipNet, err := net.ParseCIDR(config.Subnet)
					if err != nil {
						fmt.Printf("Failed to parse CIDR %s: %v\n", config.Subnet, err)
						continue
					}

					// Only include IPv4 CIDRs, exclude IPv6
					if ipNet.IP.To4() != nil {
						cidrs = append(cidrs, config.Subnet)
					}
				}
			}
		}
	}
	return cidrs
}
