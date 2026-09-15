package top.edvulcan.ticket.verify.domain

import top.edvulcan.ticket.verify.data.VerificationPreview

object VerificationPolicy {
    fun canConfirm(preview: VerificationPreview, quantity: Int, continuationConfirmed: Boolean): Boolean {
        if (quantity !in 1..preview.maxQuantity) return false
        if (!preview.batchAllowed && quantity != 1) return false
        return !preview.requiresRepeatConfirmation || continuationConfirmed
    }
}
