本文档用于维护一组只依赖原生语言能力的 Sandbox 测试代码，用于验证 OJ 对 `CE / TLE / MLE / RE / OLE` 的处理，以及对受限操作的策略拦截能力。

共包含四类语言：

1. C（C99 标准）
2. C++（C++17 标准，`-O2`）
3. Java（JDK 21 标准）
4. Python（Python 3.10+）

## 使用前提

- Java 文件名仅用于仓库内分类；实际提交时由 OJ 侧读取源码文本并自行处理文件名。
- 本仓库的目标是提供“尽量稳定触发目标类别”的测试样例，不要求所有语言使用完全相同的触发机制。
- `MLE` 和 `POLICY` 类样例与具体资源限制、权限策略有关；不同 Sandbox 可能给出略有差异的终止方式，但它们不应被视为正常通过。

## 测试代码集合说明

### 目录结构

- `ce/` - 编译错误测试（4 个文件）
- `tle/` - 超时测试（4 个文件）
- `mle/` - 内存增长测试（4 个文件）
- `re/` - 运行时错误测试（6 个文件）
- `ole/` - 输出超限测试（2 个文件）
- `policy/` - Sandbox 策略测试（2 个文件）

### 分类定义

- `CE`：应在编译阶段或解释阶段直接失败。
- `TLE`：应持续占用 CPU，不依赖输出或内存增长。
- `MLE`：应持续申请并保留内存，期望在内存限制下被终止。
- `RE`：应由运行时异常、信号或未捕获错误直接终止，不依赖沙箱策略拦截。
- `OLE`：应持续输出数据，期望在输出限制下被终止。
- `POLICY`：用于验证沙箱对进程创建、系统调用等受限操作的拦截。大概率以 `RE` 或者 `OLE` 作结。

## 详细说明

### CE (Compile Error) - 编译错误

| 文件名 | 语言 | 触发机制 | 预期结果 |
|--------|------|----------|----------|
| `ce_syntax_error.c` | C | 缺少分号 | 编译失败 |
| `ce_syntax_error.cpp` | C++ | 使用未声明标识符 | 编译失败 |
| `ce_wrong_class_name.java` | Java | 缺少分号（语法错误） | 编译失败 |
| `ce_syntax_error.py` | Python | `if` 语句缺少冒号 | 解释失败 |

### TLE (Time Limit Exceeded) - 超时

| 文件名 | 语言 | 触发机制 | 预期结果 |
|--------|------|----------|----------|
| `tle_infinite_loop.c` | C | 无限循环 + 算术运算 | 超时 |
| `tle_infinite_loop.cpp` | C++ | 无限循环 + 算术运算 | 超时 |
| `tle_infinite_loop.java` | Java | 无限循环 + 算术运算 | 超时 |
| `tle_infinite_loop.py` | Python | 无限循环 + 算术运算 | 超时 |

### MLE (Memory Limit Exceeded) - 内存增长

| 文件名 | 语言 | 触发机制 | 预期结果 |
|--------|------|----------|----------|
| `mle_malloc_blocks.c` | C | 持续分配并保留 1 MiB 堆内存块 | 内存超限或被内存限制终止 |
| `mle_vector_push.cpp` | C++ | 无限向 `vector` 追加元素 | 内存超限或被内存限制终止 |
| `mle_array_list.java` | Java | 无限向 `ArrayList` 添加 1 MiB 字节数组 | 内存超限、`OutOfMemoryError` 或被内存限制终止 |
| `mle_list_append.py` | Python | 无限向 `list` 追加对象 | 内存超限或被内存限制终止 |

### RE (Runtime Error) - 运行时错误

| 文件名 | 语言 | 触发机制 | 预期结果 |
|--------|------|----------|----------|
| `re_abort.c` | C | 主动调用 `abort()` | 运行时错误 |
| `re_vector_at.cpp` | C++ | `vector::at()` 越界并触发未捕获异常 | 运行时错误 |
| `re_null_pointer.java` | Java | 空指针解引用 | `NullPointerException` |
| `re_index_error.py` | Python | 列表索引越界 | `IndexError` |
| `re_segfault.cpp` | C++ | 空指针解引用 | 段错误 |
| `re_null_dereference.cpp` | C++ | 空指针解引用（nullptr） | 段错误 |

### OLE (Output Limit Exceeded) - 输出超限

| 文件名 | 语言 | 触发机制 | 预期结果 |
|--------|------|----------|----------|
| `ole_infinite_output.cpp` | C++ | 无限输出文本 | 输出超限 |
| `ole_infinite_print.py` | Python | 无限 `print()` | 输出超限 |

### POLICY - Sandbox 策略测试

**注意**: 这些测试当前被跳过，因为需要 seccomp 系统调用过滤配置。

| 文件名 | 语言 | 触发机制 | 当前行为 | 预期结果（需要 seccomp） |
|--------|------|----------|----------|--------------------------|
| `policy_fork_bomb.c` | C | 持续调用 `fork()` | TLE（达到 PID 限制后超时） | 被策略拦截或 RE |
| `policy_system_call.py` | Python | 调用 `os.system()` | OK（命令失败但程序正常结束） | 被策略拦截或 RE |

这些测试需要在 `sandboxSecurityOpts()` 中添加 seccomp 配置才能正确工作。
