# Code Quality Audit

本报告基于当前仓库代码，对照项目定位和 `AGENTS.md` 中的开发约束，审查架构优雅度、代码质量、测试诚实性和质量门禁落地情况。

审查时间：2026-06-21

## 总体结论

这个项目不是典型的 AI 模板堆叠代码。当前分层基本清楚，依赖方向整体保持为：

```text
transport -> service -> execution -> sandbox
                  \-> model / resource
```

核心判题流程也比较直接：HTTP 层做输入边界，service 层编排编译、运行、checker 和结果聚合，execution 层统一临时工作目录、资源限制、sandbox 调用、产物收集和容器级限流，sandbox 层封装 containerd 执行，resource 层负责只读资源访问。

但项目离“优雅、整洁、可学习、质量门禁可信”的目标还有距离。主要风险集中在：

- 同步接口的请求规模和超时预算需要继续校准
- 请求取消时容器生命周期处理不够可靠
- sandbox 安全策略偏宽松
- 外部资源缺少文件大小和单次请求总量上限
- sandbox 容器生命周期和清理路径仍需要更多验证

## 高优先级问题

### 1. 同步接口预算需要校准

位置：`internal/transport/httptransport/server.go`

`/v1/execute` 是同步接口，一次请求可能包含：

- 用户代码编译
- checker 编译
- 多测试点执行
- 每个测试点的 checker 运行

HTTP 写超时、单测试点 CPU 时间、测试点数量和容器并发之间需要保持一致，否则同步接口容易在规模稍大时表现得不稳定。

改进方向：

- 根据 `MAX_TIME_LIMIT_MS`、`MAX_TEST_CASES` 和容器并发配置继续校准 `HTTP_WRITE_TIMEOUT_MS`
- 如果要支持长时间评测，把 `/v1/execute` 演进为异步 job 模型

### 2. 请求取消时没有立即杀容器

位置：`internal/sandbox/containerd.go`

`watchExecution` 当前只等待：

- task 正常退出
- 输出超限
- wall time 超时

没有显式监听 `ctx.Done()`。如果 HTTP 请求取消、服务关闭或上游超时，sandbox 不会立刻进入 kill 分支。

同时，container 和 task 的清理逻辑复用了传入的 `ctx`：

```go
addCleanup(func() { _ = container.Delete(ctx, containerd.WithSnapshotCleanup) })
addCleanup(func() { _, _ = task.Delete(ctx) })
```

如果 `ctx` 已取消，清理操作可能失败，而且错误被直接吞掉。

改进方向：

- 在 `watchExecution` 的 select 中加入 `case <-ctx.Done()`
- 请求取消时先 kill task，再等待退出
- 清理使用独立的 `cleanupCtx := context.WithTimeout(context.Background(), ...)`
- 对清理失败至少打 debug/warn 日志，避免容器或 snapshot 泄漏难排查

### 3. sandbox seccomp 策略偏宽松

位置：`internal/sandbox/containerd.go`

当前 seccomp 使用的是黑名单策略：

```go
DefaultAction: specs.ActAllow
```

只禁止少量网络、进程创建、mount、ptrace 等 syscall。对“执行不可信用户代码”的系统来说，这个策略偏宽。

另外，编译阶段关闭 seccomp：

```go
EnableSeccomp: false
```

理由是编译需要 fork，但关闭 seccomp 同时放开了更多不必要能力。

改进方向：

- 用户代码运行阶段改为 allowlist 型 seccomp profile
- 编译阶段也使用单独 profile：允许编译器需要的 fork/exec/file 操作，但继续禁止网络、mount、ptrace 等

### 4. 外部资源缺少大小和总量上限

位置：`internal/service/judge_service.go`、`internal/resource/external.go`

请求参数本身有时间、内存、测试点数量和源码大小上限，但外部资源读取仍主要依赖路径安全检查：

```text
inputFile
expectedOutputFile
external:<checker>.cpp
```

这能防路径穿越和 symlink escape，但不能限制单个外部文件大小、单次判题总输入量、总 expected output 量或外部 checker 源码大小。内部受控系统里这个风险可接受，但它仍是同步接口资源预算的一部分。

改进方向：

- 限制单个外部文件大小
- 限制单次判题加载的输入和答案总字节数
- 限制外部 checker 源码大小
- 将这些限制纳入 `JudgeLimits` 或单独的外部资源策略

## 中优先级问题

### 5. 每个测试点直接启动 goroutine

位置：`internal/service/judge_service.go`

当前 `runAllCases` 对所有 testcase 直接起 goroutine。容器执行由 semaphore 限制，但 goroutine 数量和外部文件加载没有限制。

如果请求包含大量 testcase，会带来：

- goroutine 数量膨胀
- 同时读外部文件造成 I/O 压力
- 更高的内存峰值

改进方向：

- 使用 worker pool
- worker 数量可以复用 `MaxConcurrentContainers` 或单独配置
- 保持结果数组按 testcase 原顺序写回

## 架构评价

### 做得好的地方

- 没有明显过度分层，没有堆 `manager`、`processor`、`repository` 等空壳抽象
- HTTP DTO 和领域模型分离，边界比较清楚
- `Compiler`、`Runner`、`Sandbox` 的接口抽象有测试价值，不算过度设计
- `execution.Executor` 把编译和运行共享的 workspace/sandbox/artifact 流程收敛到一处，减少了 primitive 之间的重复逻辑
- 容器并发限流集中到 `NewThrottledExecutor`，比 compiler/runner 各自套限流更清楚
- checker 编译缓存用 decorator 形式接入，主流程没有被缓存细节污染
- external resource 做了路径 normalize 和 symlink escape 检查，方向正确
- 测试没有使用 `testing.Short()` 跳过集成/E2E，符合项目约束

### 需要收敛的地方

- `JudgeEngine` 同时负责请求级并发、测试点并发、测试数据加载、编译、运行、checker 和聚合，长期看会变胖
- `JudgeEngine.concurrencySem` 和 `execution.NewThrottledExecutor` 是两层限流，语义需要文档化：前者限制判题请求，后者限制实际容器执行
- `cachedCompiler` 的 cache key 忽略 `ArtifactName` 和 resource limits，代码注释说明了这个边界，但这仍依赖“这些字段不影响编译产物”的约定
- sandbox 的错误分类和资源清理仍是最复杂、最需要测试覆盖的部分

## 测试和质量门禁

推荐的日常门禁命令：

```bash
goimports -w .
golangci-lint config verify
golangci-lint run ./...
go test ./...
go test -cover ./...
```

参考验证命令：

```bash
go test -race ./internal/service ./internal/transport/httptransport ./internal/resource ./internal/workspace ./internal/cache ./internal/model ./internal/execution
sudo -n --preserve-env=HTTP_PROXY,HTTPS_PROXY,http_proxy,https_proxy,NO_PROXY,no_proxy \
  env GOMODCACHE=/tmp/afterglow-root-gomod-e2e GOCACHE=/tmp/afterglow-root-gocache-e2e \
  go test -count=1 ./internal/transport/httptransport -run '^TestE2E_HTTP_ExternalCases$' -v
```

参考结果：

- `go test -cover ./...` 通过
- `go test -race ...` 通过
- sudo HTTP E2E 通过
- `golangci-lint config verify` 通过
- `golangci-lint run ./...` 通过，当前为 `0 issues`

覆盖率概况：

```text
internal/config                  81.2%
internal/execution               83.6%
internal/model                   65.5%
internal/resource                81.7%
internal/sandbox                 44.8%
internal/service                 79.2%
internal/transport/httptransport 91.0%
internal/workspace               77.3%
```

`execution` 层有较高单元测试覆盖，说明共享 primitive 不是空壳抽象。`sandbox` 的 verdict、metrics、output limiter 和 spec 边界有较多纯逻辑测试；主要短板在真实 containerd 生命周期路径，例如：

- cleanup 行为的 fake task/container 测试
- 请求取消时的 kill/wait 顺序
- cleanup timeout 和日志语义

## golangci-lint 状态

当前 `.golangci.yml` 是可执行门禁，而不是理想化清单。配置使用 golangci-lint v2 schema，`golangci-lint config verify` 和 `golangci-lint run ./...` 均能通过。

配置取舍比较符合当前项目状态：

- 保留 `govet`、`staticcheck`、`errcheck`、`errorlint`、`testifylint`、`gosec` 等高信号检查
- 保留复杂度类检查，但对测试代码放宽 `funlen`、`gocognit`、`gocyclo`、`goconst` 等风格限制
- 不启用 `ireturn`、`testpackage` 这类会和当前边界表达冲突的规则
- `sloglint` 只检查参数风格，不强制全项目改成 context logger 或禁止默认 logger

当前保留一处窄范围 `nolint:gosec`：`sandbox` 构造 OCI memory limit 时，前置 helper 保证正数和不溢出，行内例外只用于说明静态分析无法跨 helper 推断这一点。它属于合理例外，不是为了掩盖设计问题。

如果在 Codex 受限 shell 中直接运行 `golangci-lint` 出现 `no go files to analyze`，更像是工具运行环境问题，不是当前代码或配置问题；在普通 shell 或 Codex escalated 执行环境中门禁可以稳定运行。

## containerd 安装说明

当前系统 apt 源里存在 Ubuntu 官方包：

```bash
sudo apt update
sudo apt install containerd
sudo systemctl enable --now containerd
sudo ctr version
```

确认 cgroup v2：

```bash
test -f /sys/fs/cgroup/cgroup.controllers && echo cgroup-v2-ok
```

`containerd.io` 是 Docker 官方 apt 源里的包名，不是 Ubuntu 默认源里的包名。对本项目来说，Ubuntu 官方源中的 `containerd` 通常足够。

## 演进优先级

1. 修复 sandbox 取消、kill 和 cleanup 语义
2. 收紧 seccomp 策略
3. 补外部资源大小和单次请求总量上限
4. 将 testcase 并发改为 worker pool
5. 提高 sandbox 生命周期路径测试覆盖

## 最终评价

当前代码“可用且不臃肿”，架构方向比常见 Vibe Coding 项目更健康。它的问题不是抽象太多，而是运行时边界还不够工程化：超时预算校准、取消、资源限制细化、安全策略、sandbox 生命周期测试这些地方需要继续打磨。

如果目标是学习型 Go 工程项目，下一步不应继续堆功能，而应优先把这些边界打磨清楚。这会比增加新语言、新 API 或新存储后端更能提升项目质量。
