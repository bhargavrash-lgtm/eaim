//go:build windows

package network_activity

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	mibTCPStateEstablished = 5
	mibTCPStateTimeWait    = 11
	mibTCPStateCloseWait   = 8
	tcpTableOwnerPIDAll    = 5
	afINET                 = 2
)

type mibTCPRowOwnerPID struct {
	State, LocalAddr, LocalPort, RemoteAddr, RemotePort, OwningPID uint32
}
type mibTCPTableOwnerPID struct{ NumEntries uint32 }

var (
	iphlpapi              = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
)

func platformScan(ctx context.Context) (ScanResult, error) {
	var result ScanResult
	conns, err := getExtendedTCPTable()
	if err != nil {
		return result, fmt.Errorf("GetExtendedTcpTable: %w", err)
	}
	now := time.Now().UTC()
	for _, row := range conns {
		remote := uint32ToIP(row.RemoteAddr).String()
		remotePort := ntohs(uint16(row.RemotePort))
		if remotePort == 0 {
			continue
		}
		hostname := reverseResolve(ctx, remote)
		if hostname == "" {
			hostname = remote
		}
		if !MatchesKnownAIHost(hostname) {
			continue
		}
		result.ActiveConnections = append(result.ActiveConnections, Connection{
			RemoteHost:  hostname,
			RemotePort:  remotePort,
			LocalPort:   ntohs(uint16(row.LocalPort)),
			ProcessName: processName(row.OwningPID),
			PID:         row.OwningPID,
			State:       tcpStateString(row.State),
			DetectedAt:  now,
		})
	}
	if len(result.ActiveConnections) == 0 {
		result.DNSCacheHits = execAndParseDNSCache(ctx)
	}
	return result, nil
}

func getExtendedTCPTable() ([]mibTCPRowOwnerPID, error) {
	var size uint32 = 32768
	for i := 0; i < 5; i++ {
		buf := make([]byte, size)
		ret, _, _ := procGetExtendedTcpTable.Call(
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)),
			1, uintptr(afINET), uintptr(tcpTableOwnerPIDAll), 0)
		if ret == 0 {
			return parseTCPTable(buf), nil
		}
		if ret == 122 {
			size *= 2
			continue
		}
		return nil, fmt.Errorf("GetExtendedTcpTable: 0x%x", ret)
	}
	return nil, fmt.Errorf("GetExtendedTcpTable: buffer too small")
}

func parseTCPTable(buf []byte) []mibTCPRowOwnerPID {
	if len(buf) < 4 {
		return nil
	}
	n := int((*mibTCPTableOwnerPID)(unsafe.Pointer(&buf[0])).NumEntries)
	rowSize := int(unsafe.Sizeof(mibTCPRowOwnerPID{}))
	offset := int(unsafe.Sizeof(mibTCPTableOwnerPID{}))
	rows := make([]mibTCPRowOwnerPID, 0, n)
	for i := 0; i < n; i++ {
		if offset+rowSize > len(buf) {
			break
		}
		rows = append(rows, *(*mibTCPRowOwnerPID)(unsafe.Pointer(&buf[offset])))
		offset += rowSize
	}
	return rows
}

func uint32ToIP(addr uint32) net.IP {
	return net.IPv4(byte(addr), byte(addr>>8), byte(addr>>16), byte(addr>>24))
}

func ntohs(n uint16) uint16 { return (n>>8)&0xff | (n&0xff)<<8 }

func tcpStateString(state uint32) ConnectionState {
	switch state {
	case mibTCPStateEstablished:
		return StateEstablished
	case mibTCPStateTimeWait:
		return StateTimeWait
	case mibTCPStateCloseWait:
		return StateCloseWait
	default:
		return ConnectionState(fmt.Sprintf("STATE_%d", state))
	}
}

func reverseResolve(ctx context.Context, ip string) string {
	lCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(lCtx, ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(names[0], ".")
}

func processName(pid uint32) string {
	if pid == 0 {
		return ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return fmt.Sprintf("pid:%d", pid)
	}
	defer windows.CloseHandle(h)
	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return fmt.Sprintf("pid:%d", pid)
	}
	full := windows.UTF16ToString(buf[:size])
	if idx := strings.LastIndexAny(full, `\/`); idx >= 0 {
		return full[idx+1:]
	}
	return full
}

func execAndParseDNSCache(ctx context.Context) []DNSHit {
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx, "ipconfig", "/displaydns").Output()
	if err != nil {
		return nil
	}
	return ParseDNSCache(string(out))
}
