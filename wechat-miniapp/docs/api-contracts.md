# SaaS Storefront 客户端接口

生产小程序使用 `https://<saas-host>/api/v1/storefront/wechat`。`POST /session` 建立 SaaS session；目录、购物车、订单、地址和支付均通过该前缀访问。

支付接口为 `POST /orders/{orderNo}/payments` 与 `GET /orders/{orderNo}/payment`。创建新 JSAPI 支付必须传 `order_no`、稳定的 `client_request_id` 和本次支付前新调用 `wx.login` 得到的 `login_code`；微信支付完成、取消或失败后都必须再次查单。返回的 `timeStamp/time_stamp`、`nonceStr/nonce_str`、`signType/sign_type`、`paySign/pay_sign` 均由小程序适配为 `wx.requestPayment` 所需字段。退款申请为 `POST /orders/{orderNo}/refund-requests`，没有独立的客户端退款查单时通过 `GET /orders/{orderNo}` 刷新订单并派生退款状态。

`commerce_storefront_bindings` 当前对同一 `channel_account_id` 只允许一个 active binding，因此一个 AppID/session/cart 只对应一个 `restaurant` 或 `retail`。小程序会隐藏/拒绝未绑定频道；要同时上线外卖和冷吃，必须先由 SaaS 后端提供多 binding 或分 channel account 的会话模型，否则按单业务发布。

当前主 SaaS storefront 契约没有配送区域列表、跑腿费/配送费计价字段，也没有优惠券、助力和商家管理公开接口。生产小程序因此不调用这些能力、不从本地缓存补写业务事实；餐饮 checkout 暂只开放 `pickup`，校园 `delivery` 需待服务端把配送计价纳入订单核价后再开放。演示模式的配送费、券和商家数据仅用于本地演示。

## 创建订单

`payload = { clientRequestId, orderGroupId?, fulfillmentType, deliveryMethod, addressId?, couponId?, customerRemark?, items: [{ productId, quantity, selectedOptions?, remark? }] }`

同一个clientRequestId和相同完整payload只生成一笔订单；变更payload使用同一ID报IDEMPOTENCY_CONFLICT。客户端发送前持久化payload，创建响应不明时只重试原请求；成功后转至原订单，不能换券新建来“重试付款”。明确验证拒绝后可以修改内容重新提交。

返回 `{ success, orderId, order, duplicate? }`。order始终脱敏。`paymentClosing` 是可公开的取消核实标记，不暴露内部交易记录。

混合购物车由客户端为每个履约类型提交一个独立 `clientRequestId`，复用同一个 `orderGroupId`；外卖订单使用 `CAMPUS` 地址或 `PICKUP`，冷吃订单必须使用 `SHIPPING` 地址。服务端不接受跨类型商品、地址或优惠券。

## 商品与库存

创建字段：name、description、categoryId、basePrice、originalPrice、stock、media、optionGroups。新商品默认下架，图片引用必须来自 SaaS 媒体接口返回的地址或媒体 ID。

规格组最多6组，每组最多12个选项：`{ id, name, required, options: [{ id, name, priceDelta }] }`，价格仍由服务端重新核算。

库存常规调整用 `stockDelta`，事务读取最新余额；绝对设置stock必须带expectedStock防止覆盖并发预扣。人工售罄与库存不足分开记录，退款/取消补库存不会误解除人工售罄。

## 助力与活动

saveCampaign保存相同活动ID不重置参与额度；新活动使用新ID。券模板版本化，session固定所承诺的模板，已发券不随活动编辑变化；已创建但尚未完成的session也继续读取旧模板。编辑被启用活动引用的模板会自动重绑定新版本，直接停用该模板会返回 `COUPON_TEMPLATE_IN_USE`。每次成功助力在一个事务中写双方券、记录和任务终态。配置仅支持1人助力。

## 支付和退款

`wx.requestPayment` 成败都不是订单终态；客户端在前后查询支付结果，重新进入详情也会核实。支付参数和订单状态均由 SaaS 返回。未知支付/关单结果保留待核实，不能清库存或券。

## 商家新订单通知

订单创建为 `WAIT_PAY` 时不发送接单通知；全券0元订单创建后已是 `PAID`，会尝试发送；普通订单在权威支付确认进入 `PAID` 后发送。通知只发给具有 `ORDER_MANAGE` 权限的店员，发送失败不回滚订单，工作台仍是最终处理入口。模板字段和前端订阅授权见 [部署说明](deployment.md)。
