package networkmanager

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"

	networktypes "github.com/docker/docker/api/types/network"
)

type NetworkManager struct {
	DockerNetworks map[string]networktypes.Inspect
}

func New() NetworkManager {
	return NetworkManager{
		DockerNetworks: map[string]networktypes.Inspect{},
	}
}

// SetInterfaceAddress sets the point-to-point IP address configuration on a network interface.
func (manager *NetworkManager) SetInterfaceAddress(ip string, peerIp string, iface string) (string, string, error) {

	cmd := exec.Command("ifconfig", iface, "inet", fmt.Sprintf("%s/32", ip), peerIp)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

// AddRoute adds a route to the macOS routing table.
func (manager *NetworkManager) AddRoute(net string, iface string) (string, string, error) {

	cmd := exec.Command("route", "-q", "-n", "add", "-inet", net, "-interface", iface)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

// DeleteRoute deletes a route from the macOS routing table.
func (manager *NetworkManager) DeleteRoute(net string) (string, string, error) {

	cmd := exec.Command("route", "-q", "-n", "delete", "-inet", net)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

func (manager *NetworkManager) ProcessDockerNetworkCreate(network networktypes.Inspect, iface string) {
	manager.DockerNetworks[network.ID] = network

	for _, config := range network.IPAM.Config {
		if network.Scope == "local" {
			fmt.Printf("Adding route for %s -> %s (%s)\n", config.Subnet, iface, network.Name)

			_, stderr, err := manager.AddRoute(config.Subnet, iface)

			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "Failed to add route: %v. %v\n", err, stderr)
			}
		}
	}
}

func (manager *NetworkManager) ProcessDockerNetworkDestroy(network networktypes.Inspect) {
	for _, config := range network.IPAM.Config {
		if network.Scope == "local" {
			fmt.Printf("Deleting route for %s (%s)\n", config.Subnet, network.Name)

			_, stderr, err := manager.DeleteRoute(config.Subnet)

			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "Failed to delete route: %v. %v\n", err, stderr)
			}
		}
	}
	delete(manager.DockerNetworks, network.ID)
}
