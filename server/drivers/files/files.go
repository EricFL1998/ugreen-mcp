package files

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"ugreen-nas-mcp/config"
	"ugreen-nas-mcp/protocol"
	"ugreen-nas-mcp/security"
)

func Register(r *protocol.ToolRegistry) {
	r.Register(protocol.Tool{
		Name:        "nas_file_list",
		Description: "浏览数据卷 (/volume1/Video, /volume1/Docker, /volume1/Music等) 目录下的文件与子目录（严格沙箱隔离）",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "目录绝对路径，如 /volume1/Video 或 /volume1/Docker",
				},
			},
			"required": []string{"path"},
		},
	}, handleFileList)

	r.Register(protocol.Tool{
		Name:        "nas_file_preview",
		Description: "预览指定文件头部内容或文本日志片段（默认读取前 100 行，防打爆模型上下文）",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "要预览的文件绝对路径",
				},
				"max_lines": map[string]interface{}{
					"type":        "integer",
					"description": "最大读取行数，默认 100",
				},
			},
			"required": []string{"path"},
		},
	}, handleFilePreview)

	r.Register(protocol.Tool{
		Name:        "nas_file_delete",
		Description: "【高危操作 - 二阶段确认】永久删除指定数据文件，需用户明确许可",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "待删除的文件绝对路径",
				},
				"confirm_token": map[string]interface{}{
					"type":        "string",
					"description": "首次调用留空；确认执行时填入确认票据 (confirm_token)",
				},
			},
			"required": []string{"path"},
		},
	}, handleFileDelete)
}

func handleFileList(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	path, _ := args["path"].(string)
	if path == "" {
		path = "/volume1"
	}

	guardian := security.GetGuardian()
	if err := guardian.CheckPathSandbox(path); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败: %v", err)
	}

	var items []map[string]interface{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "@") {
			continue
		}
		info, _ := e.Info()
		size := int64(0)
		modTime := ""
		if info != nil {
			size = info.Size()
			modTime = info.ModTime().Format("2006-01-02 15:04:05")
		}

		items = append(items, map[string]interface{}{
			"name":       name,
			"is_dir":     e.IsDir(),
			"size_bytes": size,
			"modified":   modTime,
		})
	}

	return map[string]interface{}{
		"current_path": path,
		"item_count":   len(items),
		"entries":      items,
	}, nil
}

func handleFilePreview(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("路径不能为空")
	}

	guardian := security.GetGuardian()
	if err := guardian.CheckPathSandbox(path); err != nil {
		return nil, err
	}

	maxLines := 100
	if ml, ok := args["max_lines"].(float64); ok && ml > 0 {
		maxLines = int(ml)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %v", err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() && count < maxLines {
		lines = append(lines, scanner.Text())
		count++
	}

	return map[string]interface{}{
		"file_path":  path,
		"line_count": len(lines),
		"lines":      lines,
	}, nil
}

func handleFileDelete(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("路径不能为空")
	}

	guardian := security.GetGuardian()
	if err := guardian.CheckPathSandbox(path); err != nil {
		return nil, err
	}

	confirmToken, _ := args["confirm_token"].(string)
	if confirmToken == "" {
		fi, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("目标文件不存在: %v", err)
		}

		summary := fmt.Sprintf("永久删除文件 [%s] (大小: %d 字节)", path, fi.Size())
		impact := "此操作不可撤销！文件将被直接从 Btrfs 存储卷中永久擦除，数据不可挽回"
		token, err := guardian.RequestConfirmation("FILE_DELETE", path, "", summary, impact)
		if err != nil {
			return nil, err
		}

		return map[string]interface{}{
			"status":             "CONFIRMATION_REQUIRED",
			"confirm_token":      token,
			"expires_in_seconds": 60,
			"risk_level":         "CRITICAL",
			"action_summary":     summary,
			"impact_analysis":    impact,
			"prompt_to_user":     fmt.Sprintf("⚠️ 危险操作安全拦截：即将删除文件 [%s] (大小 %d 字节)。此操作不可恢复，确认执行吗？", path, fi.Size()),
		}, nil
	}

	if err := guardian.VerifyAndConsume(confirmToken, "FILE_DELETE", path); err != nil {
		return nil, err
	}

	cfg := config.GetConfig()
	if cfg.ReadOnly {
		return nil, fmt.Errorf("当前系统运行在全局只读模式，拒绝删除操作")
	}

	if err := os.Remove(path); err != nil {
		return nil, fmt.Errorf("文件删除失败: %v", err)
	}

	return map[string]string{
		"status":  "SUCCESS",
		"message": fmt.Sprintf("文件 [%s] 已永久删除", path),
	}, nil
}
