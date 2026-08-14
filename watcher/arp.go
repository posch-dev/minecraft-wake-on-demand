package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var macPattern = regexp.MustCompile(`(?i)\b([0-9a-f]{2}[:-]){5}[0-9a-f]{2}\b`)

// Looks the MAC up in the ARP cache after a ping, which is the only way to
// learn it without asking the user to go read it off the machine.
func LookupMAC(ctx context.Context, ip string) (string, error) {
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("%s is not an IP address", ip)
	}

	pinger := &Pinger{}
	pinger.Ping(ctx, ip, 2*time.Second)

	if runtime.GOOS == "linux" {
		if mac, err := macFromProcNet(ip); err == nil {
			return mac, nil
		}
	}
	return macFromARPCommand(ctx, ip)
}

func macFromProcNet(ip string) (string, error) {
	data, err := os.ReadFile("/proc/net/arp")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] != ip {
			continue
		}
		if mac := macPattern.FindString(fields[3]); mac != "" && !isNullMAC(mac) {
			return normalizeMAC(mac), nil
		}
	}
	return "", fmt.Errorf("%s is not in the ARP cache", ip)
}

// Parses by pattern rather than by column, because the arp output is localised
// on Windows and differs between platforms.
func macFromARPCommand(ctx context.Context, ip string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "arp", "-a").Output()
	if err != nil {
		return "", fmt.Errorf("cannot read the ARP cache: %w", err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if !lineMentionsIP(line, ip) {
			continue
		}
		if mac := macPattern.FindString(line); mac != "" && !isNullMAC(mac) {
			return normalizeMAC(mac), nil
		}
	}
	return "", fmt.Errorf("%s is not in the ARP cache, is the PC switched on and on this network", ip)
}

// The address shows up bare on Windows and in parentheses on macOS.
func lineMentionsIP(line, ip string) bool {
	for _, field := range strings.Fields(line) {
		if strings.Trim(field, "()[],") == ip {
			return true
		}
	}
	return false
}

func isNullMAC(mac string) bool {
	cleaned := strings.NewReplacer(":", "", "-", "").Replace(mac)
	return cleaned == "000000000000" || strings.EqualFold(cleaned, "ffffffffffff")
}

func normalizeMAC(mac string) string {
	return strings.ToUpper(strings.ReplaceAll(mac, "-", ":"))
}
