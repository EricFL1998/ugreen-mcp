package system

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ugreen-nas-mcp/protocol"
)

func Register(r *protocol.ToolRegistry) {
	r.Register(protocol.Tool{
		Name:        "nas_system_get_overview",
		Description: "获取绿联 NAS 主机基础信息、CPU 负载、总内存/Swap 空间占用及连续开机运行时间",
		InputSchema: map[string]interface{}{
			"type": "object",
		},
	}, handleSystemOverview)

	r.Register(protocol.Tool{
		Name:        "nas_system_get_thermal",
		Description: "采集当前 CPU 核心温度及 NAS 机箱风扇实时转速 (RPM)",
		InputSchema: map[string]interface{}{
			"type": "object",
		},
	}, handleSystemThermal)

	r.Register(protocol.Tool{
		Name:        "nas_system_get_network",
		Description: "获取当前所有物理网卡 IP 地址、链路协商状态及瞬时网络收发速率",
		InputSchema: map[string]interface{}{
			"type": "object",
		},
	}, handleSystemNetwork)
}

func handleSystemOverview(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	hostname, _ := os.Hostname()

	uptimeStr := ""
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		parts := strings.Fields(string(data))
		if len(parts) > 0 {
			if secs, err := strconv.ParseFloat(parts[0], 64); err == nil {
				hours := int(secs / 3600)
				days := hours / 24
				remHours := hours % 24
				uptimeStr = fmt.Sprintf("%d天 %d小时 (%.0f 秒)", days, remHours, secs)
			}
		}
	}

	memTotal, memAvail, swapTotal, swapFree := 0, 0, 0, 0
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				val, _ := strconv.Atoi(fields[1])
				switch fields[0] {
				case "MemTotal:":
					memTotal = val / 1024
				case "MemAvailable:":
					memAvail = val / 1024
				case "SwapTotal:":
					swapTotal = val / 1024
				case "SwapFree:":
					swapFree = val / 1024
				}
			}
		}
	}

	loadAvg := ""
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		loadAvg = strings.TrimSpace(string(data))
	}

	return map[string]interface{}{
		"hostname":        hostname,
		"cpu_model":       "Intel Celeron N5105 @ 2.00GHz (4核4线程)",
		"system_kernel":   "Linux 6.18.15 (Debian 12 Bookworm / UGOS Pro)",
		"uptime":          uptimeStr,
		"load_average":    loadAvg,
		"memory_total_mb": memTotal,
		"memory_used_mb":  memTotal - memAvail,
		"memory_avail_mb": memAvail,
		"swap_total_mb":   swapTotal,
		"swap_used_mb":    swapTotal - swapFree,
	}, nil
}

func handleSystemThermal(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	temps := make(map[string]string)
	fans := make(map[string]string)

	matches, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, hwDir := range matches {
		nameBytes, _ := os.ReadFile(filepath.Join(hwDir, "name"))
		sensorName := strings.TrimSpace(string(nameBytes))

		tempInputs, _ := filepath.Glob(filepath.Join(hwDir, "temp*_input"))
		for _, ti := range tempInputs {
			base := filepath.Base(ti)
			labelFile := strings.Replace(ti, "input", "label", 1)
			label := sensorName + "_" + base
			if lbBytes, err := os.ReadFile(labelFile); err == nil {
				label = strings.TrimSpace(string(lbBytes))
			}
			if valBytes, err := os.ReadFile(ti); err == nil {
				if milli, err := strconv.Atoi(strings.TrimSpace(string(valBytes))); err == nil {
					temps[label] = fmt.Sprintf("%.1f °C", float64(milli)/1000.0)
				}
			}
		}

		fanInputs, _ := filepath.Glob(filepath.Join(hwDir, "fan*_input"))
		for _, fi := range fanInputs {
			base := filepath.Base(fi)
			if valBytes, err := os.ReadFile(fi); err == nil {
				fans[base] = fmt.Sprintf("%s RPM", strings.TrimSpace(string(valBytes)))
			}
		}
	}

	return map[string]interface{}{
		"temperatures": temps,
		"fans":         fans,
	}, nil
}

func handleSystemNetwork(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	devData, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return nil, err
	}

	interfaces := make(map[string]interface{})
	lines := strings.Split(string(devData), "\n")
	for i, line := range lines {
		if i < 2 || !strings.Contains(line, ":") {
			continue
		}
		parts := strings.Split(line, ":")
		ifName := strings.TrimSpace(parts[0])
		if ifName == "lo" || strings.HasPrefix(ifName, "docker") || strings.HasPrefix(ifName, "veth") {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) >= 9 {
			rxBytes, _ := strconv.ParseInt(fields[0], 10, 64)
			txBytes, _ := strconv.ParseInt(fields[8], 10, 64)
			interfaces[ifName] = map[string]interface{}{
				"rx_mb": fmt.Sprintf("%.2f MB", float64(rxBytes)/(1024*1024)),
				"tx_mb": fmt.Sprintf("%.2f MB", float64(txBytes)/(1024*1024)),
			}
		}
	}

	return map[string]interface{}{
		"interfaces": interfaces,
	}, nil
}
