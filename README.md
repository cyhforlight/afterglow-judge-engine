# Afterglow Judge Engine

一个基于 containerd 的代码评测引擎，接收源代码和测试数据，完成编译、隔离执行与答案校验，通过 HTTP 返回逐点判题结果。定位为大型 OJ、命题系统或训练平台的内部评测组件。

## 特性

- **容器化沙箱**：基于 containerd、cgroup v2 和 seccomp 隔离执行，支持 CPU / 内存 / 输出限制
- **多语言**：C / C++ / Java / Python
- **逐点评测**：每个测试点独立运行并返回 verdict 和资源使用数据
- **Checker 体系**：内置 11 种 testlib checker，支持外部 checker 和请求内联 checker；通过 LRU 缓存编译产物并以 singleflight 合并并发请求
- **并发调度**：goroutine 并行执行测试点，两级信号量限制判题与容器任务并发

## 快速开始

### 运行前提

- Go 1.26
- Linux（Ubuntu 22.04 / Debian 12 或更新），x64
- cgroup v2 + containerd
- root 权限或等价权限

### 构建与启动

```bash
go build -o server ./cmd/server
./server
```

内置 checker 和 `testlib.h` 在构建时 embed 进二进制，容器镜像首次使用时按需拉取。

如需使用外部测试数据或外部 checker：

```bash
export EXTERNAL_DATA_DIR=/absolute/path/to/testdata
./server
```

请求进入判题后会继续执行，即使客户端断开连接。相同源码的 checker 同步共享编译结果，调用方等待编译及资源清理完成。

收到 SIGINT / SIGTERM 后，服务停止接收新请求，等待现有请求完成后退出，不设固定关闭时限。强制终止进程可能留下尚未清理的容器和临时文件。

### 调用评测 API

```bash
curl -X POST http://localhost:8080/v1/execute \
  -H "Content-Type: application/json" \
  -d '{
    "sourceCode": "import sys\nn=int(sys.stdin.readline())\nprint(n*2)",
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

```text
transport -> service -> model
                    -> resource
                    -> execution -> sandbox
```

- `transport/httptransport`：HTTP 路由、请求体限制、JSON 解码、响应编码
- `service`：判题流程编排——加载测试数据、解析 checker、编译、执行、校验、汇总结果
- `execution`：准备临时 workspace、调用 sandbox、收集编译产物，限制容器并发
- `sandbox`：通过 containerd 在受限环境中执行编译和运行
- `resource`：内置资源和外部文件的只读访问
- `model`：判题请求、结果和枚举类型，承载 HTTP API 的 JSON 字段约定

### 目录结构

```text
cmd/server/                     HTTP 服务入口
internal/
├── execution/                  容器编译与运行、workspace、产物收集
├── model/                      JudgeRequest / JudgeResult / Verdict
├── resource/                   内置资源与外部文件只读访问
├── sandbox/                    containerd 沙箱适配层
├── service/                    判题编排、checker 编译缓存
└── transport/httptransport/    HTTP server / handler / middleware
support/
├── testlib.h
└── checkers/                   内置 checker 源码
testdata/                       外部测试数据、E2E 用例
```

## HTTP API

### `POST /v1/execute`

**请求体字段：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `sourceCode` | string | 是 | 源代码文本 |
| `language` | string | 是 | `C` / `C++` / `Java` / `Python` |
| `timeLimit` | uint32 | 是 | 单测试点 CPU 时间限制，毫秒 |
| `memoryLimit` | uint32 | 是 | 单测试点内存限制，MB；Java 对应 `-Xmx` |
| `checker` | string | 否 | 内置 checker 短名，或 `external:<path>.cpp` |
| `checkerSourceCode` | string | 否 | 请求内联的 C++ testlib checker 源码，与 `checker` 互斥 |
| `testcases` | array | 是 | 测试点列表，最多 64 个 |

**单个 testcase 字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `inputText` | string | 直接传入输入文本 |
| `expectedOutputText` | string | 直接传入标准输出文本 |
| `inputFile` | string | 相对于 `EXTERNAL_DATA_DIR` 的输入文件路径 |
| `expectedOutputFile` | string | 相对于 `EXTERNAL_DATA_DIR` 的标准输出文件路径 |

`inputText` / `expectedOutputText` 与 `inputFile` / `expectedOutputFile` 不能交叉使用；文件型必须同时提供两者。

**约束：**

- 请求体为单个 JSON 对象，未知字段直接拒绝，上限 256 MiB，读取超时 30 秒
- `checker` 和非空的 `checkerSourceCode` 不能同时提供
- `timeLimit` 和 `memoryLimit` 必须为正数
- Java JVM 堆外开销由引擎额外预留，不从 `memoryLimit` 扣除
- `memoryUsed` 表示容器内存峰值，Java 中包含堆外，可能高于 `memoryLimit`
- 达到 `timeLimit` 时主动停止任务；wall time 上限为 `timeLimit` 的三倍，用于兜底阻塞和休眠程序

**文本型请求示例：**

```json
{
  "sourceCode": "#include <iostream>\nint main(){int a,b;std::cin>>a>>b;std::cout<<a+b<<\"\\n\";}\n",
  "language": "C++",
  "timeLimit": 1000,
  "memoryLimit": 256,
  "checker": "default",
  "testcases": [{"inputText": "1 2\n", "expectedOutputText": "3\n"}]
}
```

**响应体示例：**

```json
{
  "status": "OK",
  "compile": {"succeeded": true, "log": ""},
  "checkerCompile": {"succeeded": true, "log": ""},
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

**顶层 `status` 语义：**

| 值 | 含义 |
|----|------|
| `OK` | 所有测试点已完成，含 WA / TLE 等用户程序结果 |
| `CompileError` | 用户代码编译失败 |
| `CheckerCompileError` | 请求内联 checker 编译失败 |
| `CheckerExecutionError` | 请求内联 checker 未能完成某测试点检查 |
| `SystemError` | 基础设施异常，或内置 / 外部 checker 编译或执行失败 |

业务判定读 `cases[].verdict`，不是 `status`。

`checkerCompile` 在 checker 编译得到正常结果时出现；用户代码编译失败或 checker 编译基础设施异常时省略。

**错误响应示例：**

```json
{
  "error": "Bad Request",
  "code": "INVALID_REQUEST",
  "details": "sourceCode is required"
}
```

## Checker

### 内置 checker

`checker` 字段为空时默认使用 `default`。可用值：

`default` / `ncmp` / `wcmp` / `fcmp` / `yesno` / `nyesno` / `lcmp` / `hcmp` / `rcmp4` / `rcmp6` / `rcmp9`

### 外部 checker

```json
{"checker": "external:relative/path/to/checker.cpp"}
```

路径相对于 `EXTERNAL_DATA_DIR`，必须是 `.cpp` 文件。

### 请求内联 checker

```json
{
  "checkerSourceCode": "#include \"testlib.h\"\nint main(int argc, char **argv) { registerTestlibCmd(argc, argv); quitf(_ok, \"accepted\"); }"
}
```

与内置、外部 checker 使用相同的编译环境（C++20、项目内置的 `testlib.h`）和固定资源限制。相同源码共享编译缓存。

编译失败返回 `CheckerCompileError` 和 `checkerCompile.log`；checker 进程异常退出（非 0/1/2）或触发资源限制时，对应测试点返回 `CheckerExecutionError`。

## 安全边界

项目使用 containerd、cgroup v2、只读 rootfs、能力裁剪和 seccomp 黑名单约束用户程序。适合学习和内部受控系统场景，不是公网级强隔离沙箱。

已知边界：

- 编译阶段不启用 seccomp，因为编译器需要创建进程
- 运行阶段的 seccomp 是黑名单策略，不是完整 allowlist
- 编译产物限 64 MiB，超限按编译失败处理
- 外部资源只从 `EXTERNAL_DATA_DIR` 读取，做路径穿越和符号链接检查；没有单请求总字节上限，该目录应只承载受控题目资源

## 配置

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `HTTP_LISTEN_ADDR` | `:8080` | HTTP 监听地址 |
| `CONTAINERD_SOCKET` | `/run/containerd/containerd.sock` | containerd 套接字 |
| `MAX_CONCURRENT_CONTAINERS` | `8` | 同时运行的最大容器数（编译、运行、checker 共享） |
| `MAX_CONCURRENT_JUDGES` | `4` | 同时处理的最大判题请求数 |
| `EXTERNAL_DATA_DIR` | 空 | 外部测试数据和 checker 根目录；未配置时关闭该能力 |
| `LOG_LEVEL` | `info` | slog 日志级别 |

## 开发

```bash
# 运行测试
go test -count=1 ./...

# HTTP E2E 测试（需要真实 containerd 环境）
sudo -n go test -count=1 ./internal/transport/httptransport -run TestE2E_HTTP_ExternalCases

# 代码检查
goimports -w .
golangci-lint run ./...
```
