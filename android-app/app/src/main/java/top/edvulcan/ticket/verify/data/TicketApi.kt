package top.edvulcan.ticket.verify.data

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject
import top.edvulcan.ticket.verify.BuildConfig
import java.io.IOException
import java.util.concurrent.TimeUnit

data class Checkpoint(val id: Long, val name: String, val location: String)
data class MobileDevice(val id: Long, val name: String, val serialNumber: String, val checkpointId: Long)
data class MobileTargets(val checkpoints: List<Checkpoint>, val devices: List<MobileDevice>)
data class LoginResult(val token: String, val staffName: String)
data class TenantInfo(val name: String)
data class MobileSession(val token: String, val expiresAt: String)
data class RecentOperation(val operationId: String, val quantity: Int, val completedAt: String)
data class VerificationPreview(
    val previewId: String,
    val productName: String,
    val codeMode: String,
    val batchAllowed: Boolean,
    val maxQuantity: Int,
    val pointUsed: Int,
    val pointRemaining: Int,
    val requiresRepeatConfirmation: Boolean,
    val recentOperation: RecentOperation?,
    val ticketCode: String,
)
data class PendingOperation(
    val operationId: String,
    val previewId: String,
    val ticketCode: String,
    val quantity: Int,
    val continuationOf: String?,
)
data class VerificationResult(
    val operationId: String,
    val status: String,
    val result: String,
    val reasonCode: String,
    val displayText: String,
    val quantity: Int,
    val pointRemaining: Int,
    val completedAt: String,
) {
    val allowed: Boolean get() = result == "allow"
    val processing: Boolean get() = status == "processing" || reasonCode == "processing"
}

class ApiException(
    val statusCode: Int,
    val reasonCode: String,
    val requiresConfirmation: Boolean,
    message: String,
) : IOException(message)

class TicketApi(
    private val baseUrl: String = BuildConfig.API_BASE_URL,
    private val client: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(12, TimeUnit.SECONDS)
        .readTimeout(18, TimeUnit.SECONDS)
        .writeTimeout(18, TimeUnit.SECONDS)
        .build(),
) {
    @Volatile private var authToken = ""
    @Volatile private var mobileSessionToken = ""

    fun setCredentials(token: String, sessionToken: String = mobileSessionToken) {
        authToken = token.trim()
        mobileSessionToken = sessionToken.trim()
    }

    suspend fun login(systemCode: String, jobNumber: String, password: String): LoginResult = post(
        "auth/staff/login",
        JSONObject().put("system_code", systemCode.trim()).put("job_number", jobNumber.trim()).put("password", password),
        authenticated = false,
    ).let { LoginResult(it.requireString("token"), it.optJSONObject("staff")?.optString("name").orEmpty()) }

    suspend fun tenant(): TenantInfo = get("tenants/me").let { TenantInfo(it.optString("name")) }

    suspend fun targets(): MobileTargets = get("mobile/targets").let { root ->
        val checkpoints = root.optJSONArray("checkpoints")
        val devices = root.optJSONArray("devices")
        MobileTargets(
            checkpoints = buildList {
                if (checkpoints != null) for (index in 0 until checkpoints.length()) checkpoints.getJSONObject(index).let {
                    add(Checkpoint(it.getLong("id"), it.optString("name"), it.optString("location")))
                }
            },
            devices = buildList {
                if (devices != null) for (index in 0 until devices.length()) devices.getJSONObject(index).let {
                    add(MobileDevice(it.getLong("id"), it.optString("name"), it.optString("serial_number"), it.optLong("check_point_id")))
                }
            },
        )
    }

    suspend fun createSession(checkpointId: Long, deviceId: Long): MobileSession = post(
        "mobile/sessions", JSONObject().put("check_point_id", checkpointId).put("device_id", deviceId)
    ).let { MobileSession(it.requireString("session_token"), it.optString("expires_at")) }

    suspend fun heartbeat() { post("mobile/session/heartbeat", JSONObject()) }
    suspend fun closeSession() { post("mobile/session/close", JSONObject()) }

    suspend fun preview(ticketCode: String): VerificationPreview = post(
        "mobile/session/verification-previews", JSONObject().put("ticket_code", ticketCode.trim())
    ).let { root ->
        val recent = root.optJSONObject("recent_operation")?.let {
            RecentOperation(it.optString("operation_id"), it.optInt("quantity", 1), it.optString("completed_at"))
        }
        VerificationPreview(
            previewId = root.requireString("preview_id"),
            productName = root.optString("product_name", "当前票券"),
            codeMode = root.optString("code_mode"),
            batchAllowed = root.optBoolean("batch_allowed"),
            maxQuantity = root.optInt("max_quantity", 1).coerceAtLeast(1),
            pointUsed = root.optInt("point_used"),
            pointRemaining = root.optInt("point_remaining"),
            requiresRepeatConfirmation = root.optBoolean("requires_repeat_confirmation"),
            recentOperation = recent,
            ticketCode = ticketCode.trim(),
        )
    }

    suspend fun confirm(operation: PendingOperation): VerificationResult {
        val body = JSONObject()
            .put("preview_id", operation.previewId)
            .put("operation_id", operation.operationId)
            .put("quantity", operation.quantity)
        operation.continuationOf?.let { body.put("continuation_of", it) }
        return post("mobile/session/verification-operations", body).toVerificationResult(operation)
    }

    suspend fun operation(operation: PendingOperation): VerificationResult =
        get("mobile/verification-operations/${operation.operationId}").toVerificationResult(operation)

    private suspend fun get(path: String): JSONObject = execute(Request.Builder().url(url(path)).get(), authenticated = true)

    private suspend fun post(path: String, body: JSONObject, authenticated: Boolean = true): JSONObject = execute(
        Request.Builder().url(url(path)).post(body.toString().toRequestBody(JSON)), authenticated
    )

    private suspend fun execute(builder: Request.Builder, authenticated: Boolean): JSONObject = withContext(Dispatchers.IO) {
        if (authenticated) {
            if (authToken.isBlank()) throw ApiException(401, "unauthorized", false, "登录已失效")
            builder.header("Authorization", "Bearer $authToken")
            if (mobileSessionToken.isNotBlank()) builder.header("X-Mobile-Session", mobileSessionToken)
        }
        client.newCall(builder.build()).execute().use { response ->
            val text = response.body?.string().orEmpty()
            val json = runCatching { if (text.isBlank()) JSONObject() else JSONObject(text) }.getOrElse { JSONObject() }
            if (!response.isSuccessful) {
                throw ApiException(
                    response.code,
                    json.optString("reason_code"),
                    json.optBoolean("requires_confirmation"),
                    json.optString("error", "请求失败（${response.code}）"),
                )
            }
            json
        }
    }

    private fun url(path: String) = baseUrl.trimEnd('/') + "/" + path.trimStart('/')

    private fun JSONObject.toVerificationResult(fallback: PendingOperation) = VerificationResult(
        operationId = optString("operation_id", fallback.operationId),
        status = optString("status"),
        result = optString("result"),
        reasonCode = optString("reason_code"),
        displayText = optString("display_text", if (optString("result") == "allow") "核销成功" else "核销未通过"),
        quantity = optInt("quantity", fallback.quantity),
        pointRemaining = optInt("point_remaining", -1),
        completedAt = optString("completed_at"),
    )

    private fun JSONObject.requireString(key: String): String = optString(key).takeIf { it.isNotBlank() }
        ?: throw IOException("响应缺少 $key")

    companion object {
        private val JSON = "application/json; charset=utf-8".toMediaType()
    }
}
