# Online Boutique：真实 checkout + 六个 gRPC mock

上游项目：[GoogleCloudPlatform/microservices-demo](https://github.com/GoogleCloudPlatform/microservices-demo)。固定源码 commit 为 `38e7348eb289eb5b87c0c6e8cb19ced0449dc389`。

checkout 的业务代码原样从源码构建；购物车、商品、汇率、支付、物流、邮件均由本项目的 Go gRPC mock runtime 提供。每个依赖是独立部署，可通过实现名称替换整个依赖。未配置的方法返回 Unimplemented，不会连接真实服务。

## 准备

在 Linux Docker 主机上，从 local-mock 仓库根目录运行：

```bash
make build
bash examples/online-boutique/prepare.sh
./bin/localmock validate -f examples/online-boutique/scenario.yaml
./bin/localmock up -f examples/online-boutique/scenario.yaml --json
./bin/localmock ps --json
```

`prepare.sh` 获取固定版本，检查上游 tracked 文件未修改，并添加独立 Dockerfile 与静态健康检查工具。不修改上游业务源代码。需要 GitHub 访问权限；网络受限时，可把同一 commit 的完整 Git checkout 放到 `.localmock/repos/online-boutique` 后重新运行。必须使用 `CGO_ENABLED=0` 构建 localmock（Makefile 已设置）。

上游模块声明 Go 1.27.1 工具链；构建适配使用对应 Go 容器，不在宿主机安装。此示例的 `build.args.GOPROXY` 设置为 `https://goproxy.cn,direct`，可在 `services/checkout.yaml` 中改为本地可访问的 Go 模块代理。

## 请求与日志

使用网络内部的 probe，不需要发布 checkout 端口：

```bash
./bin/localmock probe \
  --deployment checkout --interface grpc \
  --method hipstershop.CheckoutService/PlaceOrder \
  --data-file examples/online-boutique/request.json RUN_ID
./bin/localmock logs RUN_ID
./bin/localmock down RUN_ID
```

成功场景应返回订单、`local-shipment-001` 物流编号和两件商品。汇率响应固定为 USD 10，仅用于联调流程，并不是正确的通用汇率服务。

使用 `payment-failure.yaml` 启动新 Run，可模拟支付 Unavailable。checkout 自身会将支付错误包装为 Internal；请求应失败，不应执行发货和邮件确认。

## 追踪能力边界

`probe` 为请求生成 trace ID，并通过 W3C traceparent 元数据传入。mock 输出方法、匹配结果、状态、耗时和收到的 trace ID，不记录银行卡等请求正文。

这些事件用于联调证据，尚不是完整 OpenTelemetry span 图。当前上游 checkout 的 `convertCurrency` 使用 `context.TODO()`，会丢失继承的上下文；汇率 mock 事件可能没有相同 trace ID。样例保留这一行为，避免伪造完整链路。调用链收集与缺口展示仍待实现。

## 自动验收

```bash
go test -c -o bin/lifecycle.test ./tests/integration
LOCALMOCK_DOCKER_TEST=1 LOCALMOCK_BOUTIQUE_TEST=1 \
  ./bin/lifecycle.test -test.run TestBoutique -test.v
```

测试使用独立 Run，完成后清理运行资源；镜像和构建缓存保留。
