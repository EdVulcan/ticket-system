package top.edvulcan.ticket.verify.domain

import top.edvulcan.ticket.verify.data.VerificationPreview

object VerificationPolicy {
    fun canConfirm(preview: VerificationPreview, quantity: Int, continuationConfirmed: Boolean): Boolean {
        if (quantity !in 1..preview.maxQuantity) return false
        if (!preview.batchAllowed && quantity != 1) return false
        return !preview.requiresRepeatConfirmation || continuationConfirmed
    }
}

object VerificationMessages {
    fun rejection(reasonCode: String, rawMessage: String): String {
        val normalizedReason = reasonCode.trim().lowercase()
        val normalizedMessage = rawMessage.trim().lowercase()
        return when {
            normalizedReason == "invalid_ticket" || "invalid ticket" in normalizedMessage -> "无效票"
            normalizedReason == "refunded" || "refunded" in normalizedMessage -> "订单已退款，不能核销"
            normalizedReason == "expired" || "expired" in normalizedMessage -> "门票已过期"
            normalizedReason == "not_started" || "not valid yet" in normalizedMessage -> "门票尚未生效"
            normalizedReason == "order_not_paid" || "not paid" in normalizedMessage -> "订单尚未支付"
            normalizedReason == "wrong_checkpoint" || "access denied" in normalizedMessage -> "当前检票点不能核销此票"
            normalizedReason == "already_used" || "limit reached" in normalizedMessage -> "当前检票点可用次数已满"
            normalizedReason == "benefit_exhausted" -> "该票可用权益已用完"
            rawMessage.any { it.code > 127 } -> rawMessage.trim()
            else -> "无法识别此票，请核对二维码"
        }
    }
}
