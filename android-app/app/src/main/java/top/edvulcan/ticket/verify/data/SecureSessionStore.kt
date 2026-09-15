package top.edvulcan.ticket.verify.data

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import org.json.JSONObject
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

data class StoredSession(
    val authToken: String = "",
    val mobileSessionToken: String = "",
    val tenantName: String = "",
    val checkpointId: Long = 0,
    val deviceId: Long = 0,
    val expiresAt: String = "",
    val pendingOperation: PendingOperation? = null,
)

class SecureSessionStore(context: Context) {
    private val preferences = context.getSharedPreferences("secure_mobile_verify", Context.MODE_PRIVATE)

    fun load(): StoredSession {
        val encrypted = preferences.getString(DATA_KEY, null) ?: return StoredSession()
        return runCatching { decode(JSONObject(decrypt(encrypted))) }.getOrElse {
            clear()
            StoredSession()
        }
    }

    fun save(session: StoredSession) {
        val pending = session.pendingOperation?.let {
            JSONObject().put("operation_id", it.operationId).put("preview_id", it.previewId)
                .put("ticket_code", it.ticketCode).put("quantity", it.quantity)
                .put("continuation_of", it.continuationOf)
        }
        val json = JSONObject()
            .put("auth_token", session.authToken)
            .put("mobile_session_token", session.mobileSessionToken)
            .put("tenant_name", session.tenantName)
            .put("checkpoint_id", session.checkpointId)
            .put("device_id", session.deviceId)
            .put("expires_at", session.expiresAt)
            .put("pending_operation", pending)
        preferences.edit().putString(DATA_KEY, encrypt(json.toString())).apply()
    }

    fun clear() = preferences.edit().remove(DATA_KEY).apply()

    private fun decode(json: JSONObject): StoredSession {
        val pending = json.optJSONObject("pending_operation")?.let {
            PendingOperation(
                operationId = it.optString("operation_id"), previewId = it.optString("preview_id"),
                ticketCode = it.optString("ticket_code"), quantity = it.optInt("quantity", 1),
                continuationOf = it.optString("continuation_of").takeIf(String::isNotBlank),
            )
        }?.takeIf { it.operationId.isNotBlank() && it.previewId.isNotBlank() && it.ticketCode.isNotBlank() }
        return StoredSession(
            authToken = json.optString("auth_token"), mobileSessionToken = json.optString("mobile_session_token"),
            tenantName = json.optString("tenant_name"), checkpointId = json.optLong("checkpoint_id"),
            deviceId = json.optLong("device_id"), expiresAt = json.optString("expires_at"), pendingOperation = pending,
        )
    }

    private fun encrypt(plainText: String): String {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        return Base64.encodeToString(cipher.iv + cipher.doFinal(plainText.toByteArray(Charsets.UTF_8)), Base64.NO_WRAP)
    }

    private fun decrypt(encoded: String): String {
        val payload = Base64.decode(encoded, Base64.NO_WRAP)
        require(payload.size > IV_SIZE)
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, payload.copyOfRange(0, IV_SIZE)))
        return cipher.doFinal(payload.copyOfRange(IV_SIZE, payload.size)).toString(Charsets.UTF_8)
    }

    private fun key(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").run {
            init(KeyGenParameterSpec.Builder(KEY_ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())
            generateKey()
        }
    }

    companion object {
        private const val DATA_KEY = "session"
        private const val KEY_ALIAS = "ticket_verify_session_v1"
        private const val TRANSFORMATION = "AES/GCM/NoPadding"
        private const val IV_SIZE = 12
    }
}
