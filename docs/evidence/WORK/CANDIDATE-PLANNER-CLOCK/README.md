# CandidatePlanner 注入时钟

日期：2026-10-05

`NewCandidatePlannerWithClock` 让资源上界证据的新鲜度检查复用应用组合根的时钟；空时钟会在构造期拒绝。旧构造入口继续兼容，并明确采用系统时钟。

CandidatePlanner 用例已改为固定时钟，并通过推进时钟跨过证据到期时刻来断言失败，无需篡改 Planner 的私有依赖。

验证：`make check test-contract` 通过，包含 Go 格式、vet/build、依赖边界、Web 类型与生成类型检查，以及所有 Go internal race 测试。
