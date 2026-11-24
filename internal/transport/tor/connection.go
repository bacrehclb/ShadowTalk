package tor

import (
	"encoding/base64"
	"encoding/hex"
	"net/netip"
	"strings"
	"unicode"
)

// Connection allows only the settings owned by ShadowTalk. Arbitrary torrc
// directives cannot override its control authentication or onion-only SOCKS.
type Connection struct {
	Mode           string // empty or "direct"; "obfs4" requires a binary and bridges
	LyrebirdBinary string
	Bridges        []string
}

func (c Connection) Validate() error {
	switch c.Mode {
	case "", "direct":
		if c.LyrebirdBinary != "" || len(c.Bridges) != 0 {
			return ErrInvalidConnection
		}
	case "obfs4":
		if !validBinaryName(c.LyrebirdBinary) || len(c.Bridges) == 0 || len(c.Bridges) > 16 {
			return ErrInvalidConnection
		}
		seen := make(map[string]bool)
		for _, bridge := range c.Bridges {
			if !validBridge(bridge) || seen[bridge] {
				return ErrInvalidConnection
			}
			seen[bridge] = true
		}
	default:
		return ErrInvalidConnection
	}
	return nil
}

func validBinaryName(path string) bool {
	return path != "" && !strings.ContainsFunc(path, func(r rune) bool { return unicode.IsControl(r) })
}

func validBridge(line string) bool {
	if len(line) > 512 || strings.ContainsFunc(line, func(r rune) bool { return r < 32 || r > 126 }) {
		return false
	}
	fields := strings.Fields(line)
	if len(fields) != 5 || fields[0] != "obfs4" {
		return false
	}
	address, err := netip.ParseAddrPort(fields[1])
	if err != nil || address.Port() == 0 || address.Addr().Zone() != "" || address.Addr().IsUnspecified() || address.Addr().IsMulticast() {
		return false
	}
	fingerprint, err := hex.DecodeString(fields[2])
	if err != nil || len(fingerprint) != 20 {
		return false
	}
	cert, ok := strings.CutPrefix(fields[3], "cert=")
	if !ok {
		return false
	}
	decoded, err := base64.RawStdEncoding.Strict().DecodeString(cert)
	if err != nil || len(decoded) != 52 {
		return false
	}
	return fields[4] == "iat-mode=0" || fields[4] == "iat-mode=1" || fields[4] == "iat-mode=2"
}

func (c Connection) torrc(endpoint string) []byte {
	if c.Mode != "obfs4" {
		return nil
	}
	// endpoint is a validated loopback listener from our managed PT handshake.
	// No executable path, shell quoting or private bridge appears in process argv.
	var out strings.Builder
	out.WriteString("UseBridges 1\nClientTransportPlugin obfs4 socks5 " + endpoint + "\n")
	for _, bridge := range c.Bridges {
		out.WriteString("Bridge " + bridge + "\n")
	}
	return []byte(out.String())
}
