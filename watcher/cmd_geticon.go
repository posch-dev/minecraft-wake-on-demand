package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const iconDataURIPrefix = "data:image/png;base64,"

// A command, not something the proxy learns: answering an unauthenticated
// status ping must never write to disk.
func runGetServerIcon() int {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Printf("Config error: %v\n", err)
		fmt.Println("\nRun 'mc-wol-proxy init' first.")
		return 1
	}

	address := net.JoinHostPort(cfg.Server.IP, strconv.Itoa(cfg.Server.MCPort))
	fmt.Printf("Asking %s for its server icon...\n", address)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	payload, err := fetchServerStatus(ctx, cfg)
	if err != nil {
		fmt.Printf("\n%v\n", err)
		fmt.Println("\nThe server has to be running for this. Join once to wake it,")
		fmt.Println("or start the container, then run this again.")
		return 1
	}

	if strings.TrimSpace(payload.Favicon) == "" {
		fmt.Println("\nThe server does not serve an icon, so there is nothing to fetch.")
		fmt.Println("Put a 64x64 server-icon.png next to the world on the server, or put")
		fmt.Printf("one straight into %s.\n", cfg.AssetsDir())
		return 1
	}

	decoded, err := decodeFaviconDataURI(payload.Favicon)
	if err != nil {
		fmt.Printf("\nThe server sent something unusable as its icon: %v\n", err)
		return 1
	}
	if _, err := decodeIconPNG("the server's icon", decoded); err != nil {
		fmt.Printf("\n%v\n", err)
		return 1
	}

	target := filepath.Join(cfg.AssetsDir(), "server-icon.png")
	if err := writeServerIcon(target, decoded); err != nil {
		fmt.Printf("\n%v\n", err)
		return 1
	}

	fmt.Printf("\nSaved %d bytes to %s\n", len(decoded), target)
	fmt.Println("It now shows at half opacity under the sleeping and waking icons.")
	fmt.Println("Put a server-icon-sleeping.png next to it to replace those outright.")
	return 0
}

func fetchServerStatus(ctx context.Context, cfg *Config) (*statusPayload, error) {
	address := net.JoinHostPort(cfg.Server.IP, strconv.Itoa(cfg.Server.MCPort))

	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", address, err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(10 * time.Second))
	request := append(
		makeStatusHandshake(cfg.Server.IP, cfg.Server.MCPort),
		makeStatusRequest()...,
	)
	if _, err := conn.Write(request); err != nil {
		return nil, fmt.Errorf("cannot ask %s: %w", address, err)
	}

	body, err := readFramedPacket(conn, maxStatusResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("no usable answer from %s: %w", address, err)
	}
	return parseStatusPayload(body)
}

func decodeFaviconDataURI(favicon string) ([]byte, error) {
	encoded, found := strings.CutPrefix(strings.TrimSpace(favicon), iconDataURIPrefix)
	if !found {
		return nil, fmt.Errorf("not a PNG data URI")
	}
	// Some servers wrap the base64, which is not valid in a data URI but happens.
	encoded = strings.NewReplacer("\n", "", "\r", "", " ", "").Replace(encoded)

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(decoded) > maxIconBytes {
		return nil, fmt.Errorf("icon is %d bytes, over the %d byte limit", len(decoded), maxIconBytes)
	}
	return decoded, nil
}

// Existing artwork is kept as .bak, never replaced without a way back.
func writeServerIcon(target string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w", filepath.Dir(target), err)
	}
	if _, err := os.Stat(target); err == nil {
		backup := target + ".bak"
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("cannot move the existing icon aside: %w", err)
		}
		fmt.Printf("The icon that was there is now %s\n", backup)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %w", target, err)
	}
	return nil
}
