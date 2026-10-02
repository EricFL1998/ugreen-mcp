package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ugreen-nas-mcp/protocol"
)

func Register(r *protocol.ToolRegistry) {
	r.Register(protocol.Tool{
		Name:        "nas_storage_get_disks_smart",
		Description: "采集 4 块希捷 16TB 机械硬盘 (/dev/sda~/dev/sdd) 及 2 块 NVMe 固态的完整 SMART 寿命、坏道计数与实时温度",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"disk": map[string]interface{}{
					"type":        "string",
					"description": "可选盘符，如 /dev/sda 或留空表示扫描所有 6 块硬盘",
				},
			},
		},
	}, handleDisksSmart)

	r.Register(protocol.Tool{
		Name:        "nas_storage_get_raid_status",
		Description: "查看 md1 (RAID 5) 与 md2 (RAID 1) 阵列的在线成员盘健康度、掉盘预警及同步重建进度",
		InputSchema: map[string]interface{}{
			"type": "object",
		},
	}, handleRaidStatus)

	r.Register(protocol.Tool{
		Name:        "nas_storage_get_bcache_stats",
		Description: "查看 NVMe SSD 读写缓存加速池 (bcache0) 的命中率、当前缓存模式及待刷入机械盘的脏数据量",
		InputSchema: map[string]interface{}{
			"type": "object",
		},
	}, handleBcacheStats)

	r.Register(protocol.Tool{
		Name:        "nas_storage_get_volume_usage",
		Description: "查询主存储卷 /volume1 (33TB Btrfs 池) 的真实单副本数据占用、双副本元数据占用与空闲空间",
		InputSchema: map[string]interface{}{
			"type": "object",
		},
	}, handleVolumeUsage)

	r.Register(protocol.Tool{
		Name:        "nas_storage_list_subvolumes",
		Description: "列出当前 /volume1 上的所有 Btrfs 子卷结构 (Video, Docker, Music, Agent, @home) 及快照版本",
		InputSchema: map[string]interface{}{
			"type": "object",
		},
	}, handleListSubvolumes)
}

func handleDisksSmart(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	disks := []string{"/dev/sda", "/dev/sdb", "/dev/sdc", "/dev/sdd", "/dev/nvme0", "/dev/nvme1"}
	if target, ok := args["disk"].(string); ok && target != "" && target != "all" {
		disks = []string{target}
	}

	report := make(map[string]interface{})
	for _, d := range disks {
		cmd := exec.CommandContext(ctx, "smartctl", "-j", "-H", "-i", "-A", d)
		out, _ := cmd.Output()
		if len(out) > 0 {
			var parsed map[string]interface{}
			if err := json.Unmarshal(out, &parsed); err == nil {
				summary := map[string]interface{}{}
				if model, ok := parsed["model_name"]; ok {
					summary["model"] = model
				}
				if serial, ok := parsed["serial_number"]; ok {
					summary["serial"] = serial
				}
				if passed, ok := parsed["smart_status"]; ok {
					summary["status"] = passed
				}
				if temp, ok := parsed["temperature"]; ok {
					summary["temperature"] = temp
				}
				report[d] = summary
				continue
			}
		}

		// Fallback simple smartctl
		cmdSimple := exec.CommandContext(ctx, "smartctl", "-H", d)
		outSimple, _ := cmdSimple.CombinedOutput()
		report[d] = strings.TrimSpace(string(outSimple))
	}

	return report, nil
}

func handleRaidStatus(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	data, err := os.ReadFile("/proc/mdstat")
	if err != nil {
		return nil, fmt.Errorf("无法读取 /proc/mdstat: %v", err)
	}

	lines := strings.Split(string(data), "\n")
	result := make(map[string]interface{})
	for i, l := range lines {
		if strings.HasPrefix(l, "md") {
			parts := strings.Fields(l)
			mdName := parts[0]
			detail := l
			if i+1 < len(lines) {
				detail += " | " + strings.TrimSpace(lines[i+1])
			}
			result[mdName] = detail
		}
	}

	return map[string]interface{}{
		"active_arrays": result,
		"raw_mdstat":    strings.TrimSpace(string(data)),
	}, nil
}

func handleBcacheStats(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	base := "/sys/block/bcache0/bcache"
	if _, err := os.Stat(base); err != nil {
		return map[string]string{"status": "bcache0 不存在或未启用"}, nil
	}

	readVal := func(name string) string {
		b, err := os.ReadFile(filepath.Join(base, name))
		if err != nil {
			return "N/A"
		}
		return strings.TrimSpace(string(b))
	}

	return map[string]interface{}{
		"state":          readVal("state"),
		"cache_mode":     readVal("cache_mode"),
		"dirty_data":     readVal("dirty_data"),
		"writeback_rate": readVal("writeback_rate"),
		"stats_total": map[string]string{
			"cache_hits":   readVal("stats_total/cache_hits"),
			"cache_misses": readVal("stats_total/cache_misses"),
			"bypassed":     readVal("stats_total/bypassed"),
		},
	}, nil
}

func handleVolumeUsage(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	cmd := exec.CommandContext(ctx, "btrfs", "filesystem", "usage", "/volume1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("btrfs 执行失败: %v - %s", err, string(out))
	}
	return map[string]string{
		"path":         "/volume1",
		"filesystem":   "Btrfs",
		"btrfs_report": strings.TrimSpace(string(out)),
	}, nil
}

func handleListSubvolumes(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	cmd := exec.CommandContext(ctx, "btrfs", "subvolume", "list", "/volume1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("btrfs subvolume list 执行失败: %v", err)
	}

	var subvolumes []string
	lines := strings.Split(string(out), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			subvolumes = append(subvolumes, l)
		}
	}

	return map[string]interface{}{
		"subvolumes": subvolumes,
	}, nil
}
