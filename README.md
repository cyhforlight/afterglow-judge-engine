# Afterglow Judge Engine

一个基于 containerd 的代码评测引擎。它接收源代码和多组测试数据，完成编译、隔离执行、输出比对和聚合判定，并通过 HTTP 提供统一入口。

这个项目的默认定位不是公网开放平台，而是大型项目中的内部评测微服务。因此整体设计强调边界清晰、实现简洁、可维护性优先，不追求“通用平台化”的过度包装。

这个项目还有一个同样重要的原始目的：作为 Go 语言工程实践的入门学习和训练项目。也正因此，这个仓库不仅关注”功能是否可用”，也同样重视项目风格是否优雅、架构是否清晰、代码是否易读易学、是否符合主流 Go 风格、测试是否诚实、规范是否便于长期演进。

## 设计工作场景

这个项目通常被设想为大型 OJ、命题系统或训练平台中的一个组成服务，而不是直接面向最终用户的公网 API。

这意味着它的典型调用方是受控的上游系统，而不是任意外部客户端。因此在安全边界上，需要重点防御的是用户提交的 `sourceCode` 及其编译运行过程；至于 HTTP 调用方式、字段组合和接入形态，本质上属于内部系统之间的受控交互，不必为了“假想中的开放平台场景”额外堆叠过度复杂的安全设计。

## 项目目标

- 提供一个简单直接的 HTTP 评测入口
- 支持多语言编译与隔离执行
- 支持多测试点、逐点结果和最终聚合判定
- 支持内置 checker，以及基于外部文件的测试数据和 checker

## 当前实现范围

当前代码已经实现了最核心的一条评测链路：

1. HTTP 层接收并校验请求
2. service 层加载测试数据、解析 checker、编译用户代码
3. 对每个测试点执行程序并运行 checker
4. 聚合逐点结果，返回最终 verdict

期望的一些更大规模能力目前还没有展开实现，例如独立的“编排层/消息队列/并发”等，有待后续完善。

## 特性

- 安全隔离：基于 containerd 运行编译和执行流程
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
  - Bearer Token 鉴权（仅当配置了 `API_KEY`）
  - 请求体大小限制
  - 严格 JSON 解码，拒绝未知字段

## 快速开始

### 运行前提

- Linux 环境：Ubuntu 22.04 / Debian 12 或更新版本
- cgroup v2
- 可用的 containerd
- root 权限或等价权限

### 构建与启动

直接在仓库根目录构建和启动：

```bash
go build -o server ./cmd/server
./server
```

默认情况下：

- 内置 checker、`testlib.h` 等 internal resources 会在构建时 embed 进二进制，运行时不需要额外放在可执行文件旁边
- 外部测试数据和外部 checker 根目录默认使用 `/home/forlight/afterglow-judge-engine/testdata`；也可以通过 `EXTERNAL_DATA_DIR` 显式指定

因此最简单的用法仍然是在仓库根目录直接构建并运行。

### 调用评测 API

```bash
curl -X POST http://localhost:8080/v1/execute \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-api-key" \
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

如果未配置 `API_KEY`，则无需 `Authorization` 请求头。

## 架构

### 分层设计

当前实现采用一条比较克制的分层链路：

- `transport/httptransport`
  - 负责 HTTP 路由、鉴权、请求体大小限制、JSON 解码、DTO 校验和响应编码
- `service`
  - 负责完整判题流程编排：加载测试数据、解析 checker、编译、执行、校验、聚合 verdict
- `sandbox`
  - 负责通过 containerd 在受限环境中执行编译和运行动作
- `resource`
  - 负责内部资源（预置 checker）和外部资源（测试数据、题目自定义 Checker 等）的只读访问
- `model`
  - 负责领域对象和枚举类型

依赖方向保持单向：

```text
transport -> service -> model
                    -> sandbox
                    -> resource
```

### 请求处理流程

一次 `POST /v1/execute` 的处理流程如下：

1. HTTP 层限制请求体大小并做严格 JSON 解码
2. DTO 校验字段合法性，并转换为领域模型
3. service 层解析 checker
4. 如果 testcase 使用了 `inputFile` / `expectedOutputFile`，先从外部存储加载文件内容
5. 编译用户代码
6. 准备 checker
7. 逐个测试点执行用户程序，并用 checker 判定结果
8. 根据逐点结果聚合最终 verdict
9. 返回 JSON 响应

### 目录结构

```text
cmd/
└── server/                     HTTP 服务入口

internal/
├── cache/                      简单缓存，用于 checker 编译结果
├── config/                     环境变量配置加载
├── model/                      领域模型（JudgeRequest / JudgeResult / Verdict）
├── sandbox/                    containerd 沙箱适配层
├── service/                    编译、运行、checker、判题编排
├── storage/                    internal resources 和外部文件存储
├── transport/httptransport/    HTTP server / handler / dto / middleware
└── workspace/                  临时工作目录管理

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
Authorization: Bearer <token>
```

只有在配置了 `API_KEY` 时才会校验 Bearer Token。

请求体字段：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `sourceCode` | string | 是 | 源代码文本 |
| `language` | string | 是 | `C` / `C++` / `Java` / `Python` |
| `timeLimit` | int | 是 | 单测试点时间限制，单位毫秒 |
| `memoryLimit` | int | 是 | 单测试点内存限制，单位 MB |
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

- `inputText` 和 `inputFile` 不能同时出现
- `expectedOutputText` 和 `expectedOutputFile` 不能同时出现
- 请求体必须是且只能是一个 JSON 对象
- 未知字段会被直接拒绝

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
  "verdict": "WrongAnswer",
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
  ],
  "passedCount": 0,
  "totalCount": 1
}
```

错误响应示例：

```json
{
  "error": "INVALID_REQUEST",
  "code": "INVALID_REQUEST",
  "details": "sourceCode is required"
}
```

### `GET /health`

```http
GET /health
```

如果 containerd 和底层运行环境正常，返回：

```json
{"status":"healthy"}
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

`checker` 字段为空时，会回退到服务端配置的 `DEFAULT_CHECKER`。

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
| `CONTAINERD_SOCKET` | `/run/containerd/containerd.sock` | containerd 套接字 |
| `CONTAINERD_NAMESPACE` | `afterglow-sandbox` | containerd namespace |
| `MAX_INPUT_SIZE_MB` | `256` | HTTP 请求体大小上限 |
| `MAX_CONCURRENT_CONTAINERS` | `8` | 同时运行的最大容器数（编译+执行共享） |
| `DEFAULT_CHECKER` | `default` | 未显式指定 `checker` 时使用的默认 checker |
| `EXTERNAL_DATA_DIR` | `/home/forlight/afterglow-judge-engine/testdata` | 外部测试数据和外部 checker 根目录 |
| `API_KEY` | 空 | Bearer Token；非空时自动启用鉴权 |
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
golangci-lint run
```

## 文档说明

- `README.md`：项目总览、当前架构、API、配置和运行方式
- `AGENTS.md`：项目开发规范

## 未来演化方向（功能性）

1. 添加独立的评测请求编排层（如评测的优先级处理、请求排队等）
2. 支持评测结果的持久化和回查
