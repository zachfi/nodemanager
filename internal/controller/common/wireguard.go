package common

import (
	"context"
	"strconv"
	"strings"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/handler"
)

// collectWireGuardInterfaces runs `wg show all dump` and parses the
// tab-separated output to extract each interface's public key and listen port.
//
// The dump format emits one line per interface followed by one line per peer.
// Interface lines have 5 tab-separated fields:
//
//	<iface>  <private-key>  <public-key>  <listen-port>  <fwmark>
//
// Peer lines have 9 fields. We distinguish them by field count.
// Returns nil if wg is unavailable or no interfaces are configured.
func collectWireGuardInterfaces(ctx context.Context, exec handler.ExecHandler) []commonv1.WireGuardInterface {
	out, _, err := exec.RunCommand(ctx, "wg", "show", "all", "dump")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}

	var ifaces []commonv1.WireGuardInterface
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		// Interface lines: iface, private-key, public-key, listen-port, fwmark
		if len(fields) != 5 {
			continue
		}
		port, _ := strconv.Atoi(fields[3])
		ifaces = append(ifaces, commonv1.WireGuardInterface{
			Name:       fields[0],
			PublicKey:  fields[2],
			ListenPort: port,
		})
	}
	return ifaces
}
