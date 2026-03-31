package common

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"strconv"
	"strings"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/handler"
)

// generateWireGuardKeyPair generates a Curve25519 keypair using stdlib crypto.
// Returns base64-encoded private and public keys in the same format as wg(8).
func generateWireGuardKeyPair() (privateKey, publicKey string, err error) {
	curve := ecdh.X25519()
	priv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	privB64 := base64.StdEncoding.EncodeToString(priv.Bytes())
	pubB64 := base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())
	return privB64, pubB64, nil
}

// mergeBootstrappedWireGuardKey adds a bootstrapped public key entry for iface
// to liveWG if it is not already present in the live data.  When the interface
// is actually running, live data (which includes the listen port) takes
// precedence, so we leave it unchanged.  Returns liveWG unmodified if pubKey
// is empty.
func mergeBootstrappedWireGuardKey(liveWG []commonv1.WireGuardInterface, iface, pubKey string) []commonv1.WireGuardInterface {
	if pubKey == "" {
		return liveWG
	}
	for _, w := range liveWG {
		if w.Name == iface {
			return liveWG
		}
	}
	return append(liveWG, commonv1.WireGuardInterface{
		Name:      iface,
		PublicKey: pubKey,
	})
}

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
