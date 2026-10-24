# DSL v1alpha1

文件使用 `apiVersion: localmock/v1alpha1`。当前支持 `kind: Service`、`kind: Scenario` 和 `kind: Mock`，可运行例子位于 `examples/http-smoke` 与 `examples/online-boutique`。

解析拒绝未知字段、重复字段、多个 YAML document，以及超过 4 MiB 的定义/契约文件。相对路径以包含引用的 YAML 文件所在目录为基准；Dockerfile 相对于构建上下文。

## Service

- `metadata.name`：稳定逻辑服务名。
- `provides.<interface>.protocol`：http、https、grpc、thrift 或 custom。
- `provides.<interface>.contract`：本地契约文件，记录 SHA-256。gRPC 会编译 proto 并验证服务存在性，其他协议目前仅校验文件与引用。
- `provides.<interface>.service`：gRPC 必填的完整 protobuf service 名称。文件名相同不能代替服务契约相同。
- `implementations.<name>.type`：source、image 或 mock。
- `implementations.<name>.role`：real 或 mock，独立于打包方式。
- source 使用 `build.context`、`build.dockerfile` 和可选 `build.args`；image 使用 `image`；mock 使用 `mock` 引用定义文件，三者不能混用。构建参数会被记录，不应用于传递秘密。
- `endpoints.<interface>.port`：容器内端口。
- `env`：实现默认环境变量。
- `command`：可选容器 command 参数，不覆盖镜像 ENTRYPOINT。
- `readiness.command`：必填，exec 形式的健康检查；程序必须存在于镜像中。当前默认间隔 2s、超时 3s、重试 60 次，启动宽限期 5s。
- `dependencies.<alias>`：所选实现自己的依赖，包括 `service`、`interfaces` 和 `inject.env`。

配置式 mock 必须使用 `role: mock`，当前只支持一个 gRPC endpoint；command、readiness 自动生成，不能另行声明依赖。自定义代码/镜像 mock 仍可声明自己的接口和依赖。

服务名、接口名、实现名、部署名使用小写字母、数字和连字符，以字母开始，最多 48 字符。

地址引用仅允许完整表达式：

```yaml
inject:
  env:
    PAYMENT_ADDR: "${dependency.payment.charge.address}"
    PAYMENT_URL: "${dependency.payment.refund.url}"
```

`address` 生成 `deployment:port`；`url` 仅适用于 HTTP(S)。表达式必须引用本依赖声明的接口。依赖注入键不能被实现默认值、场景覆盖值或其他依赖重复占用。

## Scenario

- `metadata.name`：场景名。
- `catalog`：Service 文件列表，同名服务不能重复出现。
- `deployments.<name>.service`：逻辑服务。
- `deployments.<name>.implementation`：该服务已定义的实现名称。
- `deployments.<name>.config.env`：普通配置覆盖。
- `deployments.<name>.publish`：旧字段，非空时拒绝执行；改用 `localmock probe` 在隔离网络内部访问接口。
- `components`：用户手动选择的镜像基础组件；可用 `components add/remove` 编辑。`initialize` 声明组件内按序执行的命令、可选 stdin 文件和超时，初始化成功后启动业务。字段、地址引用、IP 槽位和快照规则见 [组件与网络](components.md)。
- `bindings.<caller>.<dependency>`：目标部署名。

一个调用方对同一逻辑服务只能声明一项依赖；这一项可以包含多个协议接口。所有接口共享同一目标部署。禁止将一个目标服务拆成两个依赖来绕过统一替换规则。

依赖图允许环，不自动转换为 Compose 启动顺序。服务需能容忍并行启动时的短暂依赖不可用。所有部署均通过健康检查后 Run 才被记录为 ready。

## 执行与产物

`plan` 输出确定的容器规格，不启动 Docker；其中包含有效环境配置，请将敏感值视为本机私有数据。

`up` 保存输入文件摘要、输入副本、执行规格、构建上下文快照、生成的 Compose 文件、启动日志和启动时容器信息。构建上下文最大 256 MiB，暂不支持符号链接，状态目录不能位于构建上下文内。排除 `.git`、`.localmock` 和 macOS 元数据；尚未按 `.dockerignore` 精简磁盘快照，Docker 构建自身仍使用 `.dockerignore`。

生成的 Compose 文件是执行产物，不是用户的编辑入口。环境变量中的 `$` 被转义，避免 Compose 隐式读取宿主机变量。场景不支持任意 Compose 片段、宿主机目录挂载、特权模式或 Docker socket 挂载。

业务容器默认 1 CPU、512 MiB、256 PIDs；组件可设置 `memoryMB`（64–16384）。日志轮转为 10 MiB × 3。探测容器使用独立的小资源配额。

源码构建产生 Run 专属镜像标签。`down` 保留镜像及构建缓存，不做全局清理。它们可能随使用增长，需后续增加明确的镜像/历史保留命令。

## 配置式 gRPC Mock

```yaml
apiVersion: localmock/v1alpha1
kind: Mock
metadata: {name: payment-error}
protocol: grpc
contract: ./demo.proto
service: hipstershop.PaymentService
rules:
  - method: Charge
    delayMs: 100
    error: {code: Unavailable, message: simulated outage}
```

每个方法最多一条固定规则。`response`（protobuf JSON 对象）与 `error` 必须二选一；`delayMs` 可选，范围 0..300000。编译阶段校验方法、响应字段/类型、错误码以及目标 service。缺失规则的已声明方法返回 Unimplemented；没有真实服务回退。暂不支持流式 RPC、请求条件匹配和有状态规则。

执行时将 descriptor set、规则和静态 Linux CLI 打包成独立 mock 镜像，不需要容器联网下载依赖，也不依赖宿主机 proto 文件。CLI 必须用 `CGO_ENABLED=0` 构建。

`localmock rpc --address HOST:PORT --proto demo.proto --method package.Service/Method --data '{}'` 支持明文 unary gRPC 调用，输出响应、状态和 trace ID。日志中的 trace ID 是实际收到的上下文，不推断或补造缺失链路。

## 当前未支持

HTTP 配置式 mock、Thrift/自定义 RPC 适配、Git ref 拉取命令、配置 profile/秘密引用、数据卷声明、跨版本 IDL 兼容性比较、历史运行精确重放、TUI 和完整 Trace 展示。
