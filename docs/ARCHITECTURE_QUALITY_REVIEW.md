# Architecture Quality Review

本报告从宏观项目架构、代码优雅度、抽象克制程度和学习价值角度审查当前代码。它不以逐个 bug 或具体问题清单为主，而是评估这个项目整体是否“长得像一个好的 Go 工程”。

审查时间：2026-06-22

## 总体评价

当前项目的整体架构方向是健康的。它没有表现出典型 Vibe Coding 项目常见的几类问题：

- 没有为了“看起来企业级”强行拆出很多空壳层
- 没有把每个 model 都配 repository、manager、processor
- 没有明显的反向依赖或循环依赖
- 没有把业务流程藏在过度抽象后面
- 没有使用大量 Go 初学者难以理解的炫技写法

从学习型 Go 工程项目的标准看，它具备比较好的骨架：入口清楚、分层清楚、核心流程可追踪、测试覆盖也不是摆设。

但它目前还谈不上“优雅成熟”。主要原因不是架构方向错了，而是代码的“表达力”和“边界感”还不够稳定：

- service 层承担的职责略多，已经接近变成全流程中心
- sandbox 层按 spec、metrics、verdict、output limiter、preflight 分组，但容器生命周期仍是最复杂的部分
- 部分错误语义和运行时边界还可以继续收敛，尤其是请求总预算和外部资源上限
- lint 配置能作为真实门禁执行，但规则取舍仍应继续围绕可读性而不是形式主义
- 代码整体可读，但还缺少一种“每个模块只说自己该说的话”的干净感

一句话评价：

> 这是一个方向正确、克制且质量门禁可执行的 Go 项目。它不像模板堆出来的代码，但还没有完全打磨出清晰、优雅、可长期演进的工程气质。

## 项目定位匹配度

项目定位是“学习型代码评测沙箱系统”，不是公网开放平台，也不是完整 OJ 平台。当前架构和这个定位基本匹配。

它没有急着引入：

- 消息队列
- 异步任务表
- 分布式 worker
- 数据库持久化
- 用户系统
- 多租户权限模型

这是正确的。因为当前最核心的问题是把一次判题请求从输入、编译、运行、checker 到聚合结果这条链路打通，并让代码结构清楚。

当前实现选择了一条比较窄的主线：

```text
HTTP request
  -> DTO validation
  -> JudgeEngine
  -> compiler / runner / checker
  -> execution.Executor
  -> sandbox
  -> result aggregation
  -> HTTP response
```

这条主线是适合学习项目的。它把读者的注意力放在真实业务流程上，而不是平台化外壳上。

“大型 OJ、命题系统或训练平台中的内部评测微服务”这个定位容易让读者误以为当前代码覆盖完整平台能力。按当前实现，更准确的理解是：

- 当前阶段聚焦单机同步判题引擎
- 未来可以演进为异步评测 worker
- sandbox 安全边界不等同于公网级强隔离产品

这让架构雄心和当前实现保持一致：先把同步判题链路打磨清楚，再考虑平台化能力。

## 分层设计评价

当前主要分层是：

```text
cmd/server
internal/config
internal/transport/httptransport
internal/service
internal/execution
internal/sandbox
internal/resource
internal/model
internal/workspace
internal/cache
```

总体上，这是一个比较自然的 Go 项目结构。

### transport 层

`transport/httptransport` 的职责比较清楚：

- HTTP 路由
- 鉴权 middleware
- body size limit
- JSON decode
- DTO validation
- DTO 与 model 转换
- response encoding

这层没有把判题业务逻辑塞进去，是优点。

DTO 和 model 分开也合理。对学习项目来说，这能清楚展示“外部协议”和“内部领域对象”不是一回事。

HTTP server 的 read/write timeout 由配置控制。需要关注的不是“是否写死”，而是同步判题接口本身的预算校准：一次请求可能包含用户代码编译、checker 编译、多测试点运行和 checker 校验。

### service 层

`service` 是当前项目最核心的一层，负责：

- 请求级校验
- checker 解析
- 外部测试数据加载
- 用户代码编译
- checker 准备
- 多测试点运行
- checker 判定
- verdict 聚合

这符合项目当前规模，但也暴露了一个趋势：`JudgeEngine` 正在成为“全知型编排器”。

它没有失控，因为核心流程仍然能顺着 `Judge()` 读下来。但如果继续加功能，比如：

- special judge 配置更多参数
- 子任务、分组、计分
- 交互题
- 多文件提交
- 编译缓存
- 异步队列

那么当前 `JudgeEngine` 会很快变胖。

更优雅的长期方向不是马上拆很多层，而是保留当前结构，同时识别几个稳定概念：

- `TestDataLoader`：负责把 testcase 中的 file reference 解析成文本
- `CheckerPreparer`：负责 checker source、testlib、编译缓存
- `CaseExecutor`：负责单个 testcase 的 user run + checker run
- `JudgeEngine`：只保留总体编排

注意，这些概念不一定要立刻抽出来。当前代码还没臃肿到必须拆，但这些是未来自然演进的方向。

### execution 层

`execution` 是当前代码里比较有价值的一层。它把“准备临时工作目录、写入文件、调用 sandbox、收集产物”这些编译和运行都会用到的动作集中起来：

- `Compiler` 通过 executor 执行编译任务并收集编译产物
- `Runner` 通过 executor 执行运行任务并传递 stdin、cwd 等运行参数
- 容器并发限制收敛到 `NewThrottledExecutor`
- 通用资源限制和原始执行 verdict 由 `execution` 表达，`service` 不需要直接依赖 `sandbox` 的底层类型
- artifact 收集逻辑由 `execution` 统一处理

这比让 `compiler`、`runner` 各自复制一套 workspace + sandbox 调用流程更清楚。它不是为了“多一层架构”，而是提炼出了一个真实存在的工程概念：一次带输入文件、挂载目录、资源限制和可选产物收集的执行任务。

这层的边界也比较克制。它不知道判题语义，不判断 OK/WA，也不解析 checker 结果，只处理通用执行编排。对当前代码来说，这是一个有实际收益的抽象。

### sandbox 层

`sandbox` 层边界是正确的：上层通过 `execution.Executor` 请求“执行一个命令”，不知道 containerd 的细节。

这是架构上的优点。`service` 不直接依赖 containerd API，也不关心 task、snapshot、cgroup metrics 的具体类型。

sandbox 内部按概念分成几个文件：

```text
sandbox/
  sandbox.go          对外接口和请求/响应类型
  containerd.go       containerd 编排主流程
  preflight.go        cgroup v2 和 containerd 可用性检查
  spec.go             OCI spec 构造
  metrics.go          cgroup metrics 解析
  verdict.go          执行结果分类
  output_limiter.go   stdout/stderr 限制
```

这不是为了拆文件而拆文件，而是让复杂性按概念分组。对学习项目来说，读者可以分别理解“容器如何创建”“资源怎么限制”“输出怎么截断”“verdict 怎么分类”。

`containerd.go` 承载最复杂的生命周期编排，其关键语义是：正常退出、超时、输出超限和请求取消统一进入可追踪的终止路径；kill 后有界等待，cleanup 始终逆序执行，并使用不受请求取消影响的独立 timeout。这里通过一个最小 task 控制接口让状态机可测试，无需继续拆包。

### resource 层

`resource` 层整体比较好。

它做了两件事：

- bundled resource：内置 checker 和 `testlib.h`
- external resource：外部测试数据和外部 checker

外部资源路径做了 normalize、symlink escape 检查和 regular file 校验，这说明代码有边界意识。

从语义上看，`resource` 比 `storage` 更贴近当前职责，因为它不是通用存储层，而是只读资源访问。主路径使用 `resources` / `externalResources` 命名，表达的是同一类只读资源访问能力。

### model 层

`model` 比较克制，只放领域枚举和请求/响应对象。

这是好事。很多 AI 项目会把 model 层写成“领域贫血对象 + 一堆无意义方法”，当前项目没有这个问题。

但 `model` 里混合了内部结果和 JSON tag，这一点略微降低纯粹性。考虑到项目规模和学习定位，这是可以接受的。如果未来 HTTP DTO 已经稳定存在，可以逐步减少 model 对 JSON 的感知。

## 抽象克制度

当前抽象总体是偏克制的。

值得保留的抽象：

- `JudgeService`：HTTP handler 依赖它，测试方便
- `Compiler`：方便缓存 decorator 和 fake 测试
- `Runner`：隔离执行 primitive，测试方便
- `Executor`：统一 workspace 准备、sandbox 调用、artifact 收集和容器限流
- `Sandbox`：隔离 containerd 细节
- `ResourceStore`：统一 bundled/external resource

这些接口基本都有真实用途，不是为了“每个 struct 都要 interface”。

稍微值得警惕的是：

- `Compiler` / `Runner` 有 primitive 化倾向
- `Executor` / `cachedCompiler` / `JudgeEngine` 共同承担不同层级的编排策略
- `JudgeEngine` 自己还有请求级 semaphore

这套设计并不错误，但对学习项目来说，需要文档解释清楚：

- judge concurrency 限制的是请求数
- executor concurrency 限制的是实际容器执行数
- cached compiler 只缓存编译产物，不改变编译语义

如果没有解释，读者会感觉“为什么这里套了好几层”。

整体判断：

> 抽象数量合理，但抽象意图还可以表达得更清楚。

## 代码优雅度评价

### 优点

当前代码大体符合 Go 的直白风格：

- 错误处理基本显式
- 控制流多数时候清楚
- 没有大量 callback、反射、泛型技巧
- 包名短而具体
- 函数大多能从名字看出职责
- 测试中 fake 对象比较直接

这对 Go 初学者友好。

例如 `Judge()` 的主线虽然不短，但读者能按顺序理解：

```text
限流
解析 checker
编译用户代码
准备 checker
运行全部测试点
聚合结果
```

这种“能顺着读”的代码，比过度拆成十几个小对象更适合当前项目。

### 不足

当前代码离“优雅”差的不是技巧，而是局部表达还不够稳定。

典型表现：

1. 部分注释是在解释代码做了什么，而不是解释为什么这样做
   例如 `// Load checker source.` 这类注释信息量偏低。

2. 有些函数承担了多个层次的思考
   例如 `JudgeEngine` 同时处理测试数据加载、checker 准备、测试点执行和结果聚合。

3. 一些策略常量仍偏分散
   例如 checker 运行限制、语言镜像和编译参数。它们不一定都要配置化，但需要集中表达“哪些是策略”。

4. 错误语义有时偏“能返回”，但不够分类清晰
   编译错误、checker 错误、infra 错误、用户错误之间的边界还可以更明确。

整体看，代码是“清楚的”，但还没有完全达到“干净的”。清楚是能读懂，干净是读完以后觉得每个东西都在它该在的位置。

## AI 臃肿评估

按 `AGENTS.md` 中“识别 AI 臃肿”的标准，当前项目没有严重 AI 臃肿。

没有看到：

- 伪通用 repository 层
- 空洞 manager/handler/processor 多层转发
- 脱离需求的插件化框架
- 配置层反向依赖业务层
- model 依赖 transport
- 为未来假想需求预留大量抽象

但有轻微 AI 痕迹：

- `.golangci.yml` 开启了较多检查，需要持续避免为了工具规则牺牲代码表达
- 某些注释偏模板化
- README 的表达比当前实现更“成熟系统化”
- 一些安全描述显得比实际 seccomp 策略更有信心

这不是结构性失败，但需要持续收敛。学习项目最重要的是诚实：文档、配置、代码能力要一致。

## 可演进性评价

当前架构具备继续演进的基础。

比较自然的演进方向：

1. 单机同步判题引擎
   当前代码处于这个阶段。

2. 单机异步 job engine
   引入 job id、状态查询、内存队列或轻量持久化。

3. worker 化评测服务
   HTTP/API 层只提交任务，judge worker 独立运行。

4. 多节点评测系统
   再考虑队列、调度、资源池和结果持久化。

当前代码没有把这些未来方向堵死，这是好事。

但要注意，下一步不应该直接跳到第 3 或第 4 阶段。对这个项目更有价值的是先把第 1 阶段打磨扎实：

- timeout 预算校准
- request limits 细化
- lint 门禁
- 真实 containerd 故障路径测试覆盖

这些是架构地基，不是功能细节。

## 学习价值评价

作为 Go 工程学习项目，它有较高学习价值。

它覆盖了很多真实工程主题：

- HTTP transport 和 DTO
- context 传播
- 结构化日志
- 接口抽象和 fake 测试
- 通用 execution primitive 的抽取
- containerd API
- cgroup metrics
- 临时工作目录
- 嵌入资源
- 外部资源路径安全
- 并发限流
- 表格驱动测试
- 集成测试前置条件检查

这些内容比普通 CRUD 项目更有训练价值。

但学习项目还有一个要求：读者应该能从代码中学到“为什么这样组织”。当前代码在这方面还可以加强：

- README 可以补一节“为什么这样分层”
- service 内可以用更少但更有价值的注释解释编排边界
- sandbox 的取消、kill、wait 和 cleanup 语义可以作为 context 与资源生命周期管理的学习案例
- lint 配置应继续保持“真实可执行、规则有解释”，避免重新变成展示愿望

## 宏观评分

以下评分是以“学习型 Go 判题微服务”为标准，不是以生产级公网沙箱为标准。

| 维度 | 评分 | 评价 |
|------|------|------|
| 架构方向 | 8/10 | 分层自然，主线清楚，没有明显反向依赖 |
| 抽象克制 | 8/10 | 接口基本有真实用途，没有严重过度设计 |
| 代码可读性 | 7/10 | 大体直白，sandbox 分组清楚但生命周期和 service 编排仍偏重 |
| Go 风格 | 7/10 | 基本 idiomatic，少量命名、注释、配置表达可改进 |
| 可演进性 | 7/10 | 当前结构能继续长，但需要先稳住边界语义 |
| 测试诚实性 | 8/10 | 没有滥用 `testing.Short()`，集成测试前置条件较诚实 |
| 质量门禁可信度 | 8/10 | 测试和 lint 都能作为当前门禁运行，sandbox 生命周期有单元与真实取消测试，故障注入场景仍可加强 |
| 文档一致性 | 8/10 | README、AGENTS 和审查文档与当前架构一致，归档文档标注历史口径 |

综合评价：7.5/10。

这是一个值得继续打磨的项目。它越过了“能跑的 demo”阶段，但还没有到“架构清爽、边界稳定、代码有教学美感”的阶段。

## 宏观改进方向

### 1. 保持主链路简单

不要急着引入队列、数据库、任务状态机。先让同步判题链路的边界变得非常清楚。

优先把这些概念稳定下来：

- request validation
- resource loading
- compile
- run
- check
- aggregate
- cleanup

### 2. 适度拆解 service

不要为了拆而拆。等 `JudgeEngine` 继续变大时，优先按真实职责拆：

- test data loading
- checker preparation
- single case execution
- result aggregation

这些拆分会让代码更语义化，而不是更复杂。

### 3. 保持 sandbox 生命周期语义稳定

sandbox 是项目最有工程含量的部分，也最容易变乱。生命周期遵循以下不变量：

- 请求取消时立即 kill task
- kill 后有界等待退出
- cleanup 使用保留 namespace 的独立 timeout
- cleanup 失败记录结构化 warning
- task、container、snapshot 按创建顺序逆序释放

后续改动应保持这些不变量，并优先补真实 containerd 故障路径测试，而不是继续增加生命周期抽象。

### 4. 保持质量门禁服务于代码表达

重点不是继续堆 linter，而是保持规则和项目目标一致。

更好的做法是：

- 保留高信号 linter，例如 `govet`、`staticcheck`、`errcheck`、`errorlint`、`testifylint`、`gosec`
- 对测试代码继续放宽风格类规则，让表格测试优先表达场景而不是迎合复杂度数字
- 对 `nolint` 保持极窄范围，并要求注释说明真实边界
- 提高标准时先改代码表达，再改配置强度

质量门禁的价值不在于配置多严格，而在于每次运行都可信。

## 最终判断

当前项目的宏观架构是“值得保留并继续打磨”的，不需要推倒重来。

最重要的不是补更多功能，而是提高现有结构的表达质量：

- 让 `service` 更像业务编排，而不是全能控制器
- 让 `sandbox` 的容器生命周期语义更可靠、更容易测试
- 让配置、文档、测试和 lint 共同表达同一套工程标准
- 让每个包的存在理由都能被 Go 初学者读懂

如果按这个方向继续收敛，这个项目可以成为一个很好的 Go 工程学习样例。
