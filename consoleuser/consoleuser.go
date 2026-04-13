//go:build darwin

package consoleuser

import (
	"fmt"
	"os/user"
	"strconv"
	"syscall"

	"os"
)

// Username returns the username of the currently logged-in GUI user
// by checking the owner of /dev/console.
func Username() (string, error) {
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

// HomeDir returns the home directory of the currently logged-in GUI user.
// This is useful when running as root (e.g. via launchd) where ~ resolves
// to /var/root instead of the actual user's home.
func HomeDir() (string, error) {
	username, err := Username()
	if err != nil {
		return "", err
	}
	u, err := user.Lookup(username)
	if err != nil {
		return "", fmt.Errorf("lookup user %s: %w", username, err)
	}
	return u.HomeDir, nil
}
