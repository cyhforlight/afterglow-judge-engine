# 项目开发指南

> 本项目是一个学习型代码评测沙箱系统，作者具有算法竞赛背景和扎实的计算机基础，但工程经验有限。因此项目追求架构简洁、代码优雅、遵循主流最佳实践，不考虑旧技术栈兼容性。

## 权限问题

所有任务都拥有最高权限，可以直接 sudo 运行。

## 技术栈

- **Go 版本**：1.22+（使用现代语法特性）
- **运行环境**：Ubuntu 22.04 / Debian 12 或更新版本
- **代码质量工具**：golangci-lint
- **测试框架**：标准库 testing + testify/assert

## 核心原则

### 1. 语义优先

**让代码"读起来就是它做的事"**。优先选择语义最直白的写法，而不是仅仅追求"lint 通过"。代码应该让 Go 新手也能轻松理解，无需额外的心智负担。

### 2. 架构简洁

- 清晰的模块划分（cmd/internal 标准布局）
- 依赖方向单向（transport → service → model）
- 接口抽象适度（便于测试，但不过度设计）

### 3. 主流最佳实践

采用 Go 1.22+ 的主流规范写法，但不追逐实验性特性。以"大多数 Go 项目已经在用"为标准。

## 编码规范

### 错误处理

根据**语义**选择对应的方式：

| 场景 | 写法 | 示例 |
|------|------|------|
| 静态错误消息 | `errors.New(...)` | `errors.New("missing required flag: --exec")` |
| 携带动态数据 | `fmt.Errorf("...%d...", val)` | `fmt.Errorf("unsupported language: %q", raw)` |
| 包装已有错误 | `fmt.Errorf("...%w", err)` | `fmt.Errorf("failed to parse flags: %w", err)` |

**反例**：不要用 `fmt.Errorf("%s", msg)` 代替 `errors.New(msg)`——语义不清晰。

### 命名规范

- **避免遮蔽内置函数**：不使用 `min`、`max`、`cap`、`len`、`new` 等作为变量名，即使编译器允许
- **短命名 vs 长命名**：
  - 作用域小、含义明显时使用短命名：`i`、`n`、`ctx`、`err`、`ok`、`buf`
  - 存在歧义或角色不明显时使用具体名称：`limit` 而非 `cap`、`cpuLimitMs` 而非 `t`
- **不要过度 Java 化**：Go 惯用短命名，不必刻意展开

### 日志规范

**所有内部代码**使用 `log/slog` 进行结构化日志记录：

```go
// ✅ 推荐（所有内部层）
slog.InfoContext(ctx, "execution complete",
    "verdict", result.Verdict.String(),
    "timeUsed", result.TimeUsed,
)

// ❌ 避免（所有内部层）
fmt.Fprintf(os.Stderr, "execution complete: %v\n", result)
```

**HTTP 响应**使用 JSON 格式：

```go
// ✅ 推荐（HTTP 传输层）
json.NewEncoder(w).Encode(response)

// ❌ 避免（HTTP 传输层）
fmt.Fprintf(w, "result: %v\n", result)
```

**原则**：
- 所有内部代码：使用 slog（机器可读、结构化）
- HTTP 响应：使用 JSON 格式（标准 API 响应）

### 现代 Go 特性

优先使用 Go 1.22+ 的现代特性，**避免使用旧模式**：

| 现代特性 | 替代的旧模式 | 示例 |
|---------|------------|------|
| `for range N` | `for i := 0; i < n; i++` | `for range 100 { doSomething() }` |
| `math/rand/v2` | `math/rand` + `rand.Seed()` | `rand.IntN(n)` 自动加密种子 |
| 内置 `min`/`max` | 手动 if 比较 | `result := min(a, b)` |
| `slices.Contains` | 手动 for 循环 | `slices.Contains(slice, value)` |
| `log/slog` | `log.Printf` | `slog.InfoContext(ctx, "msg", "key", val)` |
| `os.ReadFile` | `ioutil.ReadFile` | `os.ReadFile(path)` |
| `clear(map)` | `for k := range m { delete(m, k) }` | `clear(m)` |

**审查要点**：
- ✅ 不仅要用现代特性，还要避免旧模式共存
- ✅ 代码审查时检查是否有可现代化的旧写法
- ✅ 优先使用语义清晰的现代 API

## 测试规范

### 测试风格

1. **表格驱动测试**：使用 `[]struct` 组织测试用例
2. **testify 断言**：使用 `assert.Equal`、`require.NoError` 等，避免大量 `if got != want`
3. **测试覆盖率**：核心逻辑应达到 70%+ 覆盖率
4. **边界测试**：重点测试临界值、零值、负数等边界情况

### 测试示例

```go
func TestParseLanguage(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    Language
        wantErr bool
    }{
        {"valid C", "C", LanguageC, false},
        {"valid C++", "C++", LanguageCPP, false},
        {"invalid", "Ruby", LanguageUnknown, true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := ParseLanguage(tt.input)
            if tt.wantErr {
                require.Error(t, err)
                return
            }
            require.NoError(t, err)
            assert.Equal(t, tt.want, got)
        })
    }
}
```

## 代码质量

### 工具链

- **格式化**：`goimports`（自动管理 import）
- **Lint**：`golangci-lint run`（配置见 `.golangci.yml`）
- **测试**：`go test -cover ./...`

### 提交前检查

```bash
# 格式化代码
goimports -w .

# 运行 lint
golangci-lint run

# 运行测试
go test -cover ./...
```

## 项目特定约定

### 资源管理

使用 cleanup stack 模式确保资源释放：

```go
var cleanups []func()
succeeded := false

addCleanup := func(fn func()) { cleanups = append(cleanups, fn) }
defer func() {
    if !succeeded {
        for i := len(cleanups) - 1; i >= 0; i-- {
            cleanups[i]()
        }
    }
}()

// ... 获取资源并 addCleanup ...

succeeded = true
return resource, rollback, nil
```

### 常量定义

使用语义化的常量名和注释：

```go
const (
    // Wall time is allowed to be this multiple of CPU time limit.
    // Accounts for I/O waits, scheduling latency, container overhead, etc.
    wallTimeMultiplier = 3

    // Treat usage >= 99.5% of the limit as hitting memory limit.
    memoryHitThresholdPermille = 995
)
```

### Verdict 优先级

判定优先级：OLE > MLE > TLE > OK > RE

确保在所有代码路径中遵循此优先级。

## 学习资源

- [Effective Go](https://go.dev/doc/effective_go)
- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)
