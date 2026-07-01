//go:build linux

package network_activity

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func platformScan(ctx context.Context) (ScanResult, error) {
	var result ScanResult
	inodeMap, err := parseProcNetTCP()
	if err != nil {
		return result, fmt.Errorf("read /proc/net/tcp: %w", err)
	}
	if len(inodeMap) == 0 {
		result.DNSCacheHits = linuxDNSCache()
		return result, nil
	}

	now := time.Now().UTC()
	aiInodes := map[string]Connection{}
	for inode, c := range inodeMap {
		if MatchesKnownAIHost(c.RemoteHost) {
			c.DetectedAt = now
			aiInodes[inode] = c
		}
	}
	if len(aiInodes) == 0 {
		result.DNSCacheHits = linuxDNSCache()
		return result, nil
	}
	correlateProcesses(aiInodes)
	for _, c := range aiInodes {
		result.ActiveConnections = append(result.ActiveConnections, c)
	}
	return result, nil
}

func parseProcNetTCP() (map[string]Connection, error) {
	out := map[string]Connection{}
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if err := parseTCPFile(path, out); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return out, nil
}

func parseTCPFile(path string, out map[string]Connection) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Scan() // skip header
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		if len(fields) < 10 {
			continue
		}
		state := parseTCPState(fields[3])
		if state == "" {
			continue
		}
		remHost, remPort, err := parseHexAddr(fields[2])
		if err != nil || remPort == 0 {
			continue
		}
		_, localPort, _ := parseHexAddr(fields[1])
		out[fields[9]] = Connection{
			RemoteHost: remHost, RemotePort: remPort,
			LocalPort: localPort, State: state,
		}
	}
	return scanner.Err()
}

func parseHexAddr(hexAddr string) (string, uint16, error) {
	parts := strings.SplitN(hexAddr, ":", 2)
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid addr %q", hexAddr)
	}
	portVal, err := strconv.ParseUint(parts[1], 16, 16)
	if err != nil {
		return "", 0, err
	}
	ipBytes, err := hex.DecodeString(parts[0])
	if err != nil {
		return "", 0, err
	}
	var ip net.IP
	switch len(ipBytes) {
	case 4:
		ip = net.IPv4(ipBytes[3], ipBytes[2], ipBytes[1], ipBytes[0])
	case 16:
		b := make([]byte, 16)
		for i := 0; i < 4; i++ {
			b[i*4+0], b[i*4+1], b[i*4+2], b[i*4+3] =
				ipBytes[i*4+3], ipBytes[i*4+2], ipBytes[i*4+1], ipBytes[i*4+0]
		}
		ip = net.IP(b)
	default:
		return "", 0, fmt.Errorf("unexpected IP length %d", len(ipBytes))
	}
	names, _ := net.LookupAddr(ip.String())
	host := ip.String()
	if len(names) > 0 {
		host = strings.TrimSuffix(names[0], ".")
	}
	return host, uint16(portVal), nil
}

func parseTCPState(h string) ConnectionState {
	switch strings.ToUpper(h) {
	case "01":
		return StateEstablished
	case "06":
		return StateTimeWait
	case "08":
		return StateCloseWait
	}
	return ""
}

func correlateProcesses(aiInodes map[string]Connection) {
	dirs, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fmt.Sprintf("/proc/%d/fd", pid), fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			if c, ok := aiInodes[inode]; ok {
				c.PID = uint32(pid)
				c.ProcessName = readComm(pid)
				aiInodes[inode] = c
			}
		}
	}
}

func readComm(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return fmt.Sprintf("pid:%d", pid)
	}
	return strings.TrimSpace(string(b))
}

func linuxDNSCache() []DNSHit {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "resolvectl", "statistics")
	_ = cmd.Run()
	return nil // placeholder; resolvectl doesn't expose individual hostnames
}
