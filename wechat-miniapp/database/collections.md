# 数据库集合

## 核心集合

| 集合 | 用途 | 客户端写入 |
|---|---|---|
| `users` | OpenID、昵称、角色 | 禁止，走 `bootstrap` |
| `staff` | 店主/员工权限 | 禁止，控制台维护 |
| `store_settings` | 营业状态、公告、营业时间、跑腿费、快递费、包邮门槛与冷吃起购规则 | 禁止，走 `merchant` |
| `categories` | 商品分类 | 禁止，走 `merchant` |
| `products` | 商品价格、规格、库存、上下架与履约类型 | 禁止，走 `merchant` |
| `delivery_zones` | 校区、配送区域、跑腿费 | 禁止，走 `merchant` |
| `addresses` | 用户校园/快递收货地址 | 仅本人读取，写入必须走 `address` 云函数 |
| `address_books` | 每用户地址 ID 清单和唯一默认地址指针，串行化地址事务 | 禁止读写，走 `address` |
| `orders` | 订单及商品/地址/优惠券快照；外卖与快递分开履约 | 禁止，走 `order` |
| `payment_records` | 微信支付和退款幂等记录 | 禁止 |
| `coupon_templates` | 优惠券模板及不可变版本 | 禁止，店主维护 |
| `user_coupons` | 用户优惠券和锁定状态 | 禁止，走 `order`/`assist` |
| `assist_campaigns` | 助力活动配置 | 禁止，走 `merchant` |
| `assist_sessions` | 助力分享任务 | 禁止，走 `assist` |
| `assist_records` | 助力成功记录 | 禁止，走 `assist` |
| `operation_logs` | 改价、库存、退款和订单操作记录 | 禁止 |

## 关键约束

- 金额统一使用整数“分”。
- 订单保存 `itemsSnapshot`、`addressSnapshot`、`couponSnapshot`，历史订单不跟随商品改名/改价变化。
- 商品使用 `fulfillmentType` 区分 `TAKEAWAY`（校园跑腿）和 `COURIER`（成都快递冷吃）；混合购物车按履约类型创建关联子订单。
- 地址使用 `addressType` 区分 `CAMPUS`（绑定启用的 `zoneId`）和 `SHIPPING`（省/市/区/详细地址）；订单只保存地址快照。
- 快递订单保存 `shippingFee`、`shipping.carrier`、`shipping.trackingNo` 和 `shippedAt`；外卖订单只保存 `deliveryFee` 与跑腿快照。
- 客户端只传商品 ID、数量、选项、地址 ID、优惠券 ID 和幂等请求 ID；服务端重新读取价格、库存、规格、配送区域和优惠券状态。
- 创建订单时库存先锁定、优惠券进入 `LOCKED`；支付成功后进入 `USED`，取消或超时后释放库存和优惠券。
- 快递订单支付成功后为 `PAID`（顾客端展示为待发货），商家只能通过带快递公司和单号的 `SHIPPED` 状态发货；用户确认收货后进入 `COMPLETED`。
- 助力发券使用确定性用户券 ID和数据库事务，避免重复点击重复发券。
- 优惠券模板编辑生成新版本；已启用活动会切换到新模板，已有用户券和已创建助力任务保留旧模板；当前启用活动引用的模板不能直接换成停用版本。
- `store_settings`、`products`、`categories`、`delivery_zones` 可以公开只读；订单禁止客户端直接读写，本人经 `order` 云函数读取脱敏内容；优惠券只能本人查询。
