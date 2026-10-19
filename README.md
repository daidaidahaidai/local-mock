# local-mock

用 Go 实现的本地服务联调 CLI：通过声明式场景选择真实服务与 mock，管理隔离的 Docker 容器组，并逐步引入独立 Agent 辅助分析与配置。

**当前是可运行的开发版本，不是完整六 Agent 系统。** 已实现严格 DSL 校验、依赖绑定、源码/镜像实现、容器组生命周期、配置式 gRPC mock、SQLite 记录和 DeepSeek Repository Agent。完整能力与限制见 [开发状态](docs/status.md)。

## 运行条件

- Linux 与 Docker Engine；Docker Compose 插件需要支持 `up --wait`。
- 运行已编译 CLI 不需要安装 Go。源码构建使用 Go 1.25+。
- 只有 Agent 命令需要 `DEEPSEEK_API_KEY`。普通场景命令不调用模型。
- 本机已有的源码目录可以作为构建上下文；Git 自动拉取尚未实现。

## 使用

```bash
./bin/localmock doctor
./bin/localmock validate -f examples/http-smoke/scenario.yaml
./bin/localmock plan -f examples/http-smoke/scenario.yaml
./bin/localmock up -f examples/http-smoke/scenario.yaml --json
./bin/localmock ps --json
./bin/localmock logs RUN_ID
./bin/localmock stop RUN_ID
./bin/localmock down RUN_ID
```

示例启动一个真实 HTTP 转发服务和一个自定义代码 mock；调用方通过场景绑定访问 mock。`ps --json` 显示分配在 `127.0.0.1` 上的临时端口，访问调用方 `/` 应返回 `hello from mock`。这个小样例用于生命周期验证。

实际服务组见 [Online Boutique 样例](examples/online-boutique/README.md)：真实 checkout 从源码构建，六个依赖由配置式 gRPC mock 替代，包含成功下单和支付异常两个场景。`localmock rpc` 可以依据 proto 发起请求，并输出 trace ID；完整调用链视图尚未实现。

`up` 等待就绪后退出，容器继续运行。`stop` 停止整组容器；`down` 删除该 Run 的容器、网络及受管理的数据卷，保留诊断文件和历史记录。重复 `down` 安全。启动超时或 Ctrl-C 会尝试清理部分资源；如果 Docker 不可用导致清理失败，恢复后再次执行 `down RUN_ID`。

运行中服务异常退出不会自动销毁现场。`ps` 分别显示记录状态和实际容器状态。异常终止留下的 `starting` Run 可以显式 `down` 清理；普通 CLI 退出不触发清理。

所有运行命令支持 `--state DIR`、`--timeout 10m`；结构化命令支持 `--json`。参数必须放在位置参数之前，例如 `down --state ./state RUN_ID`。日志命令输出日志文本。默认状态目录为 `$XDG_STATE_HOME/localmock` 或 `~/.local/state/localmock`，也可通过 `LOCALMOCK_STATE_DIR` 指定。

退出码：`0` 成功，`1` 执行失败，`2` 参数或场景校验失败，`130` 用户中断。进度与错误写入 stderr。

## Repository Agent

在运行 CLI 的 shell 中设置 `DEEPSEEK_API_KEY`。可以通过 `DEEPSEEK_MODEL` 或 `--model` 选择模型，默认 `deepseek-flash`。

```bash
./bin/localmock agent inspect \
  --repo examples/http-smoke \
  --max-rounds 12 \
  --timeout 5m \
  --output repository-report.json
```

该命令会产生 API 费用。Agent 只能列目录和读取工作区文件，默认排除常见凭据文件。提交的证据路径必须实际读取过。输出是发现报告，尚不是可直接执行的 Service Manifest；不宣称已运行服务。它会把读取的源码发送到配置的模型服务，请选择适合发送的仓库。

`doctor` 仅检查密钥存在性与 Docker，不调用模型，也不输出密钥。登录 shell 中的环境变量不一定被非交互 SSH 加载，应在实际运行进程环境中配置。

## 开发与测试

业务代码按 DDD 限界上下文组织。规则见 [AGENTS.md](AGENTS.md)，设计依据见 [设计基线](docs/design.md)，当前文件格式见 [DSL](docs/dsl.md)。

在 Linux 上运行：

```bash
make test
make vet
make build
go test -c -o bin/lifecycle.test ./tests/integration
LOCALMOCK_DOCKER_TEST=1 ./bin/lifecycle.test -test.v
```

普通测试不访问 Docker 或模型 API。端到端测试显式启用后，会创建自己的容器组，验证真实请求、双场景隔离、停止、重复清理、启动超时和中断清理。

Mac 只编辑源代码时，可以使用 [remote-check.sh](scripts/remote-check.sh) 同步到 Debian，通过 Go 容器构建测试。脚本不复制模型密钥、不安装宿主机 Go，也不执行付费 Agent 调用：

```bash
bash scripts/remote-check.sh user@debian-host
```

支持 `LOCALMOCK_SSH_CONTROL` 指定已有 SSH 复用连接，`LOCALMOCK_REMOTE_DIR` 指定远端相对目录。若远端无法访问默认 Go 模块代理，可通过 `LOCALMOCK_GOPROXY` 指定可访问的代理；项目仍使用 go.sum 校验依赖。
