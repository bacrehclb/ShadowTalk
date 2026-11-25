package tor

// A real subprocess stands in for Tor in offline tests. It speaks SAFECOOKIE,
// ADD_ONION and SOCKS5 on loopback, so tests exercise process ownership, actual
// sockets and cancellation without contacting the public Tor network.
import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tored25519 "github.com/cretz/bine/torutil/ed25519"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("SHADOWTALK_FAKE_TOR"); mode != "" {
		var err error
		if len(os.Args) == 1 && os.Getenv("TOR_PT_CLIENT_TRANSPORTS") == "obfs4" {
			err = runFakePT(mode)
		} else {
			err = runFakeTor(mode)
		}
		if err != nil {
			// No raw control commands or secret key bytes in helper diagnostics.
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runFakeTor(mode string) error {
	if mode == "exit" {
		return fmt.Errorf("early exit")
	}
	if mode == "stall-port" {
		time.Sleep(time.Minute)
		return nil
	}
	args := map[string]string{}
	for i := 1; i+1 < len(os.Args); i += 2 {
		args[os.Args[i]] = os.Args[i+1]
	}
	if args["--DisableNetwork"] != "1" || args["--ClientOnly"] != "1" ||
		args["--SocksPort"] != "127.0.0.1:auto OnionTrafficOnly IsolateSOCKSAuth" {
		return fmt.Errorf("unsafe process configuration")
	}
	controller, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer controller.Close()
	socks, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer socks.Close()
	go func() {
		for {
			conn, err := socks.Accept()
			if err != nil {
				return
			}
			go serveFakeSOCKS(conn, mode)
		}
	}()
	cookie := make([]byte, 32)
	_, _ = rand.Read(cookie)
	if err := os.WriteFile(args["--CookieAuthFile"], cookie, 0600); err != nil {
		return err
	}
	if err := publishFakeControlPort(args["--ControlPortWriteToFile"], controller.Addr().String()); err != nil {
		return err
	}
	conn, err := controller.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReader(conn)
	var proof []byte
	var serviceID string
	authenticated, network, subscribed := false, false, false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if mode == "ignore-close" {
				time.Sleep(time.Minute)
			}
			return nil
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return fmt.Errorf("empty command")
		}
		switch fields[0] {
		case "PROTOCOLINFO":
			methods, cookiePath := "COOKIE,SAFECOOKIE", args["--CookieAuthFile"]
			if mode == "null-auth" {
				methods = "NULL,SAFECOOKIE"
			}
			if mode == "foreign-cookie" {
				cookiePath += "-other"
			}
			fmt.Fprintf(conn, "250-PROTOCOLINFO 1\r\n250-AUTH METHODS=%s COOKIEFILE=%q\r\n250-VERSION Tor=\"0.4.9.16\"\r\n250 OK\r\n", methods, cookiePath)
		case "AUTHCHALLENGE":
			if mode == "stall-auth" {
				_, _ = io.Copy(io.Discard, conn)
				return nil
			}
			clientNonce, err := hex.DecodeString(fields[2])
			if err != nil || len(clientNonce) != 32 {
				return fmt.Errorf("invalid challenge")
			}
			nonce := make([]byte, 32)
			_, _ = rand.Read(nonce)
			message := append(append(bytes.Clone(cookie), clientNonce...), nonce...)
			serverHash := safeCookieMAC("server-to-controller", message)
			proof = safeCookieMAC("controller-to-server", message)
			if mode == "bad-auth" {
				serverHash[0] ^= 1
			}
			fmt.Fprintf(conn, "250 AUTHCHALLENGE SERVERHASH=%X SERVERNONCE=%X\r\n", serverHash, nonce)
		case "AUTHENTICATE":
			got, _ := hex.DecodeString(fields[1])
			if proof == nil || !hmac.Equal(got, proof) {
				return fmt.Errorf("bad client proof")
			}
			authenticated = true
			fmt.Fprint(conn, "250 OK\r\n")
		default:
			if !authenticated {
				return fmt.Errorf("unauthenticated operation")
			}
			switch fields[0] {
			case "TAKEOWNERSHIP":
				fmt.Fprint(conn, "250 OK\r\n")
			case "SETEVENTS":
				subscribed = len(fields) > 1
				fmt.Fprint(conn, "250 OK\r\n")
			case "ADD_ONION":
				if !subscribed || len(fields) != 4 || fields[2] != "Flags=DiscardPK" || !strings.HasPrefix(fields[3], "Port=443,127.0.0.1:") {
					return fmt.Errorf("bad onion request")
				}
				if mode == "fail-onion" {
					// Simulate a server echoing secret input; it must not escape in errors.
					fmt.Fprintf(conn, "512 SECRET_ECHO %s\r\n", fields[1])
					continue
				}
				expanded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(fields[1], "ED25519-V3:"))
				if err != nil || len(expanded) != 64 {
					return fmt.Errorf("bad onion key")
				}
				public := tored25519.PrivateKey(expanded).PublicKey()
				address := onionAddress(ed25519.PublicKey(public))
				serviceID = strings.TrimSuffix(address, ".onion")
				if mode == "mismatch-onion" {
					serviceID = "wrong"
				}
				target := strings.TrimPrefix(fields[3], "Port=443,")
				if err := os.WriteFile(filepath.Join(os.Getenv("SHADOWTALK_FAKE_REGISTRY"), address), []byte(target), 0600); err != nil {
					return err
				}
				fmt.Fprintf(conn, "250-ServiceID=%s\r\n250 OK\r\n", serviceID)
			case "SETCONF":
				network = fields[1] == "DisableNetwork=0"
				fmt.Fprint(conn, "250 OK\r\n")
			case "GETINFO":
				switch fields[1] {
				case "net/listeners/socks":
					address := socks.Addr().String()
					if !network {
						address = ""
					}
					if mode == "remote-socks" {
						address = "192.0.2.1:9050"
					}
					fmt.Fprintf(conn, "250-net/listeners/socks=%q\r\n250 OK\r\n", address)
				case "status/bootstrap-phase":
					if mode == "bootstrap-warning" {
						fmt.Fprint(conn, "250-status/bootstrap-phase=WARN BOOTSTRAP PROGRESS=14 TAG=handshake SUMMARY=\"PRIVATE PROGRESS=100\" REASON=TIMEOUT WARNING=\"PRIVATE bridge cert\"\r\n250 OK\r\n")
						continue
					}
					if !network {
						return fmt.Errorf("network not enabled")
					}
					if subscribed && mode != "no-publish" {
						id := serviceID
						if mode == "wrong-publication" {
							id = "unrelated-service"
						}
						fmt.Fprintf(conn, "650 HS_DESC UPLOADED %s NO_AUTH example UNKNOWN\r\n", id)
					}
					fmt.Fprint(conn, "250-status/bootstrap-phase=NOTICE BOOTSTRAP PROGRESS=100 TAG=done\r\n250 OK\r\n")
				default:
					return fmt.Errorf("unknown query")
				}
			default:
				return fmt.Errorf("unknown command")
			}
		}
	}
}

func publishFakeControlPort(path, address string) error {
	// Like Tor, finish writing a separate file before publishing the port path.
	// The parent polls that path and must never observe an empty/partial record.
	pending := path + ".tmp"
	if err := os.WriteFile(pending, []byte("PORT="+address+"\n"), 0600); err != nil {
		return err
	}
	return os.Rename(pending, path)
}

func safeCookieMAC(direction string, message []byte) []byte {
	mac := hmac.New(sha256.New, []byte("Tor safe cookie authentication "+direction+" hash"))
	_, _ = mac.Write(message)
	return mac.Sum(nil)
}

func serveFakeSOCKS(conn net.Conn, mode string) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if mode == "socks-stall" {
		_, _ = io.Copy(io.Discard, conn)
		return
	}
	reader := bufio.NewReader(conn)
	read := func(size int) []byte {
		buffer := make([]byte, size)
		if _, err := io.ReadFull(reader, buffer); err != nil {
			return nil
		}
		return buffer
	}
	header := read(2)
	if len(header) != 2 || header[0] != 5 || !bytes.Contains(read(int(header[1])), []byte{2}) {
		return
	}
	_, _ = conn.Write([]byte{5, 2})
	header = read(2)
	if len(header) != 2 || header[0] != 1 || len(read(int(header[1]))) != 32 {
		return
	}
	length := read(1)
	if len(length) != 1 || len(read(int(length[0]))) != 32 {
		return
	}
	_, _ = conn.Write([]byte{1, 0})
	header = read(5)
	if len(header) != 5 || !bytes.Equal(header[:4], []byte{5, 1, 0, 3}) {
		return // SOCKS target must be a hostname, never a locally resolved IP.
	}
	address := string(read(int(header[4])))
	if ValidateAddress(address) != nil || !bytes.Equal(read(2), []byte{1, 187}) {
		return
	}
	target, err := os.ReadFile(filepath.Join(os.Getenv("SHADOWTALK_FAKE_REGISTRY"), address))
	if err != nil || !loopbackEndpoint(string(target)) {
		_, _ = conn.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	peer, err := net.DialTimeout("tcp4", string(target), time.Second)
	if err != nil {
		return
	}
	defer peer.Close()
	_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	done := make(chan struct{})
	go func() { _, _ = io.Copy(conn, peer); _ = conn.Close(); close(done) }()
	_, _ = io.Copy(peer, reader)
	_ = peer.Close()
	<-done
}
