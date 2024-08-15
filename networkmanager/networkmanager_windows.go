//go:build windows

package networkmanager

import (
	"bytes"
	"fmt"
	"os/exec"

	"github.com/docker/docker/api/types"
)

type NetworkManager struct {
	DockerNetworks map[string]types.NetworkResource
}

func New() NetworkManager {
	return NetworkManager{
		DockerNetworks: map[string]types.NetworkResource{},
	}
}

// Set the point-to-point IP address configuration on a network interface.
func (manager *NetworkManager) SetInterfaceAddress(ip string, peerIp string, iface string) (string, string, error) {

	cmd := exec.Command("netsh", "interface", "ipv4", "set", "address", "name=\""+iface+"\"", "static", ip, "255.255.255.255", peerIp, "gwmetric=100")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

// Add a route to the routing table.
func (manager *NetworkManager) AddRoute(net string, iface string) (string, string, error) {

	cmd := exec.Command("netsh", "interface", "ipv4", "add", "route", net, iface)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

// Delete a route from the routing table.
func (manager *NetworkManager) DeleteRoute(net string) (string, string, error) {

	cmd := exec.Command("netsh", "interface", "ipv4", "delete", "route", net)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

func (manager *NetworkManager) ProcessDockerNetworkCreate(network types.NetworkResource, iface string) {
	manager.DockerNetworks[network.ID] = network

	for _, config := range network.IPAM.Config {
		if network.Scope == "local" {
			fmt.Printf("Adding route for %s -> %s (%s)\n", config.Subnet, iface, network.Name)

			_, stderr, err := manager.AddRoute(config.Subnet, iface)

			if err != nil {
				fmt.Printf("Failed to add route: %v. %v\n", err, stderr)
			}
		}
	}
}

func (manager *NetworkManager) ProcessDockerNetworkDestroy(network types.NetworkResource) {
	for _, config := range network.IPAM.Config {
		if network.Scope == "local" {
			fmt.Printf("Deleting route for %s (%s)\n", config.Subnet, network.Name)

			_, stderr, err := manager.DeleteRoute(config.Subnet)

			if err != nil {
				fmt.Printf("Failed to delete route: %v. %v\n", err, stderr)
			}
		}
	}
	delete(manager.DockerNetworks, network.ID)
}
