# 实现状态

## 已实现

- Go 模块化单体；领域模型独立于 YAML、SQL、Docker 和模型 SDK。
- Service / Scenario 严格 YAML 加载、域规则校验、地址注入和执行规格解析。
- 真实与 mock 实现统一使用 source/image 打包方式，依赖由实现声明。
- `doctor / validate / plan / up / ps / logs / stop / down / version`。
- 每个 Run 独立 Compose 项目，标签限定资源清理，默认仅发布到回环地址。
- SQLite 运行记录、进程间运行锁、状态转换、失败/取消清理及日志保留。
- Go Agent 工具调用循环、DeepSeek 适配器、轮次/超时限制及独立任务上下文。
- Repository Agent 的只读目录/文件工具与引用证据校验。
- HTTP 真实转发服务 + 自定义代码 mock 的最小样例。
- 配置式 unary gRPC mock：Go 编译 proto、验证具体 service 与响应、固定返回/错误/延迟、未匹配方法明确失败、自动镜像打包与健康检查。
- `rpc` 命令和 mock 调用事件记录，实际传播 traceparent。
- 固定版本 Online Boutique 的真实 checkout + 六个 mock，成功/支付异常两个场景。

## 尚未实现

- 六 Agent 的完整任务协调及 Topology / Config / Mock / Runtime / Debug 的具体自动化流程。
- Repository 发现报告到可执行 Service Manifest 的转换与 Git 拉取。
- 配置式 HTTP mock、Thrift/自定义 RPC 适配、gRPC 流式/条件/状态规则。
- 通用测试断言命令、OpenTelemetry 调用链收集与查询。
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
