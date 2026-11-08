package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"ugreen-nas-mcp/config"
)

func setupSandbox(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// macOS 上 /var 是 /private/var 的软链，沙箱按解析后的真实路径校验
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	setConfigForTest(t, root)
	return root
}

func callRead(t *testing.T, args map[string]interface{}) (map[string]interface{}, error) {
	t.Helper()
	res, err := handleFileRead(context.Background(), args)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(res)
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("结果无法序列化: %v", err)
	}
	return out, nil
}

// .env 必须能读到，内容原样返回
func TestFileReadDotEnvAllowed(t *testing.T) {
	root := setupSandbox(t)
	p := filepath.Join(root, ".env")
	if err := os.WriteFile(p, []byte("UGREEN_MCP_TOKEN=local-test-value\nDB=historical.db\n"), 0600); err != nil {
		t.Fatal(err)
	}

	out, err := callRead(t, map[string]interface{}{"path": p})
	if err != nil {
		t.Fatalf(".env 应可读，实际报错: %v", err)
	}
	if !strings.Contains(out["content"].(string), "DB=historical.db") {
		t.Fatalf("返回内容不完整: %v", out["content"])
	}
	if out["truncated"].(bool) {
		t.Fatal("小文件不应被标记截断")
	}
}

// 私钥路径必须被拒
func TestFileReadPrivateKeyDenied(t *testing.T) {
	root := setupSandbox(t)
	dir := filepath.Join(root, ".ssh")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(p, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := callRead(t, map[string]interface{}{"path": p}); err == nil {
		t.Fatal("~/.ssh/id_ed25519 必须被拒绝，实际放行了")
	} else if !strings.Contains(err.Error(), "凭据保护") {
		t.Fatalf("拒绝原因不对: %v", err)
	}
}

// 白名单之外的路径必须被拒
func TestFileReadOutsideSandboxDenied(t *testing.T) {
	setupSandbox(t)
	if _, err := callRead(t, map[string]interface{}{"path": "/etc/hosts"}); err == nil {
		t.Fatal("/etc/hosts 必须被拒绝")
	}
}

// 大文件按 max_bytes 截断，并回报总大小
func TestFileReadTruncates(t *testing.T) {
	root := setupSandbox(t)
	p := filepath.Join(root, "pnl_history.jsonl")
	content := strings.Repeat("{\"pnl\":1.0}\n", 500) // 6500 字节
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	out, err := callRead(t, map[string]interface{}{"path": p, "max_bytes": float64(1000)})
	if err != nil {
		t.Fatal(err)
	}
	if got := int(out["returned_bytes"].(float64)); got != 1000 {
		t.Fatalf("returned_bytes 应为 1000，实际 %d", got)
	}
	if got := int(out["size_bytes"].(float64)); got != len(content) {
		t.Fatalf("size_bytes 应为 %d，实际 %d", len(content), got)
	}
	if !out["truncated"].(bool) {
		t.Fatal("超过 max_bytes 必须标记 truncated=true")
	}
}

// 中文被切断时不能返回非法 UTF-8
func TestFileReadTruncationKeepsValidUTF8(t *testing.T) {
	root := setupSandbox(t)
	p := filepath.Join(root, "cn.txt")
	if err := os.WriteFile(p, []byte(strings.Repeat("网格交易", 200)), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := callRead(t, map[string]interface{}{"path": p, "max_bytes": float64(1001)})
	if err != nil {
		t.Fatalf("中文截断不应报错: %v", err)
	}
	content := out["content"].(string)
	if !strings.HasPrefix(strings.Repeat("网格交易", 200), content) {
		t.Fatal("截断后内容不是原文件前缀")
	}
	if !out["truncated"].(bool) {
		t.Fatal("应标记 truncated")
	}
	if out["encoding"].(string) != "utf-8" {
		t.Fatalf("正常 UTF-8 截断时 encoding 应为 utf-8，实际 %v", out["encoding"])
	}
	if int(out["returned_bytes"].(float64)) <= 0 {
		t.Fatal("正常 UTF-8 截断时 returned_bytes 必须 > 0（不能把截断做成空内容）")
	}
}

// 二进制文件拒绝按文本返回
func TestFileReadRejectsBinary(t *testing.T) {
	root := setupSandbox(t)
	p := filepath.Join(root, "blob.bin")
	if err := os.WriteFile(p, []byte{'a', 0x00, 'b', 0xff}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := callRead(t, map[string]interface{}{"path": p}); err == nil {
		t.Fatal("二进制文件必须被拒绝")
	}
}

// 目录不能当文件读
func TestFileReadRejectsDirectory(t *testing.T) {
	root := setupSandbox(t)
	if _, err := callRead(t, map[string]interface{}{"path": root}); err == nil {
		t.Fatal("目录必须被拒绝，提示改用 nas_file_list")
	}
}

// setConfigForTest 走真实的 config.LoadConfig（写临时 config.json），
// 不在生产代码里为测试留任何导出入口。
func setConfigForTest(t *testing.T, allowed ...string) {
	t.Helper()
	payload := map[string]interface{}{
		"port":           8095,
		"host":           "127.0.0.1",
		"read_only":      true,
		"allowed_paths":  allowed,
		"audit_log_path": filepath.Join(t.TempDir(), "audit.log"),
	}
	data, _ := json.Marshal(payload)
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatalf("写临时配置失败: %v", err)
	}
	if _, _, err := config.LoadConfig(p); err != nil {
		t.Fatalf("加载临时配置失败: %v", err)
	}
}

// GBK 文本必须显式报错：不能返回空 content + encoding:utf-8 + truncated:true
func TestFileReadGBKRejected(t *testing.T) {
	root := setupSandbox(t)
	p := filepath.Join(root, "gbk.txt")
	// GBK「中文测试行」= D6D0 CEC4 B2E2 CAD4 D0D0，共 10 字节
	gbk := []byte{0xD6, 0xD0, 0xCE, 0xC4, 0xB2, 0xE2, 0xCA, 0xD4, 0xD0, 0xD0}
	if err := os.WriteFile(p, gbk, 0600); err != nil {
		t.Fatal(err)
	}

	res, err := handleFileRead(context.Background(), map[string]interface{}{"path": p})
	if err == nil {
		t.Fatalf("GBK 文本必须报错，实际返回: %v", res)
	}
	msg := err.Error()
	for _, want := range []string{"编码不支持", "GBK"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误信息应包含 %q，实际: %s", want, msg)
		}
	}
	if strings.Contains(msg, "truncated") {
		t.Fatalf("解码失败不能借 truncated 字段表达: %s", msg)
	}
}

// UTF-16（带 BOM）必须显式报错，并说清是 UTF-16
func TestFileReadUTF16Rejected(t *testing.T) {
	root := setupSandbox(t)
	p := filepath.Join(root, "utf16.txt")
	// BOM(FF FE) + "ab" 的 UTF-16LE
	if err := os.WriteFile(p, []byte{0xFF, 0xFE, 'a', 0x00, 'b', 0x00}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := handleFileRead(context.Background(), map[string]interface{}{"path": p}); err == nil {
		t.Fatal("UTF-16 必须被拒绝")
	} else if !strings.Contains(err.Error(), "UTF-16") {
		t.Fatalf("应说明是 UTF-16: %v", err)
	}
}

// 纯 ASCII 必须照旧能读
func TestFileReadASCIIStillWorks(t *testing.T) {
	root := setupSandbox(t)
	p := filepath.Join(root, "plain.txt")
	if err := os.WriteFile(p, []byte("count=42\nmode=paper\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := callRead(t, map[string]interface{}{"path": p})
	if err != nil {
		t.Fatalf("ASCII 文件必须能读: %v", err)
	}
	if out["encoding"].(string) != "utf-8" || out["truncated"].(bool) {
		t.Fatalf("ASCII 文件字段不对: %v", out)
	}
	if !strings.Contains(out["content"].(string), "mode=paper") {
		t.Fatalf("内容不对: %v", out["content"])
	}
}

// 尾段读取：能拿到文件末尾的内容，并明确回报这是哪一段
func TestFileReadTail(t *testing.T) {
	root := setupSandbox(t)
	p := filepath.Join(root, "grid_balance_history.json")
	body := strings.Repeat("{\"ts\":1700000000,\"bal\":100.0}\n", 200) // 6200 字节
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	out, err := callRead(t, map[string]interface{}{"path": p, "tail_bytes": float64(40)})
	if err != nil {
		t.Fatalf("尾段读取失败: %v", err)
	}
	if got := int(out["returned_bytes"].(float64)); got != 40 {
		t.Fatalf("returned_bytes 应为 40，实际 %d", got)
	}
	if got := int(out["offset_bytes"].(float64)); got != len(body)-40 {
		t.Fatalf("offset_bytes 应为 %d，实际 %d", len(body)-40, got)
	}
	if got := int(out["total_bytes"].(float64)); got != len(body) {
		t.Fatalf("total_bytes 应为 %d，实际 %d", len(body), got)
	}
	if out["content"].(string) != body[len(body)-40:] {
		t.Fatalf("尾段内容不对: %q", out["content"])
	}
	if !out["truncated"].(bool) {
		t.Fatal("只返回尾段时必须标记 truncated")
	}
	if !strings.Contains(out["range"].(string), "of") {
		t.Fatalf("range 应说明是文件的哪一段: %v", out["range"])
	}
}

// offset 段读取：从中间开始，返回实际起点
func TestFileReadOffset(t *testing.T) {
	root := setupSandbox(t)
	p := filepath.Join(root, "mid.txt")
	body := strings.Repeat("0123456789", 100) // 1000 字节
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := callRead(t, map[string]interface{}{"path": p, "offset_bytes": float64(500), "max_bytes": float64(100)})
	if err != nil {
		t.Fatal(err)
	}
	if out["offset_bytes"].(float64) != 500 {
		t.Fatalf("offset 应为 500，实际 %v", out["offset_bytes"])
	}
	if out["returned_bytes"].(float64) != 100 {
		t.Fatalf("returned_bytes 应为 100，实际 %v", out["returned_bytes"])
	}
	if out["content"].(string) != body[500:600] {
		t.Fatalf("offset 段内容不对: %q", out["content"])
	}
}

// offset 超界必须报错，不能静默返回空内容
func TestFileReadOffsetBeyondEOF(t *testing.T) {
	root := setupSandbox(t)
	p := filepath.Join(root, "small.txt")
	if err := os.WriteFile(p, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	res, err := handleFileRead(context.Background(), map[string]interface{}{"path": p, "offset_bytes": float64(11)})
	if err == nil {
		t.Fatalf("offset 超界必须报错，实际返回: %v", res)
	}
	if !strings.Contains(err.Error(), "超出文件大小") {
		t.Fatalf("错误应说明超出文件大小: %v", err)
	}

	// 恰好等于文件大小 = 空尾段，允许，但必须明确（returned_bytes=0 且 offset=total）
	out, err := callRead(t, map[string]interface{}{"path": p, "offset_bytes": float64(10)})
	if err != nil {
		t.Fatalf("offset == size 应允许（空尾段）: %v", err)
	}
	if out["returned_bytes"].(float64) != 0 || out["offset_bytes"].(float64) != 10 {
		t.Fatalf("空尾段字段不对: %v", out)
	}
}

// offset_bytes 与 tail_bytes 同时给必须报错
func TestFileReadOffsetAndTailConflict(t *testing.T) {
	root := setupSandbox(t)
	p := filepath.Join(root, "x.txt")
	if err := os.WriteFile(p, []byte("abcdef"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := handleFileRead(context.Background(), map[string]interface{}{
		"path": p, "offset_bytes": float64(1), "tail_bytes": float64(2),
	}); err == nil {
		t.Fatal("两个参数同时给必须报错")
	} else if !strings.Contains(err.Error(), "只能二选一") {
		t.Fatalf("错误应说明互斥: %v", err)
	}
}

// 尾段从多字节字符中间开始时，必须对齐到字符边界且不报解码失败
func TestFileReadTailUTF8Boundary(t *testing.T) {
	root := setupSandbox(t)
	p := filepath.Join(root, "cn_tail.txt")
	body := strings.Repeat("网格交易", 100) // 每字 3 字节，共 1200 字节
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	// 7 字节必然落在某个汉字的中间
	out, err := callRead(t, map[string]interface{}{"path": p, "tail_bytes": float64(7)})
	if err != nil {
		t.Fatalf("尾段切到字符中间不应报错，应回退到字符边界: %v", err)
	}
	content := out["content"].(string)
	if !utf8.ValidString(content) {
		t.Fatalf("返回内容不是合法 UTF-8: %q", content)
	}
	if !strings.HasSuffix(body, content) {
		t.Fatalf("尾段内容不是文件真后缀: %q", content)
	}
	if int(out["offset_bytes"].(float64))+len(content) != len(body) {
		t.Fatalf("offset+len 应等于文件大小: %v + %d != %d", out["offset_bytes"], len(content), len(body))
	}
}

// 中途截断：文件里含 GBK 字节，但本次读的段是纯 ASCII → 应成功；
// 读到 GBK 段 → 必须报编码错误（而不是静默给空/标截断）
func TestFileReadSegmentEncoding(t *testing.T) {
	root := setupSandbox(t)
	p := filepath.Join(root, "mixed.txt")
	head := strings.Repeat("A", 100)
	gbk := string([]byte{0xD6, 0xD0, 0xCE, 0xC4}) // 「中文」的 GBK 编码
	tail := strings.Repeat("B", 100)
	body := head + gbk + tail
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	out, err := callRead(t, map[string]interface{}{"path": p, "offset_bytes": float64(0), "max_bytes": float64(100)})
	if err != nil {
		t.Fatalf("只读头部 ASCII 段应成功: %v", err)
	}
	if out["content"].(string) != head {
		t.Fatalf("头部段内容不对")
	}

	if _, err := callRead(t, map[string]interface{}{"path": p, "offset_bytes": float64(100), "max_bytes": float64(20)}); err == nil {
		t.Fatal("读到 GBK 段必须报编码错误")
	} else if !strings.Contains(err.Error(), "编码不支持") {
		t.Fatalf("错误应为编码不支持: %v", err)
	}

	out, err = callRead(t, map[string]interface{}{"path": p, "tail_bytes": float64(100)})
	if err != nil {
		t.Fatalf("只读尾部 ASCII 段应成功: %v", err)
	}
	if out["content"].(string) != tail {
		t.Fatalf("尾部段内容不对")
	}
}
