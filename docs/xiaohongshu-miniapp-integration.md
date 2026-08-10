# 小红书专业号小程序接入说明

## 1. 当前接入定位

当前先按景区已有企业专业号小程序的“商家自开发交易组件”接入，不先实现第三方服务商代开发和商家授权流程。

小红书负责笔记、直播、POI、专业号商品卡、支付凭证和原生订单中心；本项目继续负责产品来源、库存、销售订单、票权、景区核销、退款事实和渠道对账。小程序只承接小红书内商品详情、下单确认、支付拉起、订单详情和售后入口，不复制管理端业务规则。

官方资料：

- 小程序登录：`https://miniapp.xiaohongshu.com/doc/DC724193`
- `code2session`：`GET https://miniapp.xiaohongshu.com/api/rmp/session`
- 小程序代码构成：`https://miniapp.xiaohongshu.com/doc/DC451266`
- 交易组件说明：`https://miniapp.xiaohongshu.com/third/api-3rd-doc/rmpDeal`
- 小程序调用凭证：`POST https://miniapp.xiaohongshu.com/api/rmp/token`
- 生活服务商品同步：`POST https://miniapp.xiaohongshu.com/api/rmp/mp/deal/poi/product/upsert`
- 订单新增或修改：`POST https://miniapp.xiaohongshu.com/api/rmp/mp/deal/order/upsert`
- 担保支付订单查询：`POST https://miniapp.xiaohongshu.com/api/rmp/mp/deal/gpay_order/get`
- 凭证核销：`POST https://miniapp.xiaohongshu.com/api/rmp/mp/deal/voucher/verify`
- 售后新增：`POST https://miniapp.xiaohongshu.com/api/rmp/mp/deal/order/after_sales_order/add`
- 结算请求：`POST https://miniapp.xiaohongshu.com/api/rmp/mp/deal/settle`
- 消息加解密：`https://miniapp.xiaohongshu.com/third/api-3rd-doc/msgCrypt`

## 2. 已确认的业务语义

- 当前景区应使用自有小程序 `appid` 和 `appsecret` 获取两小时有效的调用凭证；系统必须缓存凭证，不能每次业务请求都重新生成。
- 景点身份同步生活服务商品时必须关联小红书 POI；团购、预售券和日历商品分别使用 `product_type=1/2/3`。
- 本地生活担保支付必须配置结算方式。优先按景区实际资质选择总店或门店结算，不由客户端自行提交。
- 本系统先创建外部订单，再将订单及小程序订单详情路径同步给小红书；小红书返回 `order_id`、`pay_token` 和支付类型。
- 支付结果以小红书担保支付订单查询、支付回调和本地订单状态共同幂等收敛，不能仅凭前端支付成功页面出票。
- 小红书返回的凭证码必须绑定到本项目既有票权；闸机扫码后，服务器调用小红书核销接口，取得 `verify_id` 后再完成可追溯核销。
- 官方公开规则中，普通团购券只支持核销前整单全额退款。小红书渠道订单不得套用本项目更宽松的部分退票规则。

## 3. 与现有渠道模型的映射

| 小红书事实 | 本项目事实 |
|---|---|
| 小程序应用 | 租户独立 `ChannelAccount` |
| 外部商品和 SKU | `ChannelProductMapping` 与售卖时产品版本快照 |
| `out_order_id` | 本项目销售订单号 |
| 小红书 `order_id` | 渠道协议关联，不覆盖本地订单号 |
| `open_id` | 按渠道账号隔离的游客标识；哈希检索、密文保存，不作为后台账号 |
| 凭证码 | 绑定到对应供应商、景区和票权，不成为跨景区通用票 |
| `verify_id` | 核销外部事实编号，与本地核销记录幂等关联 |
| 平台结算结果 | 渠道对账事实，不覆盖供应商核销收入和上下游结算账本 |

小红书应用凭据必须按销售租户单独加密保存。景区自营使用景区供应商租户的应用；未来分销商自有小红书账号接入时使用分销商租户的应用，但履约票权仍属于产品对应的供应商和景区。

## 4. 第一阶段实现状态

- 已建立隔离的官方 API 客户端，覆盖调用凭证缓存、生活服务商品同步、订单同步、担保支付订单查询和凭证核销。
- 已接入官方 `xhs.login -> code2session` 登录链路。一次性 `code` 只由后端交换，`open_id` 和 `session_key` 加密保存，`session_key` 不下发小程序；小程序只使用本系统生成的可轮换短期会话令牌。
- PostgreSQL schema 78 增加按租户和小红书渠道账号隔离的游客会话及加密消息事件。租户、渠道或相应业务能力停用后，既有小程序登录态立即失效。
- 已建立只读票种目录接口。目录只返回当前小红书渠道已启用映射且仍在线的本租户产品；客户端不能提交租户、供应商、景区或价格事实。
- 原生小红书小程序工程位于 `xiaohongshu-miniapp/xiaohongshu-miniapp`，当前已完成中文票种首页、真实登录、加载/空状态和失败重试，不包含模拟商品或模拟下单成功。
- 客户端已校验订单金额、商品必要字段、担保支付结算类型和单批最多 10 张凭证。
- 管理端已支持按租户创建小红书渠道账号，并配置 AppID、AppSecret、消息 Token 和 EncodingAESKey；敏感值加密保存且不会通过接口回显，组合型供应商/分销商租户仍按销售租户隔离凭据。
- 消息推送地址为 `https://<部署域名>/api/v1/integrations/xiaohongshu/events/<AppID>`。保存配置时的 GET 请求执行 SHA-1 验签并原样返回 `echostr`；POST 事件先验签、按官方 AES-CBC/PKCS#7 协议解密、校验明文 AppID，再加密且幂等入库后返回 `success`。无法验证或无法持久化的事件不会被确认。
- 尚未用真实小程序调用 `code2session`，也未创建或修改小红书商品；当前完成的是可测试的本地代码链路，不代表交易能力已验收。
- 下一步用开发工具验证真实登录和票种目录，并在小红书后台保存管理端生成的消息推送 URL、Token 和 EncodingAESKey；随后再接入 POI/类目选择、可靠商品同步任务、订单协议关联和担保支付，不能让票种预览绕过交易组件直接收款。

## 5. 真实联调前需要的资料

- 小程序 `AppID` 和 `AppSecret`，只能通过管理端加密配置，不进入代码、文档或日志。
- 小程序后台确认已开通本地生活担保支付，支付类型应返回 `life_gpay`。
- 已认领景区 POI 的名称和 `poi_id`。
- 景点门票对应的末级 `category_id`。
- 商品详情、订单详情和售后页面的小程序路径。
- 小红书要求配置的服务器域名、业务域名、回调地址和 IP 白名单。
- 一个只用于联调的低价测试商品及明确的退款、核销许可。

## 6. 第一阶段验收门槛

1. 商品同步成功且在正确景区 POI 下可见，不影响其他租户产品。
2. 小程序下单只提交本项目签发的产品和价格事实，客户端不能篡改金额、供应商或景区。
3. 小红书支付成功后，本地订单、支付、履约单和票权幂等生成；超时、重复查询和进程重启不重复出票。
4. 小红书凭证只能在其票权所属景区核销；重复核销、跨景区核销和外网超时均安全失败。
5. 核销成功保存小红书 `verify_id`，退款与核销互斥规则符合小红书渠道政策。
6. 小红书订单、退款、核销和结算可以逐笔对账，差异不直接覆盖原始事实。
