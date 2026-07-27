//go:build codex_upgrade_shim

package install

import "fmt"

// PrepareUpgradeHelperShim is unreachable from the standalone upgrade shim.
// Keeping a tag-specific stub lets the transaction package compile without
// recursively embedding an older copy of the shim binary into itself.
func PrepareUpgradeHelperShim(string, string) (string, error) {
	return "", fmt.Errorf("nested upgrade shim preparation is unavailable")
}
