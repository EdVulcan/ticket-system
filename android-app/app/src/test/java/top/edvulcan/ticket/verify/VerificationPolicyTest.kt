package top.edvulcan.ticket.verify

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import top.edvulcan.ticket.verify.data.VerificationPreview
import top.edvulcan.ticket.verify.domain.VerificationPolicy
import top.edvulcan.ticket.verify.domain.VerificationMessages

class VerificationPolicyTest {
    @Test
    fun sharedCodeDefaultsToOneAndRejectsAmountsOutsideServerLimit() {
        val preview = preview(batchAllowed = true, maxQuantity = 3)
        assertTrue(VerificationPolicy.canConfirm(preview, 1, false))
        assertTrue(VerificationPolicy.canConfirm(preview, 3, false))
        assertFalse(VerificationPolicy.canConfirm(preview, 0, false))
        assertFalse(VerificationPolicy.canConfirm(preview, 4, false))
    }

    @Test
    fun oneTicketCodeCannotConsumeMoreThanOne() {
        val preview = preview(batchAllowed = false, maxQuantity = 3)
        assertTrue(VerificationPolicy.canConfirm(preview, 1, false))
        assertFalse(VerificationPolicy.canConfirm(preview, 2, false))
    }

    @Test
    fun recentRepeatRequiresExplicitConfirmation() {
        val preview = preview(batchAllowed = true, maxQuantity = 2, repeat = true)
        assertFalse(VerificationPolicy.canConfirm(preview, 1, false))
        assertTrue(VerificationPolicy.canConfirm(preview, 1, true))
    }

    @Test
    fun internalEnglishErrorsAreNeverShownToFieldStaff() {
        assertTrue(VerificationMessages.rejection("invalid_ticket", "invalid ticket") == "无效票")
        assertTrue(VerificationMessages.rejection("", "ticket has been refunded") == "订单已退款，不能核销")
        assertTrue(VerificationMessages.rejection("", "unexpected internal failure") == "无法识别此票，请核对二维码")
    }

    private fun preview(batchAllowed: Boolean, maxQuantity: Int, repeat: Boolean = false) = VerificationPreview(
        previewId = "preview", productName = "测试票", codeMode = if (batchAllowed) "order" else "ticket",
        batchAllowed = batchAllowed, maxQuantity = maxQuantity, pointUsed = 0, pointRemaining = maxQuantity,
        requiresRepeatConfirmation = repeat, recentOperation = null, ticketCode = "CODE",
    )
}
