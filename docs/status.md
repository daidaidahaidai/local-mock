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

## 尚未实现

- 六 Agent 的完整任务协调及 Topology / Config / Mock / Runtime / Debug 的具体自动化流程。
- Repository 发现报告到可执行 Service Manifest 的转换与 Git 拉取。
- 配置式 HTTP/gRPC mock、Thrift/自定义 RPC 适配。
- Online Boutique 场景、请求测试命令、OpenTelemetry 调用链查询。
- TUI、历史重放、数据重置、运行产物保留策略。

## 验证记录

2026-10-18，Debian amd64，Docker Engine 29.8.2，Compose 5.6.0，Go 1.25 构建容器：

- 单元测试、`go test -race ./...`、`go vet ./...` 和 Linux CLI 构建通过。
- 首次 HTTP 容器组启动并通过健康检查，CLI 退出后容器保持运行。
- 使用真实 DeepSeek API 的 Repository Agent 检查小样例，4 轮完成、引用证据校验通过；此次调用共返回 4,830 tokens 用量。
- 生命周期端到端验证通过：真实 HTTP 请求经调用方到达 mock；两个活动 Run 互不干扰；停止、重复清理及历史日志读取正常；启动超时与 Ctrl-C 中断均清理全部所属容器和网络。

模型 Key 仅从运行 shell 读取，未复制到仓库或工作负载容器。
