//go:build darwin

package network_activity

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func platformScan(ctx context.Context) (ScanResult, error) {
	var result ScanResult
	cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	out, err := exec.CommandContext(cmdCtx, "lsof",
		"-i", "tcp", "-n", "-P", "-sTCP:ESTABLISHED", "-F", "pcn").Output()
	if err != nil {
		result.DNSCacheHits = darwinDNSCache(ctx)
		return result, nil
	}

	now := time.Now().UTC()
	for _, c := range parseLsofOutput(out) {
		if !MatchesKnownAIHost(c.RemoteHost) {
			continue
		}
		c.DetectedAt = now
		result.ActiveConnections = append(result.ActiveConnections, c)
	}
	if len(result.ActiveConnections) == 0 {
		result.DNSCacheHits = darwinDNSCache(ctx)
	}
	return result, nil
}

func parseLsofOutput(out []byte) []Connection {
	var conns []Connection
	var pid uint32
	var name string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			if v, err := strconv.ParseUint(line[1:], 10, 32); err == nil {
				pid = uint32(v)
			}
		case 'c':
			name = line[1:]
		case 'n':
			if c, ok := parseLsofAddr(line[1:], pid, name); ok {
				conns = append(conns, c)
			}
		}
	}
	return conns
}

func parseLsofAddr(addr string, pid uint32, procName string) (Connection, bool) {
	parts := strings.Split(addr, "->")
	if len(parts) != 2 {
		return Connection{}, false
	}
	remoteHost, remotePort := splitHostPort(parts[1])
	_, localPort := splitHostPort(parts[0])
	if remoteHost == "" || remotePort == 0 {
		return Connection{}, false
	}
	return Connection{
		RemoteHost: remoteHost, RemotePort: remotePort,
		LocalPort: localPort, ProcessName: procName, PID: pid,
		State: StateEstablished,
	}, true
}

func splitHostPort(hp string) (host string, port uint16) {
	if len(hp) == 0 {
		return
	}
	if hp[0] == '[' {
		close := strings.LastIndex(hp, "]")
		if close < 0 {
			return
		}
		host = hp[1:close]
		rest := hp[close+1:]
		if len(rest) > 1 && rest[0] == ':' {
			if p, err := strconv.ParseUint(rest[1:], 10, 16); err == nil {
				port = uint16(p)
			}
		}
		return
	}
	idx := strings.LastIndex(hp, ":")
	if idx < 0 {
		host = hp
		return
	}
	host = hp[:idx]
	if p, err := strconv.ParseUint(hp[idx+1:], 10, 16); err == nil {
		port = uint16(p)
	}
	return
}

func darwinDNSCache(ctx context.Context) []DNSHit {
	cmdCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx, "dscacheutil", "-cachedump", "-entries", "Host").Output()
	if err != nil {
		return nil
	}
	now := time.Now().UTC()
	seen := map[string]bool{}
	var hits []DNSHit
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "name:") {
			continue
		}
		hostname := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "name:")))
		if hostname == "" || seen[hostname] {
			continue
		}
		if MatchesKnownAIHost(hostname) {
			seen[hostname] = true
			hits = append(hits, DNSHit{Hostname: hostname, DetectedAt: now})
		}
	}
	return hits
}
