# 学习文档导航

先完成[运行与调试](RUNNING.md)的Docker快速启动，再读[迁移分析](MIGRATION_ANALYSIS.md)，了解Java项目真实链路和为什么要修。随后按[学习路线](LEARNING_PATH.md)循序阅读，不建议直接从秒杀Lua开始。

| 目标 | 文档 |
|---|---|
| 知道项目如何启动 | [项目README](../README.md)、[运行与调试](RUNNING.md) |
| 理解整体代码边界 | [架构](ARCHITECTURE.md) |
| 对照Spring学习Go | [Java → Go](JAVA_TO_GO.md) |
| 查接口和示例 | [API](API.md)、[OpenAPI](../api/openapi.yaml) |
| 自己从零重写 | [学习路线](LEARNING_PATH.md)、[实现指南](IMPLEMENTATION_GUIDE.md) |
| 深入Redis与缓存 | [Redis](REDIS.md) |
| 深入Blog社交 | [Social](SOCIAL.md) |
| 深入秒杀 | [Seckill](SECKILL.md) |
| 看表、索引和迁移 | [Database](DATABASE.md) |

建议在编辑器左右分别打开Java与Go：例如`src/main/java/com/hmdp/service/impl/UserServiceImpl.java`对`go-dianping/internal/service/user.go`。先写出原调用链，再解释Go中Context、error和显式依赖如何取代ThreadLocal、Exception和IOC。
