# 六 Agent 工作流

`agent develop` 使用六次独立 Agent 会话，通过结构化产物协作。每个角色重新建立 system/user 上下文，只接收该角色需要的清单、契约及前序产物，不共享其他角色的模型对话历史。

当前输入是**已有可执行 Service catalog 的 Scenario**，再结合源码和自然语言任务调整环境。它已经能完成分析、选择实现、配置、生成 gRPC Mock 规则、启动验证和诊断；尚不支持从任意陌生仓库零配置生成所有 Service 清单或自动修改业务代码。

## 使用

```bash
./bin/localmock agent develop \
  --repo examples/http-smoke \
  -f examples/http-smoke/scenario.yaml \
  --task '保留真实 caller，把 dependency 的 MOCK_RESPONSE 改为 hello from agents，并验证 caller 的 GET / 返回该文本' \
  --output .localmock/agent-demo
```

默认仅分析并生成文件，**不启动 Docker、不发送测试请求**。Debug 的 verdict 为 `not_executed`。可以检查生成的文件，再用普通命令运行：

```bash
./bin/localmock validate -f .localmock/agent-demo/scenario.yaml
./bin/localmock up -f .localmock/agent-demo/scenario.yaml --json
```

加 `--execute`，程序在最终 DSL 校验成功后启动容器、执行 Runtime Agent 提出的探测，并将真实观察交给 Debug Agent。成功后容器继续运行，可用普通 `logs/stop/down` 管理。加 `--cleanup` 可在验证成功后也清理；任何启动、探测、模型、写入或中断失败都尝试清理本工作流的 Run。

输出目录必须不存在，防止覆盖上次结果。失败产物会保留；修复问题后使用新的输出目录，不自动恢复或重复上次请求。`--state` 控制普通运行记录目录。

## 各角色及权限

| Agent | 任务 | 产物与约束 |
|---|---|---|
| Repository | 读取相关构建文件和源码入口，核对已有清单 | 带实际读取证据的报告；仅拥有 `list_files/read_file` 工具 |
| Topology | 选择部署、真实或 Mock 实现及依赖绑定 | 完整拓扑；程序校验依赖闭包和整个服务替换；仅允许临时发布端口 |
| Config | 决定部署环境覆盖 | 完整覆盖表；禁止覆盖依赖地址和生成凭据类配置 |
| Mock | 按任务修改选中的配置式 gRPC Mock | 完整方法规则列表；Proto 类型、错误码、延迟及实现范围校验；未修改项保留 |
| Runtime | 设计入口探测、检查环境可执行性 | 1–8 个 HTTP GET 或 unary gRPC 探测；由程序生成 DSL/执行规格并调用 Docker |
| Debug | 根据探测与日志解释结果 | findings 必须引用 observation ID；verdict 与程序断言结果一致；仅建议修复 |

后五个 Agent 不能调用 shell、Docker、写入源码或直接发送请求；模型只提交结构化产物。文件写入、契约编译、Compose 和容器生命周期由确定性的 Go 代码处理。若无法满足任务，Agent 可返回 `blocked` 原因，工作流停止并保留产物。

Mock 规则替换针对 Service 的所选实现，**实现名称可以是任意合法名字**，判断依据是 `type: mock`。若多个部署选中同一个实现，它们共享此规则修改。代码/镜像 Mock 可以通过 Config 修改其已有环境参数，但该阶段不自动重写其代码。

## 产物

- `request.json`：用户任务、模型、预算及执行选项。
- `repository/topology/config/mock/runtime/debug.json`：各角色已验证输出、轮次和用量；失败时仅包含完成到该阶段的结果。
- `scenario.yaml`、`*.service.yaml`、`*.mock.yaml`：可直接使用普通 CLI 的 DSL。
- `graph.json`：以 deployment 为节点、每个依赖接口为边的有向多重图；同一逻辑服务的多个部署保持独立。
- `config-snapshot.json`、`execution-spec.json`：包括自动注入地址的有效配置与执行规格。
- `observations.json`：执行后的容器状态、探测断言和截取日志。
- `workflow.json`：阶段、状态、总用量、Run ID、是否成功清理及失败原因。

源码和 IDL 保持指向本机仓库的绝对路径；DSL 可在同一工作区复用，移动到另一台机器需调整路径。普通运行机制会进一步保存输入与源码快照。不能因此宣称已支持跨机器无依赖重放。

## 预算和验证边界

`--max-rounds` 默认每 Agent 10 轮，`--max-tokens` 默认整个流程 100,000 tokens，`--timeout` 默认 15 分钟。预算在每次模型请求前检查；由于实际用量由服务端在响应后报告，最后一次响应可能超过 token 预算。每个阶段的初始输入另有限制为 256 KiB。耗尽后不继续请求，也不自动重试具有副作用的执行。

模型 API 仅从进程读取 `DEEPSEEK_API_KEY`；普通运行不需要模型。工作流会把所读源码、选定清单/配置、任务和截取的服务日志发送给 DeepSeek，请使用适合发送的测试数据；常见凭据文件过滤不等于通用秘密识别。

探测目标只能是本次 Run 在 `127.0.0.1` 发布的服务接口；HTTP 不跟随重定向、不使用宿主机 HTTP 代理。gRPC 请求先通过契约校验；状态码接受 `Internal` / `INTERNAL`、`InvalidArgument` / `INVALID_ARGUMENT` 等常见写法并统一比较。每个探测只执行一次，最多 15 秒。判断依据是预期状态码及可选文本包含断言，预期错误可以构成通过的测试。

日志最多提交 16 KiB 给 Debug；它们不是完整 Trace 图。traceparent 仅表示实际发送的上下文，服务是否传播由自身实现决定。模型无法被程序证明理解了所有自然语言意图；请检查生成的拓扑和探测是否覆盖你的要求。

六角色的自动化测试使用脚本化模型，不消耗 API tokens；Docker 验收使用真实构建、请求、观察和清理。真实 DeepSeek 验证记录见 [实现状态](status.md)。
