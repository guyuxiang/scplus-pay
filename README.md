**SC+Pay** 是一个基于 Go 构建、支持私有化部署的 **多链多币种 Crypto 支付网关**。让任意网站或应用都能快速接入多条链、多种代币的加密支付能力。没有第三方托管，没有平台抽成，资金直接进入你的钱包。

私有部署，按 HTTP API 接入，几分钟内就可以开始接收 **Crypto Payments**。

### 已支持网络与代币

| 网络 | 代币 |
|------|------|
| **TRC20** (Tron) | USDT、TRX |
| **ERC20** (Ethereum) | USDT、USDC、ETH |
| **Solana** | USDT、USDC |
| **BEP20** (BSC) | USDT、USDC、BNB |
| **Polygon** | USDT、USDC |
| **Aptos** | USDC、USDT |
| **更多** | 持续扩展中… |

---

## 广泛兼容，即插即用

无论你运营的是哪类系统，SC+Pay 均可基于现有接口方案，**无需重构业务逻辑**，快速接入，立即获得 Crypto 收款能力，低成本扩展全球支付场景：

| 领域 | 已支持系统 |
|------|-----------|
| **AI 分发** | [Sub2API](https://github.com/Wei-Shaw/sub2api)、[NewAPI](https://github.com/QuantumNous/new-api) |
| **发卡系统** | [独角数卡（Dujiaoka）](https://dujiao-next.com/)、[异次元发卡](https://github.com/lizhipay/acg-faka) |
| **代理面板** | [V2Board](https://github.com/v2board/v2board)、[XBoard](https://github.com/cedar2025/Xboard)、[xiaoV2board](https://github.com/wyx2685/v2board/)、[SSPanel](https://github.com/anankke/sspanel-uim) |
| **建站生态** | [WordPress](https://wordpress.com/)、[WHMCS](https://www.whmcs.com/) |
| **Epay 兼容** | 兼容各类支持 Epay 易支付接口的平台 |
| **更多** | 简易 HTTP API，10 分钟内接入 |

---

## 核心特性

- **多链多币种** — 支持 TRC20、ERC20、BEP20、Polygon、Aptos 等主流网络
- **私有化部署** — 资金完全自主掌控
- **零依赖运行** — 单个二进制即可启动，低并发场景无需 MySQL + Redis
- **跨平台** — 支持 x86 / ARM 架构的 Windows / Linux / Mac
- **多钱包轮询** — 自动轮换收款地址，提高并发处理能力
- **异步队列** — 高性能消息回调，适配高并发场景
- **HTTP API** — 标准化接口，任何语言 / 框架都能快速集成
- **Telegram Bot** — 实时支付通知，快捷管理与监控

---

## 文档与教程

完整文档请访问：****

快速入门：

| 教程 | 说明 |
|------|------|
| [Docker 部署]() | 推荐方式，一键启动 |
| [手动部署](h) | 完全手动控制 |
| [开发者 API 文档]() | 接口集成指南 |


---

## 实现原理

SC+Pay 通过监听多条区块链网络（TRC20、ERC20、BEP20、Polygon 等）的 API 或 RPC 节点，实时捕获钱包地址的代币入账事件，利用**金额差异**与**时效性**精确匹配交易归属：

```text
工作流程：
1. 客户发起支付，需支付 20.05 USDT
2. 系统在哈希表中查找可用的钱包地址 + 金额组合
3. 若 address_1:20.05 未被占用 -> 锁定该组合（有效期 10 分钟），返回给客户
4. 若已被占用 -> 自动累加 0.0001 尝试下一个金额组合（最多 100 次）
5. 后台线程持续监听所有钱包的入账事件，金额匹配则确认支付成功
```
