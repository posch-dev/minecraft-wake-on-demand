package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
)

const duckDNSEndpoint = "https://www.duckdns.org/update"

var duckDNSClient = &http.Client{Timeout: 10 * time.Second}

// The empty ip parameter tells DuckDNS to use the address the request came from.
func duckDNSURL(cfg *config.Config) string {
	params := url.Values{}
	params.Set("domains", cfg.DuckDNS.Domain)
	params.Set("token", cfg.DuckDNS.Token)
	params.Set("ip", "")
	return duckDNSEndpoint + "?" + params.Encode()
}

func updateDuckDNS(ctx context.Context, cfg *config.Config) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, duckDNSURL(cfg), nil)
	if err != nil {
		return err
	}
	resp, err := duckDNSClient.Do(req)
	if err != nil {
		logging.Warnf("DuckDNS update failed: %v", err)
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		logging.Warnf("DuckDNS update failed: %v", err)
		return err
	}
	answer := strings.TrimSpace(string(body))
	if answer == "OK" {
		logging.Infof("DuckDNS updated successfully for %s", cfg.DuckDNSHost())
		return nil
	}
	// DuckDNS answers "KO" for a wrong domain or token, with status 200.
	logging.Warnf("DuckDNS update returned: %s", logging.Sanitize(answer, 64))
	return fmt.Errorf("DuckDNS answered %q instead of OK", logging.Sanitize(answer, 64))
}

func runDuckDNSUpdater(ctx context.Context, cfg *config.Config) {
	interval := time.Duration(cfg.DuckDNS.UpdateIntervalHours) * time.Hour
	for {
		updateDuckDNS(ctx, cfg)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
