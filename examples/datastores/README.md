# MySQL / Redis 初始化与业务读写

这个场景启动真实 MySQL、Redis，以及一个使用 Go MySQL/Redis 客户端的订单示例服务。平台先执行 schema.sql、seed.sql 和 Redis 初始化步骤，再启动订单服务。服务启动时必须读到预置商品和缓存内容。

在 Linux Docker 主机运行：

```bash
./bin/localmock validate -f examples/datastores/scenario.yaml
./bin/localmock plan -f examples/datastores/scenario.yaml
./bin/localmock up -f examples/datastores/scenario.yaml --json
./bin/localmock probe --deployment orders --interface api --path /exercise RUN_ID
./bin/localmock logs RUN_ID
./bin/localmock down RUN_ID
```

`/exercise` 是专用于验收的 GET，会创建一条订单、从 SQL 读回、写入 Redis 并读回；重复请求会创建更多订单。当前 HTTP probe 只支持 GET，示例不作为生产 REST 接口设计参考。

第一次响应的 body 包含：

```json
{
  "order_id": 1,
  "product_id": 1,
  "product": "local coffee",
  "cached_product": "local coffee",
  "greeting": "hello from fixtures"
}
```

`down` 会删除数据；重新 `up` 得到新 Run，订单编号重新从 1 开始。初始化记录及输入快照保存在状态目录的 `runs/RUN_ID/`，不受工作区后续文件编辑影响。

应用构建使用独立 go.mod/go.sum，不增加平台本身的数据库客户端依赖。构建阶段需要下载 Go 模块；如果开发主机无法访问默认代理，可在 service.yaml 的 `build.args.GOPROXY` 中显式指定可访问的模块代理。业务运行阶段只有 Run 内部网络。

镜像使用本地测试配置，连接地址由 `${component...}` 引用注入。修改 fixtures/schema.sql、fixtures/seed.sql 或 Redis `initialize` 步骤即可定义另一组测试数据。每个新 Run 使用自己的数据，不连接宿主机现有数据库。

该例覆盖 SQL、Redis string/hash/TTL，以及业务实际读写；没有增加配置文件挂载、数据持久化或自动识别 SDK 配置。
