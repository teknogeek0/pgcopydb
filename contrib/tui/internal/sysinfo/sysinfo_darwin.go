//go:build darwin

package sysinfo

import (
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/metrics"
)

func collect() (*metrics.SystemStats, error) {
	s := &metrics.SystemStats{}

	// CPU
	percents, err := cpu.Percent(0, false)
	if err == nil && len(percents) > 0 {
		s.CPUPercent = percents[0]
	} else if err == nil {
		percents, _ = cpu.Percent(200*time.Millisecond, false)
		if len(percents) > 0 {
			s.CPUPercent = percents[0]
		}
	}

	// Memory
	vm, err := mem.VirtualMemory()
	if err == nil {
		s.MemTotal = vm.Total
		s.MemUsed = vm.Used
		s.MemPercent = vm.UsedPercent
	}

	// Disk
	usage, err := disk.Usage("/")
	if err == nil {
		s.DiskTotal = usage.Total
		s.DiskUsed = usage.Used
		s.DiskPercent = usage.UsedPercent
	}

	// Network
	counters, err := net.IOCounters(false)
	if err == nil && len(counters) > 0 {
		s.NetTxBytes = counters[0].BytesSent
		s.NetRxBytes = counters[0].BytesRecv
	}

	return s, nil
}
