package networkmanager

import (
	"bytes"
	"os/exec"
)

type NetworkManager struct{}

func New() NetworkManager {
	return NetworkManager{}
}

// SetInterfaceAddress sets the point-to-point IP address configuration on a network interface.
func (manager *NetworkManager) SetInterfaceAddress(ip string, peerIp string, iface string) (string, string, error) {
	cmd := exec.Command("ifconfig", iface, "inet", ip+"/32", peerIp)

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
