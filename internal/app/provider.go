package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func publicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48", "2001::/32", "2002::/16", "fec0::/10"} {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return false
		}
	}
	return true
}
func safeClient() *http.Client {
	return &http.Client{Timeout: 100 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 90 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("host has no addresses")
		}
		for _, ip := range ips {
			if !publicIP(ip.IP) {
				return nil, errors.New("non-public destination denied")
			}
		}
		d := net.Dialer{Timeout: 10 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}}
}

type providerResponse struct {
	TaskID string `json:"task_id"`
	Status string `json:"task_status"`
	Data   []struct {
		URL    string `json:"url"`
		Base64 string `json:"b64_json"`
	} `json:"data"`
}

func readProvider(res *http.Response) (providerResponse, error) {
	defer res.Body.Close()
	var p providerResponse
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return p, fmt.Errorf("provider returned HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 29*1024*1024+1))
	if err != nil || len(body) > 29*1024*1024 {
		return p, errors.New("provider response too large")
	}
	err = json.Unmarshal(body, &p)
	return p, err
}
func (a *App) callProvider(ctx context.Context, cfg Settings, prompt string, images []string) (string, error) {
	client := a.providerClient()
	defer client.CloseIdleConnections()
	payload := map[string]any{"model": cfg.Model, "prompt": prompt, "size": "1024x1024", "quality": cfg.Quality, "n": 1, "output_format": "png", "response_format": "b64_json", "background": "transparent", "async": true, "retries": 0}
	if len(images) == 1 {
		payload["image"] = images[0]
	} else {
		payload["images"] = images
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	key := a.secret("api_key")
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.APIBase+"/images/generations", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	// Never retry a generation POST: one dispatch is one user credit.
	res, err := client.Do(req)
	if err != nil {
		return "", errors.New("image provider connection failed")
	}
	p, err := readProvider(res)
	if err != nil {
		return "", err
	}
	for len(p.Data) == 0 && p.TaskID != "" && p.Status != "failed" && p.Status != "canceled" {
		if !idPattern.MatchString(p.TaskID) {
			return "", errors.New("invalid provider task ID")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
		task := p.TaskID
		req, err = http.NewRequestWithContext(ctx, "GET", cfg.APIBase+"/images/"+url.PathEscape(task), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		res, err = client.Do(req)
		if err != nil {
			return "", errors.New("image status request failed")
		}
		p, err = readProvider(res)
		if err != nil {
			return "", err
		}
		if p.TaskID == "" {
			p.TaskID = task
		}
	}
	if p.Status == "failed" || p.Status == "canceled" || len(p.Data) != 1 {
		return "", errors.New("provider did not return exactly one image")
	}
	item := p.Data[0]
	if item.Base64 != "" {
		return "data:image/png;base64," + item.Base64, nil
	}
	u, err := url.Parse(item.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" && !strings.EqualFold(u.Port(), "443") || !contains(cfg.AssetHosts, u.Hostname()) {
		return "", errors.New("image download host not allowed")
	}
	req, err = http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return "", err
	}
	res, err = client.Do(req)
	if err != nil {
		return "", errors.New("image download failed")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", errors.New("image download status invalid")
	}
	b, err = io.ReadAll(io.LimitReader(res.Body, 20*1024*1024+1))
	if err != nil || len(b) > 20*1024*1024 {
		return "", errors.New("image response too large")
	}
	return "data:" + http.DetectContentType(b) + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}
