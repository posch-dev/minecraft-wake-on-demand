package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const duckDNSEndpoint = "https://www.duckdns.org/update"

var duckDNSClient = &http.Client{Timeout: 10 * time.Second}

// The empty ip parameter tells DuckDNS to use the address the request came from.
func duckDNSURL(cfg *Config) string {
	params := url.Values{}
	params.Set("domains", cfg.DuckDNS.Domain)
	params.Set("token", cfg.DuckDNS.Token)
	params.Set("ip", "")
	return duckDNSEndpoint + "?" + params.Encode()
}

func updateDuckDNS(ctx context.Context, cfg *Config) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, duckDNSURL(cfg), nil)
	if err != nil {
		return err
	}
	resp, err := duckDNSClient.Do(req)
	if err != nil {
		log.Warnf("DuckDNS update failed: %v", err)
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		log.Warnf("DuckDNS update failed: %v", err)
		return err
	}
	answer := strings.TrimSpace(string(body))
	if answer == "OK" {
		log.Infof("DuckDNS updated successfully for %s.duckdns.org", cfg.DuckDNS.Domain)
		return nil
	}
	log.Warnf("DuckDNS update returned: %s", sanitizeForLog(answer, 64))
	return nil
}

func runDuckDNSUpdater(ctx context.Context, cfg *Config) {
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
