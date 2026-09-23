# 实际查询索引清单

按云开发控制台的建议创建复合索引，字段名中的payment.outTradeNo表示嵌套字段。唯一性由确定性文档ID与事务实现，不能用非唯一clientRequestId索引替代。

```text
staff: userId + storeId + enabled
products: storeId + sort ASC
products: storeId + isOnSale + isSoldOut + sort ASC
products: storeId + fulfillmentType + isOnSale + isSoldOut + sort ASC
categories: storeId + enabled + sort ASC
delivery_zones: storeId + enabled + sort ASC
delivery_zones: storeId + zoneName + enabled
addresses: userId
orders: userId + createdAt DESC
orders: storeId + createdAt DESC
orders: storeId + payment.outTradeNo
orders: storeId + payment.outRefundNo
orders: storeId + status + expireAt ASC + updatedAt ASC
orders: storeId + status + updatedAt ASC
orders: storeId + status + deliveringAt ASC
orders: storeId + fulfillmentType + status + updatedAt ASC
user_coupons: userId + storeId + expireAt ASC
assist_campaigns: storeId + enabled + startAt DESC
assist_campaigns: storeId + startAt DESC
assist_sessions: shareToken
operation_logs: storeId + createdAt DESC
coupon_templates: storeId + enabled + createdAt DESC
payment_records: orderId
payment_records: transactionId
```

过期待付查询同时使用expireAt范围与updatedAt排序，具体索引顺序/范围能力须在目标CloudBase环境执行查询确认；如果控制台提示其他组合，以对应查询实际要求建立并验证，不要删除排序后造成失败订单饥饿。用户地址簿、确定性订单、助力记录与券等doc(id)查询使用内建_id索引。
