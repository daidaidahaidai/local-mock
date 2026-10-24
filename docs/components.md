# 基础组件与隔离网络

用户在 Scenario 的 `components` 中选择要启动的注册中心、配置中心、数据库和中间件。没有声明的组件不会启动；Agent 不能增删或替换这些选择。Nacos 等组件直接运行真实镜像，平台不模拟注册中心协议。

## 选择组件

```bash
localmock components list
localmock components show nacos
localmock components show mysql
localmock components show redis
localmock components add redis --name cache -f scenario.yaml
localmock components add mysql --name orders-db -f scenario.yaml
localmock components remove cache -f scenario.yaml
```

`show` 只输出可编辑的 YAML 片段，不启动容器，也不修改现有文件。把选中的条目合并到场景唯一的 `components` 字段中，再使用 `validate / plan / up`。这些模板使用测试环境配置：Nacos 关闭客户端认证，MySQL 使用空的本地 root 密码，Redis 不设认证；不复制线上凭据。

`add` 将完整模板写入 DSL；不存在的场景文件会创建为只包含所选组件的场景。可用 `--name` 添加多个同类实例，名称重复则拒绝；IP 槽位优先使用模板值，冲突时选择 10–99 中的空闲槽位并写入文件。`--image` 可覆盖模板镜像，用户需要保证该镜像与模板的命令、环境变量及健康检查兼容。任意其他镜像仍可直接写入 DSL。

`remove` 删除一个组件；如果当前业务或其他组件仍通过 `${component...}` 引用它，或者删除后场景为空，则拒绝修改。添加/删除均在候选文件通过完整校验后原子替换，保留其他字段、注释和相对路径；不会启动、停止或修改已有 Run。YAML 排版可能规范化；编辑命令暂不支持 YAML alias/merge。并发编辑通过场景旁的 `.localmock-lock` 文件互斥；异常终止留下锁时，确认没有编辑进程后可删除该锁。

这些命令允许名称位于选项之前或之后；其他运行命令仍要求选项放在 Run ID 前。

完整例子为 [examples/infrastructure/scenario.yaml](../examples/infrastructure/scenario.yaml)。可以删除不需要的组件；只运行基础组件时允许省略 catalog 和 deployments。它只启动组件，接入业务服务还需要显式配置对应 SDK 地址。

```yaml
components:
  cache:
    roles: [cache]
    image: redis:7.4.9-alpine
    ipOffset: 40
    memoryMB: 256
    command: [redis-server, --save, "", --appendonly, "no"]
    endpoints:
      redis: 6379
    readiness:
      command: [redis-cli, ping]
```

组件角色为 `registry / config / database / cache / middleware`，可多选。同一 Nacos 实例可以同时承担 registry 与 config；也可以声明两个不同名称、不同 IP 槽位的实例。角色用于表达选择意图，不会自动把业务 SDK 接入该组件。

`image` 必填，可直接使用用户的本地镜像或私有仓库镜像；私有仓库由用户事先完成 `docker login`。模板不构成镜像白名单。推荐固定版本或 `@sha256:...`，运行诊断中的容器信息记录实际 image ID。模板版本不会自动追随 latest。

无现成镜像的内部组件，可以自行构建兼容服务镜像并按同一格式接入。平台要求它声明端口、启动参数和 exec 健康检查；不会通过修改 Compose 或开放宿主机网络来绕过约束。自定义注册协议的适配器和 SDK 配置分析尚未实现，不会因为填写了 `roles: [registry]` 就自动获得协议兼容性。

## 连接组件

业务服务的实现默认 `env` 或部署的 `config.env` 可使用完整引用：

```yaml
config:
  env:
    REDIS_ADDR: "${component.cache.redis.address}"
    MYSQL_HOST: "${component.mysql.sql.host}"
    MYSQL_PORT: "${component.mysql.sql.port}"
    NACOS_SERVER_ADDR: "${component.nacos.http.address}"
```

组件名称必须与实际选择一致。`address` 生成 `组件名:端口`，`host` 生成内部 DNS 名称，`port` 生成端口数字。仅支持完整表达式，暂不支持字符串拼接。未知组件/端口或错误表达式在启动前失败。环境变量名需要按被测服务实际 SDK 配置；示例变量名不是跨框架标准。

Agent 会保留种子场景中已有的组件地址引用，不能将其改为其他地址；组件环境变量不作为组件上下文发送给模型。组件 Env 仍会保存在本机 DSL 和私有运行产物中，勿放线上秘密。

所有基础组件健康且初始化成功后才启动业务服务。组件之间目前并行启动，存在互相依赖的自定义组件需要自行处理启动重试；尚不支持组件依赖排序或挂载自定义配置文件。镜像内配置、环境变量和 command 可用。

## 初始化测试数据

在选中的组件内声明 `initialize` 步骤，例如：

```yaml
# 以下片段分别放入已有 mysql / redis 组件定义中
initialize:
  - name: schema
    command: [mysql, --host=127.0.0.1, --user=root, --database=localmock]
    stdin: ./fixtures/schema.sql
    timeoutSeconds: 30
```

```yaml
initialize:
  - name: greeting
    command: [redis-cli, -e, SET, 'fixture:greeting', hello]
  - name: session
    command: [redis-cli, -e, SET, 'fixture:session', active, EX, '3600']
  - name: customer
    command: [redis-cli, -e, HSET, 'fixture:customer', name, Alice]
```

步骤在**该组件容器内**以 exec 方式执行，使用镜像自带的客户端；无需在宿主机安装 mysql/redis-cli。`command` 是字面量 argv，不做 shell、变量或组件引用展开；需要 shell 时必须显式写出 `sh -c`。Redis 命令使用 `-e`，使服务端错误产生非零退出码；MySQL 不使用会忽略 SQL 错误的 `--force`。自定义镜像按相同机制执行，但必须自行保证命令返回码反映失败。

`stdin` 可省略；填写时相对场景文件解析，必须是普通文件且不超过 4 MiB。加载时记录哈希，启动前验证并复制到 Run 的 inputs，执行只读取已验证的快照，通过 Docker exec 的 stdin 传入；不绑定挂载宿主机文件。SQL 可拆分成 schema、seed 等多个步骤。Redis 批量文件也可通过 `redis-cli --pipe` 接收 RESP 格式输入。

组件按名称排序，组件内部按声明顺序执行，每个组件最多 64 步。每步默认超时 30 秒，可设置 `timeoutSeconds` 为 1–300；`up --timeout` 仍限制整个启动过程。每个新 Run 执行一次，**失败不重试、不启动业务**，保留诊断后清理该 Run。取消和超时也走同一清理流程；中断的 Run 不恢复初始化，新建 Run 重新执行。

执行记录保存到 Run 目录下 `initialization.jsonl`，包含步骤名、开始/成功/失败、输入哈希及最多 32 KiB 的 stdout/stderr；`down` 后仍保留。运行产物可能包含测试数据，请勿在初始化脚本中放置线上凭据。用户的初始化声明会原样经过 Agent 流程保留，不由 Agent 修改。

完整业务示例见 [MySQL/Redis 读写验收](../examples/datastores/README.md)。

## 网络与地址分配

运行要求 Linux Docker Engine 28+、支持 `up --wait` 的 Compose，以及静态 Linux CLI。每个 Run 由 Runtime 创建独立的用户定义 bridge 网络，设置 `internal` 与 IPv4/IPv6 `gateway_mode=isolated`；使用 Docker IPAM 选取不与已有 Docker 网络重叠的子网。网络创建失败直接终止，不回退到默认网络。

IP 槽位是相对子网起点的偏移量：

| 槽位 | 用途 |
|---|---|
| 2 | 平台内部探测容器 |
| 10–99 | 用户选择的组件，显式指定 `ipOffset`，禁止重复 |
| 100–249 | 业务部署，按部署名排序确定 |

例如两个 Run 的注册中心都选槽位 10，会得到各自子网的 `.10` 地址，而不是复用同一个全局地址。实际网络、子网和地址表保存为 Run 目录下的 `network.json`。网络由 Runtime 管理，生成的 Compose 通过 external network 引用它；external 在这里表示由 Compose 之外的 Runtime 创建，不表示有外网连接。

所有容器只连接此网络，不发布宿主机端口，不挂载宿主机目录或 Docker socket，不启用 privileged；移除 NET_RAW/NET_ADMIN，禁用容器 IPv6。Docker 内部 DNS 解析本次网络内的服务，配置的上游为容器自身 loopback，不继承宿主机 DNS。未解析的名称不会回退线上。

启动后和执行 probe 前检查实际网络/容器设置，任何不符合隔离策略的状态都会报错。启动检查失败进入原有回滚流程。隔离针对容器运行流量，不包括宿主机控制面的 Git、拉镜像、构建依赖下载和模型请求；控制面不向容器提供网络代理。Docker 管理员仍能在工具之外修改网络，本工具不防御有宿主机管理权限的主动篡改。

实现参考：[Docker gateway modes](https://docs.docker.com/engine/network/port-publishing/#gateway-modes)。单独 `internal: true` 仍有访问宿主机桥接地址的可能，故同时使用 isolated 模式。测试只访问自建 canary 和另一个测试 Run，不触碰线上服务。

## 发起请求与清理

```bash
localmock probe --deployment caller --interface api --path / RUN_ID
localmock probe --deployment checkout --interface grpc \
  --method hipstershop.CheckoutService/PlaceOrder \
  --data-file examples/online-boutique/request.json RUN_ID
localmock probe --deployment cache --interface redis RUN_ID
```

HTTP 使用 GET；gRPC 使用已快照的 descriptor set；基础组件端口支持 TCP 连通性探测。Probe 在 Run 内部的静态工具容器执行，CLI 通过 Docker exec/stdin/stdout 传递请求和结果，不在宿主机启动业务代理。HTTP 不使用代理、不跟随重定向。旧场景的非空 `publish` 会被拒绝，需要删除并迁移到 `probe`。

`stop` 停止组件、业务和探测容器；`down` 删除本次容器、匿名数据卷和受管理网络。数据不跨 Run 复用，保留镜像/构建缓存及诊断记录。启动失败和取消走相同的按 Run 清理流程。

镜像模板参考：[Nacos Docker](https://github.com/nacos-group/nacos-docker)、[MySQL 官方镜像](https://hub.docker.com/_/mysql)、[Redis 官方镜像](https://hub.docker.com/_/redis)。
