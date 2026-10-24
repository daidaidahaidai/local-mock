# 实现状态

## 已实现

- Go 模块化单体；领域模型独立于 YAML、SQL、Docker 和模型 SDK。
- Service / Scenario 严格 YAML 加载、域规则校验、地址注入和执行规格解析。
- 真实与 mock 实现统一使用 source/image 打包方式，依赖由实现声明。
- `doctor / validate / plan / components / up / probe / ps / logs / stop / down / version`。
- 每个 Run 独立 Compose 项目和 internal + isolated 自定义网络；无宿主机端口发布；标签限定资源清理。
- 显式 components 选择、Nacos/MySQL/Redis 镜像模板、自定义镜像、组件地址引用、固定 IP 槽位。
- `components add/remove` 原子编辑场景，保留相对路径和注释；支持多个同类实例、空闲 IP 分配，拒绝重名或删除被引用组件。
- 组件内声明式初始化（命令、stdin 文件、超时），输入哈希与快照、逐步执行记录；初始化成功后启动业务，失败不重试并清理该 Run。
- MySQL schema/seed 与 Redis string/hash/TTL 初始化，真实 Go 业务 SQL/缓存读写示例。
- 网络内 HTTP/gRPC/TCP probe，Agent 探测使用相同接口；启动及探测前检查实际隔离配置。
- SQLite 运行记录、进程间运行锁、状态转换、失败/取消清理及日志保留。
- Go Agent 工具调用循环、DeepSeek 适配器、轮次/超时限制及独立任务上下文。
- Repository Agent 的只读目录/文件工具与引用证据校验。
- 六 Agent 独立上下文和顺序协调：拓扑选择、配置、gRPC Mock 规则、入口探测、基于观测的 Debug 报告。
- `agent develop` 输出可执行 DSL、多重图、配置快照和阶段记录；显式执行、预算及失败清理。
- HTTP 真实转发服务 + 自定义代码 mock 的最小样例。
- 配置式 unary gRPC mock：Go 编译 proto、验证具体 service 与响应、固定返回/错误/延迟、未匹配方法明确失败、自动镜像打包与健康检查。
- `rpc` 命令和 mock 调用事件记录，实际传播 traceparent。
- 固定版本 Online Boutique 的真实 checkout + 六个 mock，成功/支付异常两个场景。

## 尚未实现

- 自动识别/改写任意注册中心 SDK 配置，Dubbo Provider/元数据适配，以及注册数据的声明式初始化。组件容器可运行不等于业务自动接入。
- 组件依赖排序、配置文件挂载、运行中数据重置、持久数据卷和跨 Run 数据复用。
- Repository 发现报告到可执行 Service Manifest 的转换与 Git 拉取。
- 配置式 HTTP mock、Thrift/自定义 RPC 适配、gRPC 流式/条件/状态规则。
- 更丰富的测试断言（当前 Agent 流程支持状态码/文本包含）、OpenTelemetry 调用链收集与查询。
- TUI、历史重放、数据重置、运行产物保留策略。

## 验证记录

2026-10-18，Debian amd64，Docker Engine 29.8.2，Compose 5.6.0，Go 1.25 构建容器：

- 单元测试、`go test -race ./...`、`go vet ./...` 和 Linux CLI 构建通过。
- 首次 HTTP 容器组启动并通过健康检查，CLI 退出后容器保持运行。
- 使用真实 DeepSeek API 的 Repository Agent 检查小样例，4 轮完成、引用证据校验通过；此次调用共返回 4,830 tokens 用量。
- 生命周期端到端验证通过：真实 HTTP 请求经调用方到达 mock；两个活动 Run 互不干扰；停止、重复清理及历史日志读取正常；启动超时与 Ctrl-C 中断均清理全部所属容器和网络。

模型 Key 仅从运行 shell 读取，未复制到仓库或工作负载容器。

2026-10-19：

- gRPC mock 的健康检查、类型校验、未匹配方法、延迟取消和 traceparent 传播测试通过。
- 修复 SQLite 多进程初始化竞争；并发打开/写入、生命周期取消回归通过。
- Online Boutique 端到端验收通过：真实 checkout + 六个 mock 完成下单；切换为支付 Unavailable 后请求失败，且没有发货与邮件调用。
- 上游源码通过 Mac 获取固定 commit 后传到 Debian（Debian 直接 GitHub clone 超时）；业务源码未修改，构建及运行均在 Debian 容器内完成。
- 未选择的真实实现不要求源码目录已存在，选择后才检查构建材料。

2026-10-20：

- 六 Agent 角色隔离、产物交接、预算停止、启动/断言/模型失败清理及取消测试通过；全量 race tests、vet 与 Linux 构建通过。
- 脚本化模型 + 真实 Docker 的六 Agent 验收通过：环境覆盖生效、真实请求穿过调用方到达 mock、成功/断言失败都正确清理。
- 真实 DeepSeek HTTP 流程六 Agent 全部完成：实际响应包含 `hello from six agents`，Debug 引用真实探测及日志，清理成功；共 13,387 tokens。
- 首次 Boutique 模型验证因上下文用量超过预算而在启动前停止（104,629 tokens，最后一次响应越过 100,000 预算）；随后裁剪角色输入并澄清 Mock 实现类型与名称。
- 裁剪角色输入、分开总目标与阶段任务，并统一 gRPC 状态码写法后，真实 DeepSeek Boutique 六 Agent 验证通过：保留七个部署，Mock Agent 修改支付规则，真实 checkout 返回预期 Internal 与指定错误文本；探测与支付调用的 trace ID 一致，日志未出现 ShipOrder 或邮件调用；全部容器和网络清理成功。本次 30,390 tokens。
- 本轮真实 API 验证共 5 次（HTTP 1 次、Boutique 4 次），含上述预算失败和两次阶段拒绝，总用量 185,859 tokens。阶段拒绝均发生在 Docker 启动前；没有把未执行结果标记为成功。


2026-10-21～22，Debian amd64，Docker Engine 29.8.2，Compose 5.6.0：

- 增加用户显式选择的基础组件，提供 Nacos `v2.5.1`、MySQL `8.4.11`、Redis `7.4.9-alpine` 模板；自定义名称/镜像使用同一执行路径。支持只启动组件的场景。
- Run 改为 Runtime 管理的 internal + isolated bridge；组件固定 IP 槽位、业务内部 DNS、无宿主机端口发布。CLI 和 Agent 的 HTTP/gRPC 探测迁入网络内部工具容器。
- 实际连通性验收：本 Run 服务正常；其他 Run、宿主机各 IPv4 地址的自建 canary 均不可达；未知域名不能解析连接。没有使用线上服务作为探测目标。
- 真实 Nacos 注册/发现、配置发布/读取，MySQL 建表写入查询，Redis SET/GET，以及组件与业务容器共同启动通过。Nacos 配置缓存异步更新，验收只在限定时间内轮询读取，不重放写入。
- 独立 MySQL/Redis 场景及 TCP probe 通过；down 后确认 MySQL 匿名数据卷删除。
- 单元/race、vet、静态 CLI 构建通过；原有双 Run、stop/down、超时/SIGINT 回滚、六 Agent 成功/断言失败清理，以及 Boutique 下单/支付异常回归通过。
- Mac 仅编辑和同步；构建与容器测试均在 Debian 执行。本轮未调用付费模型。测试资源按 Run 清理，镜像和构建缓存保留。

2026-10-24，Debian amd64，Docker Engine 29.8.2，Go 1.25 构建容器：

- `components add/remove` 验证通过：新建场景、同类多实例和 IP 分配、重名拒绝、引用保护、未知/重复字段拒绝、失败不覆盖文件，以及注释和相对路径保留。
- SQL 初始化文件的哈希/快照、DSL 导出及 Agent 交接保留、命令/超时校验、输出截断测试通过。
- 真实 MySQL/Redis + Go 订单服务验收通过：读取预置商品与缓存、订单写入/读回、缓存写入/读回、hash 与 TTL；删除后重建 Run，订单编号重新从 1 开始。
- SQL 错误、Redis 错误和步骤超时都阻止业务容器创建，后续步骤未执行；初始化期间 SIGINT 取消也清理该 Run，保留初始化记录。
- 全量 race、vet、静态 CLI 构建通过；全套 Docker 生命周期、网络隔离、Nacos、六 Agent、Boutique 回归通过。最终输出截断修正后重跑单元/race/vet 和新增初始化 Docker 验收，均通过。
- 最新源码和二进制同步到 Debian；没有安装 Mac 运行时，没有调用付费模型。验收后无受管理的容器或网络残留。
