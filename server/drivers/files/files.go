package files

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"ugreen-nas-mcp/config"
	"ugreen-nas-mcp/protocol"
	"ugreen-nas-mcp/security"
)

const (
	// defaultReadMaxBytes: 256 KiB，足以装下 .env / 单日 jsonl 片段，不会打爆模型上下文
	defaultReadMaxBytes = 256 * 1024
	// maxReadMaxBytes: 1 MiB 硬上限，防止一次调用把整个大日志吸进响应
	maxReadMaxBytes = 1024 * 1024
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
		Name:        "nas_file_read",
		Description: "【只读】按绝对路径读取文本文件：可读头段（默认）、任意 offset 段、或尾部 tail_bytes。默认最多 256 KiB，截断会回报文件总字节数与实际起止偏移。用于读 .env、*.jsonl、*.json 等；不写、不删、不改任何文件",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "要读取的文件绝对路径，如 /volume1/Agent/hermes/.env",
				},
				"max_bytes": map[string]interface{}{
					"type":        "integer",
					"description": "从 offset_bytes 起最多返回的字节数，默认 262144 (256 KiB)，上限 1048576 (1 MiB)；超出部分截断",
				},
				"offset_bytes": map[string]interface{}{
					"type":        "integer",
					"description": "起始字节偏移，默认 0（从头读）。与 tail_bytes 互斥；返回里的 offset_bytes 是实际起点",
				},
				"tail_bytes": map[string]interface{}{
					"type":        "integer",
					"description": "只要文件最后 N 字节（读日志尾部/最新 JSON 记录用）。与 offset_bytes 互斥，同时给会报错",
				},
			},
			"required": []string{"path"},
		},
	}, handleFileRead)

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

func handleFileRead(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("路径不能为空")
	}

	guardian := security.GetGuardian()
	if err := guardian.CheckCredentialDeny(path); err != nil {
		return nil, err
	}

	// 按解析后的真实路径做沙箱校验：软链不能把读取带出白名单，
	// 同时避免把 /var → /private/var 这类系统软链误判为越界。
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("解析路径失败: %v", err)
	}
	if err := guardian.CheckPathSandbox(resolved); err != nil {
		return nil, err
	}
	if err := guardian.CheckCredentialDeny(resolved); err != nil {
		return nil, err
	}

	fi, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %v", err)
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("目标是一个目录，请改用 nas_file_list")
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("目标不是普通文件（可能是设备或管道），拒绝读取")
	}

	maxBytes := defaultReadMaxBytes
	if mb, ok := args["max_bytes"].(float64); ok && mb > 0 {
		maxBytes = int(mb)
	}
	if maxBytes > maxReadMaxBytes {
		maxBytes = maxReadMaxBytes
	}

	_, hasOffset := args["offset_bytes"]
	_, hasTail := args["tail_bytes"]
	if hasOffset && hasTail {
		return nil, fmt.Errorf("offset_bytes 与 tail_bytes 只能二选一：前者从指定位置向后读，后者读文件末尾 N 字节")
	}

	total := fi.Size()
	var start, want int64

	if hasTail {
		n := int64(defaultReadMaxBytes)
		if tb, ok := args["tail_bytes"].(float64); ok && tb > 0 {
			n = int64(tb)
		}
		if n > maxReadMaxBytes {
			n = maxReadMaxBytes
		}
		if n > total {
			n = total
		}
		start = total - n
		want = n
	} else {
		off := int64(0)
		if ob, ok := args["offset_bytes"].(float64); ok {
			if ob < 0 {
				return nil, fmt.Errorf("offset_bytes 不能为负数: %.0f", ob)
			}
			off = int64(ob)
		}
		if off > total {
			return nil, fmt.Errorf("offset_bytes=%d 超出文件大小 %d，拒绝返回空内容", off, total)
		}
		start = off
		want = int64(maxBytes)
		if total-off < want {
			want = total - off
		}
	}

	f, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %v", err)
	}
	defer f.Close()

	if err := checkTextEncoding(resolved, total); err != nil {
		return nil, err
	}

	// 起点不在 0 时，向前多读最多 3 字节，用来把起点对齐到字符边界
	readStart := start
	pad := int64(0)
	if start > 0 {
		readStart = start - 3
		if readStart < 0 {
			readStart = 0
		}
		pad = start - readStart
	}
	if _, err := f.Seek(readStart, io.SeekStart); err != nil {
		return nil, fmt.Errorf("定位文件失败: %v", err)
	}

	buf := make([]byte, want+pad)
	n, readErr := io.ReadFull(f, buf)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("读取文件失败: %v", readErr)
	}
	buf = buf[:n]

	// 丢掉对齐用的前导字节：从中间开始时不能把多字节字符切成两半，
	// 否则会变成"截断即解码失败"——那正是上一轮的缺陷形态。
	if pad > 0 {
		if int64(len(buf)) <= pad {
			buf = buf[:0]
		} else {
			buf = buf[pad:]
		}
		for i := 0; i < 3 && len(buf) > 0 && buf[0]&0xC0 == 0x80; i++ {
			buf = buf[1:]
			start++
		}
	}

	end := start + int64(len(buf))

	// 末尾不落在文件结尾时才可能切断字符：最多回退 3 字节到字符边界
	if end < total && !utf8.Valid(buf) {
		for i := 0; i < 3 && len(buf) > 0 && !utf8.Valid(buf); i++ {
			buf = buf[:len(buf)-1]
		}
		end = start + int64(len(buf))
	}
	// 仍非法 → 编码不支持，必须报错，不能静默降级成空内容/截断
	if !utf8.Valid(buf) {
		return nil, fmt.Errorf("编码不支持：%s（文件共 %d 字节，本次读取 offset=%d, %d 字节），已拒绝返回内容；请先转成 UTF-8 再读",
			describeEncoding(buf), total, start, len(buf))
	}

	return map[string]interface{}{
		"file_path":      resolved,
		"total_bytes":    total,
		"size_bytes":     total, // 兼容旧字段名
		"offset_bytes":   start,
		"returned_bytes": len(buf),
		"end_bytes":      end,
		"range":          fmt.Sprintf("bytes %d-%d of %d", start, end, total),
		"truncated":      end < total || start > 0,
		"encoding":       "utf-8",
		"content":        string(buf),
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

// checkTextEncoding 在读取前用文件头/样本快速判定明显不是文本的情况（BOM、NUL）。
func checkTextEncoding(path string, total int64) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("打开文件失败: %v", err)
	}
	defer f.Close()

	head := make([]byte, 4096)
	n, _ := f.Read(head)
	head = head[:n]
	if n == 0 {
		return nil // 空文件按 UTF-8 处理
	}

	switch {
	case bytes.HasPrefix(head, []byte{0xEF, 0xBB, 0xBF}):
		// 带 BOM 的 UTF-8，正常
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE, 0x00, 0x00}):
		return fmt.Errorf("编码不支持：疑似 UTF-32LE 文本（含 BOM，共 %d 字节），本工具只返回 UTF-8，请先转码", total)
	case bytes.HasPrefix(head, []byte{0x00, 0x00, 0xFE, 0xFF}):
		return fmt.Errorf("编码不支持：疑似 UTF-32BE 文本（含 BOM，共 %d 字节），本工具只返回 UTF-8，请先转码", total)
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE}):
		return fmt.Errorf("编码不支持：疑似 UTF-16LE 文本（含 BOM，共 %d 字节），本工具只返回 UTF-8，请先转码", total)
	case bytes.HasPrefix(head, []byte{0xFE, 0xFF}):
		return fmt.Errorf("编码不支持：疑似 UTF-16BE 文本（含 BOM，共 %d 字节），本工具只返回 UTF-8，请先转码", total)
	}

	if bytes.IndexByte(head, 0x00) >= 0 {
		return fmt.Errorf("目标疑似二进制文件（含 NUL 字节，共 %d 字节），本工具只返回文本", total)
	}
	return nil
}

// describeEncoding 给解码失败的文件一个诚实、可操作的描述，而不是谎报 utf-8。
func describeEncoding(sample []byte) string {
	high := 0
	for _, b := range sample {
		if b >= 0x80 {
			high++
		}
	}
	if len(sample) > 0 && high*2 > len(sample) {
		return "检测到大量非 ASCII 字节、但不是合法 UTF-8（疑似 GBK/GB18030 等本地编码）"
	}
	return "不是合法 UTF-8（含无法解码的字节序列）"
}
