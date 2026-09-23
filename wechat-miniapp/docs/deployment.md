# 部署与上线验收

> 当前生产架构：微信小程序只访问本 SaaS 的 HTTPS storefront API。CloudBase 云函数、数据库、支付和存储章节均为历史迁移资料，不能继续作为生产运行路径。正式上线前必须完成 SaaS 渠道绑定、真实微信登录、支付/退款适配和现场验收；未完成时保持 demo 或明确 fail-closed。

## 1. 模式与环境

`miniprogram/config/runtime.js` 默认 `deploymentMode: 'demo'`，完全使用本地演示数据，不调用收款接口。正式包须配置 `deploymentMode: 'production'`、SaaS `apiBaseUrl`（HTTPS）和正式微信 AppID；AppSecret 只保存在 SaaS 的 `wechat_miniapp` 渠道账号密文中，不能写入小程序。API 或登录配置缺失时必须失败，不能回退演示支付。检查 `project.config.json` 的 AppID 与发布配置一致。

正式发布前，在 SaaS 管理后台完成以下配置：

1. 为租户开通“餐饮”或“电商”商业能力，并创建同租户、同业务类型的履约地点和在线商品/SKU/库存。
2. 在渠道管理创建并配置 `wechat_miniapp` 渠道账号，保存 AppID/AppSecret；密钥不会返回给小程序。
3. 在商业 storefront 发布配置中，将该渠道账号绑定到业务类型和履约地点。绑定接口为 `/api/v1/commerce/storefront-bindings`，保存后会写租户审计。
4. 将 SaaS HTTPS 域名加入微信小程序业务域名，开发者工具中使用生产配置重新编译；先验证登录、目录、购物车、地址快照和订单查询，再进行支付联调。

当前 `commerce_storefront_bindings` 对同一 `channel_account_id` 只允许一个 active binding，且同一 AppID 不能同时映射 `restaurant` 和 `retail`。因此小程序虽然保留“外卖”和“冷吃”两个入口，但生产运行时只开放 SaaS 返回的那个业务频道，不能用客户端参数把同一 session/cart 伪装成另一业务。若要同时经营两个业务，后端必须改为支持按业务拆分的绑定模型（例如两个 channel account 或明确的多 binding/session 隔离）；在后端改造完成前，应按单业务发布。

当前 SaaS storefront 尚未提供配送区域列表或跑腿费/配送费字段，餐饮生产 checkout 暂只开放门店自提；校园配送入口会显示待接入并被拒绝，避免客户端本地计算的跑腿费与服务端订单金额不一致。优惠券、助力、商家工作台、配送区域管理同样保持明确不可用，不能用缓存或历史 CloudBase 数据补齐。

微信开发者工具私有配置可能覆盖公共配置；本机 `project.private.config.json` 的 `urlCheck: false` 只是开发者个人设置，发布验收时必须打开合法域名校验。营业时间文字是展示说明，当前接单权限由营业开关控制，不是自动排班。

## 2. 数据库与权限（历史 CloudBase 资料，仅供迁移核对）

以下集合、云函数和 CloudBase 规则不属于 SaaS 生产运行路径。不要在正式包中初始化 CloudBase，也不要把这些集合当成商业订单或库存的第二权威；若仍需读取旧数据，必须另行设计一次性、只读、可审计的迁移。

在目标环境创建 [集合清单](../database/collections.md) 中的全部集合，包括空的 `address_books`、`payment_records`、`operation_logs`。种子文件是“集合名→数组”的清单，须按集合分别导入，不能当作一个集合的导入文件。替换示例店主OpenID、门店电话、校区/价格/活动日期；不要将示例手机号用于营业。

按 [索引清单](../database/indexes.md) 建立实际查询需要的索引。`database/security-rules.json` 也是逐集合规则清单，**不是可直接一次导入的平台权限文件**。逐集合设置并用非管理员账号验证。订单/账单/活动/地址簿/日志不允许客户端直接读写；订单只能经过脱敏云函数获取。云函数管理端不依赖客户端角色。

员工权限：

- `ORDER_MANAGE`：查看含收货信息的订单、制作/配送/完成。
- `REFUND`：发起、重试、核实退款。工作台展示订单同时需要 `ORDER_MANAGE`。
- `PRODUCT_MANAGE`：商品图片、内容、价格、规格、库存、上下架。
- `STORE_SETTING`：门店、配送区域、活动和日志。

员工授权由可信管理员在控制台维护，不开放客户端自助提权。商品图片上传到 `products/store_001/`，在云存储配置公开读取、仅可信店员可写；不得为实现上传而将整个存储设为公开写入。请在目标云环境验证权限表达式/角色方案和上传行为。替换图片不会自动删除旧文件，避免影响历史订单；可在确认无引用后另行清理。

## 3. 旧 CloudBase 支付资料（仅存档，不可用于生产）

本节保留历史协议，便于核对旧版本数据和回滚调查。它不再是当前小程序的支付实现，不能通过配置环境变量重新启用。

当前下单/查单/关单/退款使用 `wx-server-sdk@4.0.2` 的 `cloud.cloudPay`。须在云开发控制台完成云支付开通、小程序与商户/子商户绑定。新文档中的 `pay-common` HTTP函数不是此集成的替代配置，不能只改参数名后混用。

下列 **四个云函数均需** 设置公共参数：

`payment`、`paymentCallback`、`order`、`scheduledTasks`

| 变量 | 说明 |
|---|---|
| `WECHAT_PAY_APPID` | 实际顾客小程序AppID（子应用身份） |
| `WECHAT_PAY_MCH_ID` | 云支付商户/服务商配置 |
| `WECHAT_PAY_SUB_MCH_ID` | 实际收款子商户；无独立子商户时与MCH_ID一致 |
| `CLOUDBASE_ENV_ID` | 实际云环境ID字符串；**不能用DYNAMIC_CURRENT_ENV符号代替** |
| `WECHAT_PAY_CALLBACK_FUNCTION` | 默认 `paymentCallback` |
| `WECHAT_ORDER_NOTICE_TEMPLATE_ID` | 商家新订单订阅消息模板ID；四个共享支付/订单函数统一设置，未配置则不发送 |
| `WECHAT_MINIPROGRAM_STATE` | 可选，订阅消息跳转的小程序状态；不填默认为 `formal` |

SDK不会自动补齐全部接口的随机串，代码显式生成 `nonceStr`。查询响应必须同时包含成功的 `returnCode/resultCode`，核实金额、交易号、小程序/子商户和付款人。legacy可选 `fee_type` 按协议缺省为CNY，金额和身份不从本地订单补齐。

支付回调把来参当作查单提示，二次请求微信并完成事务后返回 `{ errcode: 0, errmsg: '' }`。legacy退款不依赖不存在的HTTP回调参数：由定时任务和门店查单确认，使用SDK真实的 `refundFee_0/outRefundNo_0` 等字段。重试始终复用原商户退款号。

退款查单仅传当前outRefundNo，避免按支付单查单默认前10笔的分页限制；新退款号只用于已核实关闭且由店员明确确认的重新发起。

### 可选：已配置的微信支付v3 HTTP通知入口

仅 `paymentCallback` 的验签解密分支需要：

- `WECHAT_PAY_API_V3_KEY`（32字节）或对应BASE64变量；
- `WECHAT_PAY_PLATFORM_CERTIFICATE` 或对应BASE64变量；
- `WECHAT_PAY_PLATFORM_SERIAL_NO`（建议强制指定并按平台证书轮换维护）。

只有确实配置了微信HTTP通知地址时才启用该分支。网关必须原样传递原始body及 `Wechatpay-*` 请求头，并正确映射函数的HTTP `statusCode/headers/body` 响应。代码测试了RSA-SHA256验签、AES-GCM解密、时间窗及服务商字段，但**尚未验证实际网关映射或真实微信通知**。

v3标准退款通知不含appid/mchid，只有在完成验签、使用该商户APIv3密钥解密后才允许缺省，随后仍逐一核对原交易号、退款号、微信退款ID、总额/退款额与币种。密钥绝不能写入小程序、种子数据、日志或Git。

### 商家新订单订阅消息

订单通知使用一次性订阅消息，不是轮询或短信兜底。先在小程序后台创建与代码字段匹配的模板，再把模板ID同时填入 `miniprogram/config/notifications.js` 的 `orderNoticeTemplateId` 和 `order`、`payment`、`paymentCallback`、`scheduledTasks` 四个函数的 `WECHAT_ORDER_NOTICE_TEMPLATE_ID`。当前发送字段为 `thing1`、`amount1`、`phrase2`、`time3`；若后台模板关键字不同，须同步调整 `cloud-shared/payment-core.js` 后重新构建共享副本。

每个有 `ORDER_MANAGE` 权限的店员都要在生产包工作台点击“开启新订单提醒”并同意订阅。普通 `WAIT_PAY` 订单不发送；订单确认进入 `PAID` 后才尝试发送，发送失败不会回滚订单，工作台仍是最终处理入口。没有模板ID、店员未授权或模板额度耗尽时，必须依靠工作台刷新发现订单，不能把订阅消息当作订单可靠性保障。

## 4. 打包、上传与定时器

先执行 `npm run build:cloud`、`npm run verify`、`npm test`。共享核心会复制到4个函数目录；禁止只修改生成副本。各业务函数独立安装 `wx-server-sdk@4.0.2` 依赖并上传部署，函数运行时建议Node.js 20（以目标平台支持为准）。

部署函数：bootstrap、catalog、address、coupon、order、payment、paymentCallback、assist、merchant、scheduledTasks。模板 `quickstartFunctions` 不用于生产。上传脚本只负责函数部署和共享代码一致性检查，不配置远端环境变量、索引、权限或存储规则。

`scheduledTasks/config.json` 定义名为 `everyTenMinutes` 的10分钟定时器。部署后确认控制台实际启用，并检查事件字段 `Type: Timer / TriggerName: everyTenMinutes`。不要为定时器开放公共HTTP入口。客户端即使伪造Timer参数也会被拒绝。

失败退款轮转处理，避免最早100笔长期失败导致后续订单饥饿；未确认退款会保留原退款号重试。需监控定时器失败、`pendingVerification`、`refund_dispatch_pending` 日志，并配置云监控告警。没有外部告警接收人配置时不能把日志当作已经通知了店员。

## 5. 资金与故障处理

- 未尝试支付的订单可事务取消；已尝试支付的订单先冻结新的支付创建，再查询微信并明确关单成功后释放库存/锁券。
- 网络超时、未知交易状态、空响应、ORDERNOTEXIST不构成关单证明，不自动释放。付款重试可以复用原交易号；取消核实中的订单暂停继续支付。
- 若已取消订单收到完全核实的迟到付款，持久化为REFUNDING并发起原路全额退款；网络失败由定时器同号补发。已释放库存/券不再二次扣减。
- REFUNDING不是退款成功。只有核实微信成功后才能标记REFUNDED；店员可“重试退款/查退款”。
- 已核实的REFUNDCLOSE/CLOSED进入REFUND_FAILED，保留原失败账单，店员确认后可使用新退款号重新发起；previousRefundNo校验避免重复操作创建多笔退款。CHANGE/ABNORMAL进入REFUND_REVIEW，不允许自动或新号退款，需在微信商户后台核查后再次查单。异常订单优先展示在工作台。
- 全券抵扣至0元的订单立即本地结算，账本类型ZERO_PAYMENT，没有微信交易ID。撤销这类订单无外部转账。
- 已付订单退款不恢复商品库存、不退回已用券，以免已出餐商品自动重售。店员按实物情况补库存。
- 长期待核实订单需在微信商户后台核对交易/退款账单后处理。不要直接改状态、删除账本或解锁券来掩盖差异。

## 6. SaaS 生产准入与上线门槛（当前有效）

在下列项目完成前，小程序只能使用 demo，或由 SaaS 明确返回“支付渠道尚未接入”；不得把本地订单标记为已支付：

1. SaaS PostgreSQL 迁移到当前 schema，租户、商业能力、微信渠道账号和 storefront 绑定已配置并通过跨租户检查。
2. 微信登录真实联调成功，游客 session、目录、购物车、服务端地址快照、订单列表和退款申请在正式 AppID 下可用。
3. SaaS 接入真实微信支付下单、支付回调验签/解密、主动查单、超时和进程重启恢复；支付金额只能来自服务端订单快照。小程序创建新 JSAPI 支付时会重新调用 `wx.login`，把一次性 `login_code` 传给 `/orders/{orderNo}/payments`；不得复用 App 启动登录 code。
4. SaaS 接入真实退款申请、退款回调和幂等重试；退款确认前订单/库存/履约状态不得提前结转。
5. 商家履约 worker、通知、日志/告警和备份恢复完成现场演练。
6. 开发者工具与真机检查登录、商品、规格、地址、订单、错误提示和支付/退款状态；确认发布包没有注册页面的 `wx.cloud` 调用。

以下旧 CloudBase 验收项目仅作为历史迁移参考，不可据此宣称 SaaS 已上线。

1. 两个微信账号验证地址权限、助力双方到账、不能自助力/重复领券。
2. 实际支持的低额商品完成一次真实支付、制作、配送、收货和全额退款。
3. 真实回调落库、重复投递、客户端支付取消/超时后重新进入订单核实。
4. 接近支付有效期同时付款/取消；库存与优惠券无重复释放。
5. 退款请求网络失败后同号重试；定时器实际能更新结果。
6. 无权限用户不能进入数据管理接口、读取内部订单字段或上传/覆盖商品图片。
7. 微信模拟器及真机检查全部页面、图片、规格、键盘遮挡、电话拨打、分享路径。

当前仅完成本地源码/协议模拟/密码学/事务并发回归，未执行以上真实环境门槛。工作台金额为最近100单内按创建日期统计的净额展示，不是财务结算报表。

另已通过本机官方WXML/WXSS离线编译器检查（19个注册页面、23个样式文件），不替代上述模拟器/真机与资金联调门槛。
