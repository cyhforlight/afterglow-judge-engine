# Afterglow Judge Engine

一个基于 containerd 的代码评测引擎。它接收源代码和多组测试数据，完成编译、隔离执行、输出比对和聚合判定，并通过 HTTP 提供统一入口。

这个项目当前阶段更准确地说是一个**单机同步判题引擎**：一次 HTTP 请求完成编译、运行、checker 校验和结果聚合。它未来可以演进为异步评测 worker 或更完整的内部评测微服务，但当前代码没有引入队列、数据库、任务状态机或多 worker 调度。

这个项目的默认定位不是公网开放平台，而是大型项目中的内部评测组件。因此整体设计强调边界清晰、实现简洁、可维护性优先，不追求“通用平台化”的过度包装。

这个项目还有一个同样重要的原始目的：作为 Go 语言工程实践的入门学习和训练项目。也正因此，这个仓库不仅关注”功能是否可用”，也同样重视项目风格是否优雅、架构是否清晰、代码是否易读易学、是否符合主流 Go 风格、测试是否诚实、规范是否便于长期演进。

## 设计工作场景

这个项目通常被设想为大型 OJ、命题系统或训练平台中的一个组成服务，而不是直接面向最终用户的公网 API。

这意味着它的典型调用方是受控的上游系统，而不是任意外部客户端。因此在安全边界上，需要重点防御的是用户提交的 `sourceCode` 及其编译运行过程；至于 HTTP 调用方式、字段组合和接入形态，本质上属于内部系统之间的受控交互，不必为了“假想中的开放平台场景”额外堆叠过度复杂的安全设计。

## 项目目标

- 提供一个简单直接的 HTTP 评测入口
- 支持多语言编译与隔离执行
- 支持多测试点、逐点 verdict 和判题流程状态
- 支持内置 checker，以及基于外部文件的测试数据和 checker

## 当前实现范围

当前代码实现了最核心的一条评测链路：

1. HTTP 层接收并校验请求
2. service 层加载测试数据、解析 checker、编译用户代码
3. execution 层准备临时工作目录、调用 sandbox 并收集编译产物
4. 对每个测试点执行程序并运行 checker
5. 汇总逐点结果和判题流程状态

更大规模的能力目前没有展开实现，例如异步任务队列、评测结果持久化和多 worker 调度等。

## 特性

- 受限执行：基于 containerd、cgroup 和 seccomp 运行编译与执行流程
- 多语言：C / C++ / Java / Python
- 多测试点：逐点评测并返回明细
- 多种判定：`OK` / `WrongAnswer` / `CompileError` / `TimeLimitExceeded` / `MemoryLimitExceeded` / `OutputLimitExceeded` / `RuntimeError` / `UnknownError`
- Checker 支持：
  - 内置 checker：`default`、`ncmp`、`wcmp`、`fcmp`、`yesno`、`nyesno`、`lcmp`、`hcmp`、`rcmp4`、`rcmp6`、`rcmp9`
  - 外部 checker：`external:<relative-path>.cpp`
- 测试数据支持：
  - 直接在请求体中传 `inputText` / `expectedOutputText`
  - 通过 `inputFile` / `expectedOutputFile` 引用外部文件
- HTTP 边界保护：
  - 请求体大小限制
  - 单次请求的时间、内存、测试点数量和源码大小上限
  - 严格 JSON 解码，拒绝未知字段

## 安全边界

本项目使用 containerd、cgroup、只读 rootfs、能力裁剪和 seccomp 黑名单来约束用户程序运行。它适合学习和内部受控系统场景，但不应被理解为公网级强隔离沙箱。

当前需要诚实看待的边界：

- 编译阶段没有启用 seccomp，因为编译器和语言工具链需要创建进程
- 运行阶段的 seccomp 仍是黑名单策略，不是完整 allowlist
- 请求取消会立即停止 task；kill、wait 和 cleanup 使用独立的有界 context，避免请求取消阻断资源释放
- 外部测试数据和外部 checker 只从 `EXTERNAL_DATA_DIR` 指定的资源根目录读取，并做路径穿越和 symlink escape 检查

## 快速开始

### 运行前提

- Linux 环境：Ubuntu 22.04 / Debian 12 或更新版本
- cgroup v2
- 可用的 containerd
- root 权限或等价权限
- x64 CPU 架构

### 构建与启动

直接在仓库根目录构建和启动：

```bash
go build -o server ./cmd/server
./server
```

默认情况下：

- 内置 checker、`testlib.h` 等 internal resources 会在构建时 embed 进二进制，运行时不需要额外放在可执行文件旁边
- 编译和运行镜像在首次使用时按需拉取
- 外部测试数据和外部 checker 默认关闭；如需启用，可通过 `EXTERNAL_DATA_DIR` 显式指定根目录

因此最简单的用法仍然是在仓库根目录直接构建并运行。

如果需要使用 `inputFile` / `expectedOutputFile` 或 `external:<path>.cpp`，再额外配置：

```bash
export EXTERNAL_DATA_DIR=/absolute/path/to/testdata
```

### 调用评测 API

```bash
curl -X POST http://localhost:8080/v1/execute \
  -H "Content-Type: application/json" \
  -d '{
    "sourceCode": "import sys\nn=int(sys.stdin.readline())\nprint(n*2)",
    "checker": "default",
    "language": "Python",
    "timeLimit": 1000,
    "memoryLimit": 256,
    "testcases": [
      {"inputText": "21\n", "expectedOutputText": "42\n"},
      {"inputText": "7\n", "expectedOutputText": "14\n"}
    ]
  }'
```

## 架构

### 分层设计

当前实现采用一条比较克制的分层链路：

- `transport/httptransport`
  - 负责 HTTP 路由、鉴权、请求体大小限制、JSON 解码、调用 service 和响应编码
- `service`
  - 负责完整判题流程编排：加载测试数据、解析 checker、编译、执行、校验、汇总逐点结果和判题流程状态
- `execution`
  - 负责通用容器执行任务：准备临时 workspace、写入文件、表达资源限制、调用 sandbox、收集产物，并集中限制容器并发
- `sandbox`
  - 负责通过 containerd 在受限环境中执行编译和运行动作
- `resource`
  - 负责内部资源（预置 checker）和外部资源（测试数据、题目自定义 Checker 等）的只读访问
- `model`
  - 负责判题请求、结果和枚举类型，并承载当前 HTTP API 的 JSON 字段约定

依赖方向保持单向：

```text
transport -> service -> model
                    -> resource
                    -> execution -> sandbox
```

### 请求处理流程

一次 `POST /v1/execute` 的处理流程如下：

1. HTTP 层限制请求体大小并做严格 JSON 解码
2. transport 单次调用 service；service 校验请求限制、checker 引用和外部资源是否可用
3. 请求通过校验后，service 限制并发判题请求数
4. service 解析语言、编译用户代码并准备已解析的 checker
5. 编译成功后，各 testcase 按需加载 `inputFile` / `expectedOutputFile`
6. compiler / runner 通过 execution 层执行用户程序和 checker；容器并发由 execution 层统一限制
7. service 汇总逐点结果和判题流程状态
8. transport 将未受理错误映射为 HTTP 400，或以 HTTP 200 返回判题结果

### 目录结构

```text
cmd/
└── server/                     HTTP 服务入口

internal/
├── config/                     环境变量配置加载
├── execution/                  通用容器执行任务、资源限制、内部 workspace 和产物收集
├── model/                      领域模型（JudgeRequest / JudgeResult / Verdict）
├── resource/                   内置资源和外部文件的只读访问
├── sandbox/                    containerd 沙箱适配层
├── service/                    编译、运行、checker、判题编排和 checker 编译缓存
└── transport/httptransport/    HTTP server / handler / middleware

support/
├── testlib.h                   编译进二进制的内置 checker 依赖
└── checkers/                   编译进二进制的内置 checker 源码

testdata/
└── ...                         外部测试数据、外部 checker、E2E 用例
```

## HTTP API

### `POST /v1/execute`

请求头：

```http
POST /v1/execute
Content-Type: application/json
```

请求体字段：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `sourceCode` | string | 是 | 源代码文本 |
| `language` | string | 是 | `C` / `C++` / `Java` / `Python` |
| `timeLimit` | int | 是 | 单测试点 CPU 时间限制，单位毫秒 |
| `memoryLimit` | int | 是 | 单测试点内存限制，单位 MB；Java 中对应最大堆容量（`-Xmx`） |
| `checker` | string | 否 | 内置 checker 短名，或 `external:<path>.cpp` |
| `testcases` | array | 是 | 测试点列表 |

单个 testcase 字段：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `inputText` | string | 否 | 直接传入输入文本 |
| `expectedOutputText` | string | 否 | 直接传入标准输出文本 |
| `inputFile` | string | 否 | 相对于 `testdata/` 的输入文件路径 |
| `expectedOutputFile` | string | 否 | 相对于 `testdata/` 的标准输出文件路径 |

约束：

- testcase 必须完整使用文本或文件：`inputText` / `expectedOutputText` 与 `inputFile` / `expectedOutputFile` 不能交叉使用
- 文件型 testcase 必须同时提供 `inputFile` 和 `expectedOutputFile`
- 请求体必须是且只能是一个 JSON 对象
- 未知字段会被直接拒绝
- Java 的 JVM 堆外开销由 Judge Engine 额外预留，不从 `memoryLimit` 中扣除
- `memoryUsed` 表示容器整体内存峰值；Java 中包含 JVM 堆外内存，可能高于 `memoryLimit`
- Judge Engine 会在达到 `timeLimit` 时主动停止任务，并以三倍 wall time 作为阻塞和休眠程序的生命周期兜底

文本型 testcase 示例：

```json
{
  "sourceCode": "#include <iostream>\nint main(){int a,b;std::cin>>a>>b;std::cout<<a+b<<\"\\n\";}\n",
  "language": "C++",
  "timeLimit": 1000,
  "memoryLimit": 256,
  "checker": "default",
  "testcases": [
    {
      "inputText": "1 2\n",
      "expectedOutputText": "3\n"
    }
  ]
}
```

外部文件型 testcase 示例：

```json
{
  "sourceCode": "#include <iostream>\nint main(){long long n;std::cin>>n;std::cout<<n*2<<\"\\n\";}\n",
  "language": "C++",
  "timeLimit": 1000,
  "memoryLimit": 256,
  "checker": "default",
  "testcases": [
    {
      "inputFile": "E2E_cases/P1/data/sum1.in",
      "expectedOutputFile": "E2E_cases/P1/data/sum1.out"
    }
  ]
}
```

响应体示例：

```json
{
  "status": "OK",
  "compile": {
    "succeeded": true,
    "log": ""
  },
  "cases": [
    {
      "verdict": "WrongAnswer",
      "stdout": "4\n",
      "timeUsed": 12,
      "memoryUsed": 8,
      "exitCode": 0,
      "extraInfo": "stdout does not match expected output"
    }
  ]
}
```

顶层 `status` 只表示判题流程状态：`OK` 表示测试点均已完成评测，`CompileError` 表示用户代码编译失败，`SystemError` 表示基础设施错误阻止了评测。它不会聚合测试点 verdict；业务判定应读取 `cases[].verdict`。`cases` 与请求中的 `testcases` 顺序一致。

`cases[].timeUsed` 表示实际测得的 CPU 时间，不会截断到请求的 `timeLimit`。

错误响应示例：

```json
{
  "error": "INVALID_REQUEST",
  "code": "INVALID_REQUEST",
  "details": "sourceCode is required"
}
```

## Checker 说明

### 内置 checker

当前内置 checker 源码位于 `support/checkers/`，构建时会 embed 进二进制，包括：

- `default`
- `ncmp`
- `wcmp`
- `fcmp`
- `yesno`
- `nyesno`
- `lcmp`
- `hcmp`
- `rcmp4`
- `rcmp6`
- `rcmp9`

`checker` 字段为空时，默认使用内置的 `default` checker。

### 外部 checker

如果希望使用外部 checker，请传：

```text
external:relative/path/to/checker.cpp
```

这里的路径同样是相对于 `testdata/` 根目录解析的，并且必须是 `.cpp` 文件。

## 配置

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `HTTP_ADDR` | `0.0.0.0` | HTTP 监听地址 |
| `HTTP_PORT` | `8080` | HTTP 监听端口 |
| `HTTP_READ_TIMEOUT_MS` | `30000` | HTTP 读取请求的超时时间 |
| `HTTP_WRITE_TIMEOUT_MS` | `600000` | HTTP 写响应的超时时间 |
| `CONTAINERD_SOCKET` | `/run/containerd/containerd.sock` | containerd 套接字 |
| `CONTAINERD_NAMESPACE` | `afterglow-sandbox` | containerd namespace |
| `MAX_INPUT_SIZE_MB` | `256` | HTTP 请求体大小上限 |
| `MAX_CONCURRENT_CONTAINERS` | `8` | execution 层同时运行的最大容器数（编译、运行、checker 共享） |
| `MAX_CONCURRENT_JUDGES` | `4` | 同时处理的最大判题请求数 |
| `MAX_TIME_LIMIT_MS` | `10000` | 单测试点 CPU 时间上限 |
| `MAX_MEMORY_MB` | `1024` | 单测试点内存上限 |
| `MAX_TEST_CASES` | `64` | 单次请求测试点数量上限 |
| `MAX_SOURCE_SIZE_KB` | `256` | 源代码大小上限 |
| `EXTERNAL_DATA_DIR` | 空 | 外部测试数据和外部 checker 根目录；未配置时关闭该能力 |
| `LOG_LEVEL` | `info` | 日志级别；当前支持 `info` 和 `debug` |

## 开发

### 运行测试

```bash
go test -count=1 ./...
```

需要真实环境的 HTTP E2E 测试：

```bash
sudo -n go test -count=1 ./internal/transport/httptransport -run TestE2E_HTTP_ExternalCases
```

### 代码检查

```bash
goimports -w .
golangci-lint config verify
golangci-lint run ./...
```

## 文档说明

- `README.md`：项目总览、当前架构、API、配置和运行方式
- `AGENTS.md`：项目开发规范

## 未来演化方向（功能性）

1. 添加独立的评测请求编排层（如评测的优先级处理、请求排队等）
2. 支持评测结果的持久化和回查
