// Package sqlite 提供「只读 SQLite 查询」工具。
//
// 只读是**服务端强制**的，不是文档约定：
//  1. 数据库以 sqlite3 的 URI `file:<path>?mode=ro` 打开 —— 底层句柄本身就是只读的，
//     即使 SQL 里混进 INSERT，飞到底层也会报 "attempt to write a readonly database"；
//  2. 额外再执行 `PRAGMA query_only=1` 作为第二道锁；
//  3. 入口处只放行 SELECT / EXPLAIN / WITH，并拒写类关键字（黑名单是兜底，不是唯一防线）。
//
// 超时是**硬中断**：Go 侧到点直接 SIGTERM→SIGKILL 杀掉 python 子进程，不依赖 SQLite 自己发现超时。
// 原因：`set_progress_handler` 的回调只在 VDBE 逐行步进时被调用；`select count(*)` 会被 SQLite
// 编译成 `OP_Count`（页级计数，直接走 b-tree 页结构），**整条路径 0 次回调**（本地实测：
// 12M 行 count(*) 0.044s / 0 次回调，而 count(1) 同一张表 1800 次）——所以对 count(*) 这类
// 查询，进程内的 deadline 永远不会被观察到。线上 64.8 秒那次就是这条路径。
//
// 实现方式：调用宿主机 python3（自带 sqlite3 模块）做查询，Go 负责校验与硬超时。
// 这样保持本项目「零第三方依赖」：不引入 modernc.org/sqlite（纯 Go 但会把二进制从
// 7.4 MB 涨到 20 MB+），也不用 cgo（静态交叉编译会痛）。
// 代价：宿主必须有 python3（UGOS Pro / Debian 12 基线自带 /usr/bin/python3）。
// 缺失时工具返回明确错误，不影响其余 22 个工具。
package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"ugreen-nas-mcp/protocol"
	"ugreen-nas-mcp/security"
)

const (
	// 默认 60s：实测 historical.db 的 count(*) 要 36.3s，5~30s 会让工具本身变成废物
	defaultTimeoutSeconds = 60
	// 硬上限 90s：够跑大表统计，又不至于一个长查询把 NAS 的 IO 占满
	maxTimeoutSeconds = 90
	defaultRowLimit   = 200
	maxRowLimit       = 1000
	// 硬中断余量：python 自己的 deadline 在 timeout 处见效（报错更干净），
	// Go 在 timeout+此值时无条件 SIGTERM/SIGKILL，保证 elapsed ≤ timeout + 0.5s。
	hardKillGrace = 400 * time.Millisecond
	// SIGTERM 后等待自行退出的时间，超时则 SIGKILL（python 一般毫秒级退出）
	sigtermWait = 2 * time.Second
	// 单行单元格文本上限，防止一个 TEXT 列把响应撑爆
	maxCellChars = 2048
)

// 串行化：同一时刻只跑一条 SQL，避免并发全表扫描把 NAS 磁盘打满
var queryMutex sync.Mutex

var writeKeywordRe = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|REPLACE|CREATE|DROP|ALTER|ATTACH|DETACH|PRAGMA|VACUUM|REINDEX|ANALYZE|BEGIN|COMMIT|ROLLBACK|END|SAVEPOINT|RELEASE|TRUNCATE|GRANT|REVOKE)\b`)

var leadingKeywordRe = regexp.MustCompile(`(?is)^\(?\s*(SELECT|EXPLAIN|WITH)\b`)

func Register(r *protocol.ToolRegistry) {
	r.Register(protocol.Tool{
		Name: "nas_sql_query",
		Description: "【只读】对 NAS 上的 SQLite 库执行只读查询（底层以 mode=ro 打开 + PRAGMA query_only=1，仅允许 SELECT/EXPLAIN/WITH）。" +
			"默认服务端超时 60s（上限 90s）、行数上限 200（上限 1000）。不写库、不建表、不 ATTACH。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"db_path": map[string]interface{}{
					"type":        "string",
					"description": "SQLite 数据库绝对路径，如 /volume1/Agent/hermes/grid-trading/data/historical.db",
				},
				"sql": map[string]interface{}{
					"type":        "string",
					"description": "单条只读 SQL（SELECT / EXPLAIN / WITH ... SELECT），不要带写操作与多语句",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "最多返回行数，默认 200，上限 1000",
				},
				"timeout_seconds": map[string]interface{}{
					"type":        "integer",
					"description": "服务端查询超时秒数，默认 60，上限 90；超时返回明确错误而不是挂住",
				},
			},
			"required": []string{"db_path", "sql"},
		},
	}, handleSQLQuery)
}

func handleSQLQuery(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	dbPath, _ := args["db_path"].(string)
	if strings.TrimSpace(dbPath) == "" {
		return nil, fmt.Errorf("db_path 不能为空")
	}
	sqlText, _ := args["sql"].(string)
	if strings.TrimSpace(sqlText) == "" {
		return nil, fmt.Errorf("sql 不能为空")
	}

	guardian := security.GetGuardian()
	if err := guardian.CheckCredentialDeny(dbPath); err != nil {
		return nil, err
	}
	// 按解析后的真实路径做沙箱校验（软链不能把数据库读取带出白名单）
	resolved, err := filepath.EvalSymlinks(dbPath)
	if err != nil {
		return nil, fmt.Errorf("解析数据库路径失败: %v", err)
	}
	if err := guardian.CheckPathSandbox(resolved); err != nil {
		return nil, err
	}
	if err := guardian.CheckCredentialDeny(resolved); err != nil {
		return nil, err
	}

	fi, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %v", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("目标不是普通文件: %s", resolved)
	}
	if fi.Size() < 16 {
		return nil, fmt.Errorf("文件太小，不是 SQLite 数据库: %s (%d 字节)", resolved, fi.Size())
	}
	if err := checkSQLiteHeader(resolved); err != nil {
		return nil, err
	}

	stmt, err := validateReadOnlySQL(sqlText)
	if err != nil {
		return nil, err
	}

	limit := defaultRowLimit
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	if limit > maxRowLimit {
		limit = maxRowLimit
	}

	timeoutSec := defaultTimeoutSeconds
	if v, ok := args["timeout_seconds"].(float64); ok && v > 0 {
		timeoutSec = int(v)
	}
	if timeoutSec > maxTimeoutSeconds {
		timeoutSec = maxTimeoutSeconds
	}

	return runQuery(ctx, resolved, stmt, limit, timeoutSec)
}

// checkSQLiteHeader 校验文件头是否为 "SQLite format 3\x00"，避免把任意文件当库打开
func checkSQLiteHeader(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("打开数据库失败: %v", err)
	}
	defer f.Close()

	head := make([]byte, 16)
	n, _ := f.Read(head)
	if n < 16 || string(head) != "SQLite format 3\x00" {
		return fmt.Errorf("目标不是有效的 SQLite 数据库（文件头不匹配）: %s", path)
	}
	return nil
}

// validateReadOnlySQL 只放行 SELECT / EXPLAIN / WITH，单语句，且不含写类关键字
func validateReadOnlySQL(sqlText string) (string, error) {
	stmt := strings.TrimSpace(sqlText)
	for strings.HasSuffix(stmt, ";") {
		stmt = strings.TrimSpace(strings.TrimSuffix(stmt, ";"))
	}
	if stmt == "" {
		return "", fmt.Errorf("只读拦截：SQL 为空")
	}
	if !leadingKeywordRe.MatchString(stmt) {
		return "", fmt.Errorf("只读拦截：只允许 SELECT / EXPLAIN / WITH 开头的查询语句")
	}
	if strings.Contains(stmt, ";") {
		return "", fmt.Errorf("只读拦截：不允许一次提交多条语句")
	}
	if kw := writeKeywordRe.FindString(stmt); kw != "" {
		return "", fmt.Errorf("只读拦截：语句中出现写操作关键字 %s，本工具只执行只读查询", strings.ToUpper(kw))
	}
	return stmt, nil
}

// runQuery 执行查询。limit / timeout 已由调用方归一化。
// queryRequest 是一次只读查询的全部参数。
type queryRequest struct {
	DBPath         string
	Stmt           string
	Limit          int
	TimeoutSeconds int
	// pythonDeadline=false 仅供自测：让 python 侧不装 deadline，
	// 用于证明「即使 SQLite 回调完全不触发，Go 的硬中断仍然生效」。
	pythonDeadline bool
}

func runQuery(ctx context.Context, dbPath, stmt string, limit, timeoutSec int) (map[string]interface{}, error) {
	return runQueryReq(ctx, queryRequest{
		DBPath:         dbPath,
		Stmt:           stmt,
		Limit:          limit,
		TimeoutSeconds: timeoutSec,
		pythonDeadline: true,
	})
}

func runQueryReq(ctx context.Context, req queryRequest) (map[string]interface{}, error) {
	pythonBin, err := findPython()
	if err != nil {
		return nil, err
	}

	queryMutex.Lock()
	defer queryMutex.Unlock()

	pythonTimeout := 0 // 0 = python 侧不装 deadline（仅自测使用）
	if req.pythonDeadline {
		pythonTimeout = req.TimeoutSeconds
	}
	args := []string{"-I", "-B", "-c", pythonBridge,
		req.DBPath, strconv.Itoa(req.Limit), strconv.Itoa(pythonTimeout)}

	cmd := exec.Command(pythonBin, args...)
	cmd.Stdin = strings.NewReader(req.Stmt)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONDONTWRITEBYTECODE=1")
	// 独立进程组：kill 时连子进程一起收掉
	cmd.SysProcAttr = sysProcAttr()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	started := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动只读查询进程失败: %v", err)
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	hardDeadline := time.Duration(req.TimeoutSeconds)*time.Second + hardKillGrace
	timer := time.NewTimer(hardDeadline)
	defer timer.Stop()

	var runErr error
	hardKilled := false
	select {
	case runErr = <-waitDone:
		// 正常结束，或用 python 自己的 deadline 中止（报错更干净）
	case <-timer.C:
		hardKilled = true
		signalGroup(cmd, syscallTerm())
		select {
		case runErr = <-waitDone:
		case <-time.After(sigtermWait):
			signalGroup(cmd, syscallKill())
			runErr = <-waitDone
		}
	case <-ctx.Done():
		hardKilled = true
		signalGroup(cmd, syscallKill())
		runErr = <-waitDone
	}
	wall := time.Since(started)

	if hardKilled {
		return nil, timeoutError(req.TimeoutSeconds, wall)
	}

	if stdout.Len() == 0 {
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		return nil, fmt.Errorf("只读查询进程无输出（退出码 %v）: %s", runErr, tail)
	}

	var p struct {
		Error          string          `json:"error"`
		ErrorClass     string          `json:"error_class"`
		Columns        []string        `json:"columns"`
		Rows           [][]interface{} `json:"rows"`
		RowCount       int             `json:"row_count"`
		Truncated      bool            `json:"truncated"`
		Limit          int             `json:"limit"`
		TimeoutSeconds float64         `json:"timeout_seconds"`
		ElapsedMS      int             `json:"elapsed_ms"`
		Readonly       string          `json:"readonly"`
		DBPath         string          `json:"db_path"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &p); err != nil {
		return nil, fmt.Errorf("解析查询结果失败: %v", err)
	}
	if p.Error != "" {
		if p.ErrorClass == "TIMEOUT" {
			// 把 Go 侧实测墙钟时间附上，避免"说是超时但不知道超了多久"
			return nil, fmt.Errorf("TIMEOUT: %s（实测耗时 %.2fs，上限 %ds）", p.Error, wall.Seconds(), req.TimeoutSeconds)
		}
		return nil, fmt.Errorf("%s: %s", p.ErrorClass, p.Error)
	}

	return map[string]interface{}{
		"db_path":            p.DBPath,
		"columns":            p.Columns,
		"rows":               p.Rows,
		"row_count":          p.RowCount,
		"truncated":          p.Truncated,
		"limit":              p.Limit,
		"timeout_seconds":    p.TimeoutSeconds,
		"elapsed_ms":         p.ElapsedMS,
		"readonly_enforced":  p.Readonly,
		"write_ops_rejected": true,
	}, nil
}

// timeoutError 统一超时文案：带上实测耗时，便于调用方核对"承诺是否兑现"。
func timeoutError(timeoutSec int, wall time.Duration) error {
	return fmt.Errorf("TIMEOUT: 查询超过服务端上限 %ds（实测耗时 %.2fs，已硬中断 SIGTERM/SIGKILL），未返回任何数据",
		timeoutSec, wall.Seconds())
}

// findPython 依次尝试：环境变量覆盖 → 常见绝对路径 → PATH 查找
func findPython() (string, error) {
	candidates := []string{}
	if env := strings.TrimSpace(os.Getenv("UGREEN_MCP_PYTHON")); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates, "/usr/bin/python3", "/usr/local/bin/python3", "/opt/homebrew/bin/python3")
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.Mode().IsRegular() {
			return c, nil
		}
	}
	if p, err := exec.LookPath("python3"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("只读 SQL 需要宿主 python3（自带 sqlite3 模块），未找到；可用环境变量 UGREEN_MCP_PYTHON 指定路径")
}

// pythonBridge 在宿主机 python3 上执行：mode=ro 打开 + query_only + 进度回调超时 + 行数截断。
// SQL 从 stdin 读取（不进 argv，避免 ps 里看到查询内容）。
var pythonBridge = strings.ReplaceAll(pythonBridgeTmpl, "__MAX_CELL__", strconv.Itoa(maxCellChars))

const pythonBridgeTmpl = `
import json, math, os, sqlite3, sys, time, urllib.parse

# systemd 下 LANG 可能为空，默认 stdout 编码会退化成 ascii，
# 写中文错误信息时会 UnicodeEncodeError；这里强制 UTF-8。
try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

def emit(obj, code=0):
    sys.stdout.write(json.dumps(obj, ensure_ascii=False))
    sys.stdout.flush()
    os._exit(code)

def fail(msg, cls):
    emit({"error": msg, "error_class": cls}, 2)

db_path = sys.argv[1]
limit = int(sys.argv[2])
timeout_s = float(sys.argv[3])
sql = sys.stdin.read().strip()
if not sql:
    fail("SQL 为空", "EMPTY_SQL")

uri = "file:" + urllib.parse.quote(db_path) + "?mode=ro"
try:
    conn = sqlite3.connect(uri, uri=True, timeout=5.0)
except sqlite3.Error as e:
    fail("打开数据库失败（只读模式）: %s" % e, "OPEN_FAILED")

try:
    conn.execute("PRAGMA query_only=1")
except sqlite3.Error as e:
    fail("无法锁定只读会话: %s" % e, "READONLY_LOCK_FAILED")

# timeout_s <= 0 表示不装 deadline（仅自测用：验证 Go 侧硬中断不依赖 SQLite 回调）。
# 注意：这个 deadline 只能算"软"的——select count(*) 走 OP_Count 页级计数，
# 整个扫描过程中进度回调一次都不会被调用，所以真正的上限由 Go 侧 kill 保证。
if timeout_s > 0:
    deadline = time.monotonic() + timeout_s

    def on_progress():
        return 1 if time.monotonic() > deadline else 0

    conn.set_progress_handler(on_progress, 20000)

stmt = sql.strip()
while stmt.endswith(";"):
    stmt = stmt[:-1].rstrip()

head = stmt.lstrip("(").lstrip().upper()
# 只有 SELECT / WITH 才包一层 LIMIT（在 SQLite 内截断，不把百万行拉进内存）；
# EXPLAIN 不能包；其余语句原样交给 SQLite——mode=ro 会直接报"只读数据库"。
if head.startswith("EXPLAIN") or not (head.startswith("SELECT") or head.startswith("WITH")):
    wrapped = stmt
else:
    wrapped = "SELECT * FROM (\n" + stmt + "\n) LIMIT %d" % (limit + 1)

t0 = time.monotonic()
try:
    cur = conn.execute(wrapped)
    rows = cur.fetchall()
    cols = [d[0] for d in (cur.description or [])]
except sqlite3.OperationalError as e:
    msg = str(e)
    low = msg.lower()
    if "interrupted" in low or "cancel" in low:
        fail("查询超时：超过服务端上限 %.0f 秒，已中断" % timeout_s, "TIMEOUT")
    if "readonly" in low or "read-only" in low or "read only" in low or "attempt to write" in low:
        fail("只读拦截：数据库以 mode=ro 打开，写操作在 SQLite 层即被拒绝（%s）" % msg, "SQLITE_READONLY")
    if "not a database" in low or "encrypted" in low or "malformed" in low:
        fail("目标不是可读的 SQLite 数据库：%s" % msg, "NOT_A_DB")
    fail("SQL 执行失败：%s" % msg, "SQL_ERROR")
except sqlite3.Error as e:
    fail("SQL 执行失败：%s" % e, "SQL_ERROR")
finally:
    try:
        conn.close()
    except Exception:
        pass

elapsed_ms = int((time.monotonic() - t0) * 1000)
truncated = len(rows) > limit
rows = rows[:limit]

def enc(v):
    if isinstance(v, (bytes, bytearray, memoryview)):
        b = bytes(v)
        return {"__binary_hex": b[:64].hex(), "size_bytes": len(b)}
    if isinstance(v, float) and (math.isnan(v) or math.isinf(v)):
        return str(v)
    if isinstance(v, str) and len(v) > __MAX_CELL__:
        return {"__truncated_text": v[:__MAX_CELL__], "size_chars": len(v)}
    return v

emit({
    "db_path": db_path,
    "columns": cols,
    "rows": [[enc(v) for v in r] for r in rows],
    "row_count": len(rows),
    "truncated": truncated,
    "limit": limit,
    "timeout_seconds": int(timeout_s),
    "elapsed_ms": elapsed_ms,
    "readonly": "file:<path>?mode=ro + PRAGMA query_only=1",
})
`
