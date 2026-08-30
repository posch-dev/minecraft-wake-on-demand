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
		printError("Config error: " + err.Error())
		fmt.Println("\nRun 'mcwod' first to set things up.")
		return 1
	}

	p := newPrompter()
	target := filepath.Join(cfg.AssetsDir(), "server-icon.png")
	existing := fileExists(target)

	fmt.Println("\nUse the picture from your server")
	printHint("Your Minecraft server has its own picture, the small square people see",
		"next to your server in their list. MCWOD can copy it, so the same picture",
		"shows while your PC is asleep.",
		"",
		"You can set one up here as well: move a 64x64 PNG to",
		target)
	if existing {
		fmt.Println("")
		printWarning("There is already a picture there. Copying will replace it.")
	}

	fmt.Println("")
	if !p.yesNo("Copy the picture from your server PC?", true) {
		fmt.Println("Nothing was changed.")
		return 0
	}

	keepOld := false
	if existing {
		keepOld = p.yesNo("Keep the picture that is there now?", true)
	}

	fmt.Printf("\nAsking %s for its picture...\n", cfg.Server.ContainerName)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	payload, err := fetchServerStatus(ctx, cfg)
	if err != nil {
		printWarning("Your PC is asleep, so I cannot ask it for the picture.")
		printHint("Join the server once to wake it up, then try again.")
		return 1
	}

	if strings.TrimSpace(payload.Favicon) == "" {
		printWarning("Your server does not have a picture of its own yet.")
		printHint("You can give it one: put a 64x64 PNG called server-icon.png next to",
			"your world on the server PC. Or put your own here instead:",
			target)
		return 1
	}

	decoded, err := decodeFaviconDataURI(payload.Favicon)
	if err != nil {
		printError("Your server sent something that is not a usable picture: " + err.Error())
		return 1
	}
	if _, err := decodeIconPNG("the server's picture", decoded); err != nil {
		printError(err.Error())
		return 1
	}

	if err := writeServerIcon(target, decoded, keepOld); err != nil {
		printError(err.Error())
		return 1
	}
	fmt.Println("  Done. It shows while your server sleeps.")
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

// Kept under a name somebody would recognise, and only when they asked for it.
func writeServerIcon(target string, data []byte, keepOld bool) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w", filepath.Dir(target), err)
	}
	if keepOld && fileExists(target) {
		kept, err := keepOldIcon(target)
		if err != nil {
			return err
		}
		fmt.Printf("  Kept as %s\n", filepath.Base(kept))
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %w", target, err)
	}
	return nil
}

// Numbered rather than overwritten, so a second run never loses the first one.
func keepOldIcon(target string) (string, error) {
	base := strings.TrimSuffix(target, ".png") + "-old"
	for i := 0; ; i++ {
		candidate := base + ".png"
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d.png", base, i+1)
		}
		if !fileExists(candidate) {
			return candidate, os.Rename(target, candidate)
		}
	}
}
