package sqlite

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ugreen-nas-mcp/config"
)

// makeDB 用宿主 python3 造一个真实 SQLite 库（测试不依赖任何第三方 Go 包）
func makeDB(t *testing.T, rows int) string {
	t.Helper()
	dir := t.TempDir()
	db := filepath.Join(dir, "historical.db")
	script := `
import sqlite3, sys
db, n = sys.argv[1], int(sys.argv[2])
c = sqlite3.connect(db)
c.execute("create table ticks (id integer primary key, price real, symbol text)")
c.executemany("insert into ticks (price, symbol) values (?, ?)", [(1.5 * i, "BTCUSDT") for i in range(n)])
c.execute("create table agg_trades (id integer primary key, qty real)")
c.commit()
c.close()
`
	cmd := exec.Command("/usr/bin/python3", "-c", script, db, itoa(rows))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("造库失败: %v %s", err, out)
	}
	return db
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func allowDir(t *testing.T, db string) {
	t.Helper()
	dir := filepath.Dir(db)
	// macOS 上 /var 是 /private/var 的软链，沙箱按解析后的真实路径校验
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	setConfigForTest(t, dir)
}

func callSQL(t *testing.T, args map[string]interface{}) (map[string]interface{}, error) {
	t.Helper()
	res, err := handleSQLQuery(context.Background(), args)
	if err != nil {
		return nil, err
	}
	return res.(map[string]interface{}), nil
}

// 正常查询：count(*) 与字段都正确
func TestSQLSelectCount(t *testing.T) {
	db := makeDB(t, 7)
	allowDir(t, db)

	out, err := callSQL(t, map[string]interface{}{
		"db_path": db,
		"sql":     "select count(*) as n from ticks",
	})
	if err != nil {
		t.Fatalf("count(*) 应成功: %v", err)
	}
	if out["row_count"].(int) != 1 {
		t.Fatalf("应返回 1 行，实际 %v", out["row_count"])
	}
	row := out["rows"].([][]interface{})[0]
	if row[0].(float64) != 7 {
		t.Fatalf("count 应为 7，实际 %v", row[0])
	}
	if out["readonly_enforced"] == "" {
		t.Fatal("应回报只读是怎么强制的")
	}
}

// 写操作在入口就被拒（关键字层）
func TestSQLWriteStatementsRejected(t *testing.T) {
	db := makeDB(t, 3)
	allowDir(t, db)

	bad := []string{
		"insert into ticks (price, symbol) values (1, 'X')",
		"drop table ticks",
		"attach database '/tmp/evil.db' as evil",
		"update ticks set price = 0",
		"delete from ticks",
		"select 1; drop table ticks",
		"pragma table_info(ticks)",
		"with x as (select 1) delete from ticks",
	}
	for _, s := range bad {
		if _, err := callSQL(t, map[string]interface{}{"db_path": db, "sql": s}); err == nil {
			t.Errorf("写类语句必须被拒: %s", s)
		} else if !strings.Contains(err.Error(), "只读拦截") {
			t.Errorf("拒绝原因应为只读拦截: %s → %v", s, err)
		}
	}
}

// 关键一组：绕过关键字校验，直接交给底层 —— 必须由 SQLite 的 mode=ro 拒绝
func TestSQLReadonlyEnforcedAtSQLiteLayer(t *testing.T) {
	db := makeDB(t, 3)
	allowDir(t, db)

	_, err := runQuery(context.Background(), db, "insert into ticks (price, symbol) values (9, 'EVIL')", 10, 10)
	if err == nil {
		t.Fatal("底层 mode=ro 未拦住 INSERT：写操作成功了")
	}
	msg := err.Error()
	if !strings.Contains(msg, "SQLITE_READONLY") && !strings.Contains(msg, "readonly") && !strings.Contains(msg, "只读") {
		t.Fatalf("拒绝原因应指向只读数据库，实际: %v", msg)
	}

	// 数据没被写进去
	out, err := callSQL(t, map[string]interface{}{"db_path": db, "sql": "select count(*) from ticks"})
	if err != nil {
		t.Fatal(err)
	}
	if out["rows"].([][]interface{})[0][0].(float64) != 3 {
		t.Fatal("INSERT 竟然落库了，只读没生效")
	}
}

// 超时：慢查询必须在超时后返回明确错误，而不是挂住
func TestSQLTimeout(t *testing.T) {
	db := makeDB(t, 3)
	allowDir(t, db)

	start := time.Now()
	_, err := callSQL(t, map[string]interface{}{
		"db_path":         db,
		"sql":             "with recursive c(x) as (select 1 union all select x+1 from c) select count(*) from c",
		"timeout_seconds": float64(2),
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("慢查询必须超时返回错误")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("错误应说明超时: %v", err)
	}
	// 承诺：elapsed ≤ timeout + 1s（软中断路径，python 自己的 deadline 就能收住）
	if elapsed > 3*time.Second {
		t.Fatalf("超时未按承诺生效，实际耗时 %v（上限应为 2s+1s）", elapsed)
	}
	t.Logf("2s 超时实际耗时 %v，错误: %v", elapsed, err)
}

// 行数上限与 truncated 标记
func TestSQLLimitTruncates(t *testing.T) {
	db := makeDB(t, 50)
	allowDir(t, db)

	out, err := callSQL(t, map[string]interface{}{
		"db_path": db,
		"sql":     "select id, price, symbol from ticks order by id",
		"limit":   float64(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["row_count"].(int) != 5 {
		t.Fatalf("应只返回 5 行，实际 %v", out["row_count"])
	}
	if !out["truncated"].(bool) {
		t.Fatal("结果被 LIMIT 截断时必须标记 truncated")
	}
}

// 路径白名单：库文件必须在允许目录内
func TestSQLPathOutsideSandboxRejected(t *testing.T) {
	db := makeDB(t, 1)
	setConfigForTest(t, "/volume1")

	if _, err := callSQL(t, map[string]interface{}{"db_path": db, "sql": "select 1"}); err == nil {
		t.Fatal("沙箱外的库必须被拒")
	}
}

// 非 SQLite 文件必须被拒（文件头校验）
func TestSQLNonSQLiteFileRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "notdb.db")
	if err := os.WriteFile(p, []byte(strings.Repeat("A", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	allowDir(t, p)

	if _, err := callSQL(t, map[string]interface{}{"db_path": p, "sql": "select 1"}); err == nil {
		t.Fatal("非 SQLite 文件必须被拒")
	} else if !strings.Contains(err.Error(), "SQLite") {
		t.Fatalf("提示应说明不是 SQLite: %v", err)
	}
}

// 校验函数本身
func TestValidateReadOnlySQL(t *testing.T) {
	ok := []string{"select * from t", "  WITH x AS (SELECT 1) SELECT * FROM x;", "EXPLAIN QUERY PLAN SELECT 1"}
	for _, s := range ok {
		if _, err := validateReadOnlySQL(s); err != nil {
			t.Errorf("应放行: %s → %v", s, err)
		}
	}
	bad := []string{"", "insert into t values (1)", "PRAGMA writable_schema=1", "select 1; select 2", "create table x(a)"}
	for _, s := range bad {
		if _, err := validateReadOnlySQL(s); err == nil {
			t.Errorf("应拒绝: %s", s)
		}
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

// 关键一条：即使 SQLite 的进度回调完全不触发（如线上 count(*) 的 OP_Count 路径），
// Go 侧的硬中断也必须按 timeout 收住。
func TestSQLTimeoutHardKill(t *testing.T) {
	db := makeDB(t, 3)
	allowDir(t, db)

	start := time.Now()
	_, err := runQueryReq(context.Background(), queryRequest{
		DBPath:         db,
		Stmt:           "with recursive c(x) as (select 1 union all select x+1 from c) select count(*) from c",
		Limit:          10,
		TimeoutSeconds: 2,
		pythonDeadline: false, // 关掉 python 侧 deadline，模拟"回调不被调用"
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("关闭 python deadline 后，Go 硬中断必须生效")
	}
	if !strings.Contains(err.Error(), "TIMEOUT") {
		t.Fatalf("必须是 TIMEOUT 类错误: %v", err)
	}
	if !strings.Contains(err.Error(), "实测耗时") {
		t.Fatalf("超时错误应带实测耗时: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("硬中断未按承诺生效，实际耗时 %v（上限应为 2s+1s）", elapsed)
	}
	t.Logf("硬中断：timeout=2s，实测 %v，错误=%v", elapsed, err)
}
