package top.edvulcan.ticket.verify

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import java.util.UUID
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import top.edvulcan.ticket.verify.data.ApiException
import top.edvulcan.ticket.verify.data.Checkpoint
import top.edvulcan.ticket.verify.data.MobileDevice
import top.edvulcan.ticket.verify.data.PendingOperation
import top.edvulcan.ticket.verify.data.SecureSessionStore
import top.edvulcan.ticket.verify.data.StoredSession
import top.edvulcan.ticket.verify.data.TicketApi
import top.edvulcan.ticket.verify.data.VerificationPreview
import top.edvulcan.ticket.verify.data.VerificationResult
import top.edvulcan.ticket.verify.domain.VerificationPolicy
import top.edvulcan.ticket.verify.domain.VerificationMessages

enum class VerifyScreen { LOGIN, TARGETS, VERIFY }
enum class ConnectionState { OFFLINE, CHECKING, ONLINE, DEGRADED }

data class RecentVerification(
    val title: String,
    val allowed: Boolean,
    val quantity: Int,
    val checkpointName: String,
)

data class MobileVerifyUiState(
    val screen: VerifyScreen = VerifyScreen.LOGIN,
    val busy: Boolean = false,
    val tenantName: String = "",
    val staffName: String = "",
    val checkpoints: List<Checkpoint> = emptyList(),
    val devices: List<MobileDevice> = emptyList(),
    val selectedCheckpointId: Long = 0,
    val selectedDeviceId: Long = 0,
    val connection: ConnectionState = ConnectionState.OFFLINE,
    val preview: VerificationPreview? = null,
    val quantity: Int = 1,
    val continuationConfirmed: Boolean = false,
    val result: VerificationResult? = null,
    val uncertain: Boolean = false,
    val error: String = "",
    val recent: List<RecentVerification> = emptyList(),
)

class MobileVerifyViewModel(application: Application) : AndroidViewModel(application) {
    private val api = TicketApi()
    private val store = SecureSessionStore(application)
    private val _state = MutableStateFlow(MobileVerifyUiState())
    val state: StateFlow<MobileVerifyUiState> = _state.asStateFlow()
    private var stored = store.load()
    private var heartbeatJob: Job? = null

    init {
        if (stored.authToken.isNotBlank()) restore()
    }

    fun login(systemCode: String, jobNumber: String, password: String) {
        if (systemCode.isBlank() || jobNumber.isBlank() || password.isBlank() || _state.value.busy) return
        launchBusy {
            val login = api.login(systemCode, jobNumber, password)
            api.setCredentials(login.token, "")
            val tenant = api.tenant()
            stored = StoredSession(authToken = login.token, tenantName = tenant.name)
            store.save(stored)
            _state.value = MobileVerifyUiState(screen = VerifyScreen.TARGETS, tenantName = tenant.name, staffName = login.staffName, connection = ConnectionState.OFFLINE)
            loadTargetsInternal()
        }
    }

    fun selectCheckpoint(id: Long) {
        val devices = _state.value.devices.filter { it.checkpointId == id }
        _state.value = _state.value.copy(selectedCheckpointId = id, selectedDeviceId = devices.singleOrNull()?.id ?: 0, error = "")
    }

    fun selectDevice(id: Long) { _state.value = _state.value.copy(selectedDeviceId = id, error = "") }

    fun refreshTargets() = launchBusy { loadTargetsInternal() }

    fun startSession() {
        val current = _state.value
        if (current.selectedCheckpointId == 0L || current.selectedDeviceId == 0L || current.busy) return
        launchBusy {
            val session = api.createSession(current.selectedCheckpointId, current.selectedDeviceId)
            api.setCredentials(stored.authToken, session.token)
            stored = stored.copy(
                mobileSessionToken = session.token, checkpointId = current.selectedCheckpointId,
                deviceId = current.selectedDeviceId, expiresAt = session.expiresAt, pendingOperation = null,
            )
            store.save(stored)
            _state.value = _state.value.copy(screen = VerifyScreen.VERIFY, connection = ConnectionState.ONLINE, preview = null, result = null, uncertain = false, error = "")
            startHeartbeat()
        }
    }

    fun inspectCode(raw: String) {
        val ticketCode = normalizeTicketCode(raw)
        if (ticketCode.isBlank() || _state.value.busy || _state.value.uncertain || _state.value.preview != null) return
        _state.value = _state.value.copy(busy = true, result = null, error = "")
        viewModelScope.launch {
            try {
                val preview = api.preview(ticketCode)
                _state.value = _state.value.copy(preview = preview, quantity = 1, continuationConfirmed = false, error = "")
            } catch (error: Throwable) {
                if (error is ApiException && error.statusCode == 401) {
                    stored = stored.copy(mobileSessionToken = "", checkpointId = 0, deviceId = 0, expiresAt = "", pendingOperation = null)
                    store.save(stored)
                    api.setCredentials(stored.authToken, "")
                    _state.value = _state.value.copy(screen = VerifyScreen.TARGETS, connection = ConnectionState.OFFLINE, error = "核销会话已失效，请重新选择检票点")
                } else if (error is ApiException && error.statusCode in 400..499) {
                    val message = VerificationMessages.rejection(error.reasonCode, error.message.orEmpty())
                    _state.value = _state.value.copy(
                        result = VerificationResult("preview-${UUID.randomUUID()}", "denied", "deny", error.reasonCode.ifBlank { "invalid_ticket" }, message, 0, -1, ""),
                        error = "",
                    )
                } else {
                    _state.value = _state.value.copy(error = "网络连接失败，请检查网络后重新扫码")
                }
            } finally {
                _state.value = _state.value.copy(busy = false)
            }
        }
    }

    fun changeQuantity(delta: Int) {
        val current = _state.value
        val preview = current.preview ?: return
        _state.value = current.copy(quantity = (current.quantity + delta).coerceIn(1, preview.maxQuantity))
    }

    fun setContinuationConfirmed(confirmed: Boolean) {
        _state.value = _state.value.copy(continuationConfirmed = confirmed)
    }

    fun dismissPreview() {
        if (_state.value.busy) return
        _state.value = _state.value.copy(preview = null, quantity = 1, continuationConfirmed = false)
    }

    fun confirmPreview() {
        val current = _state.value
        val preview = current.preview ?: return
        if (current.busy || current.uncertain || !VerificationPolicy.canConfirm(preview, current.quantity, current.continuationConfirmed)) return
        val pending = PendingOperation(
            operationId = UUID.randomUUID().toString(), previewId = preview.previewId,
            ticketCode = preview.ticketCode, quantity = current.quantity,
            continuationOf = preview.recentOperation?.operationId.takeIf { preview.requiresRepeatConfirmation },
        )
        stored = stored.copy(pendingOperation = pending)
        store.save(stored)
        _state.value = current.copy(busy = true, error = "")
        viewModelScope.launch {
            try {
                finishOperation(api.confirm(pending), pending)
            } catch (error: Throwable) {
                handleOperationFailure(error, pending)
            } finally {
                _state.value = _state.value.copy(busy = false)
            }
        }
    }

    fun retryPending() {
        val pending = stored.pendingOperation ?: return
        if (_state.value.busy) return
        _state.value = _state.value.copy(busy = true, error = "")
        viewModelScope.launch {
            try {
                val current = api.operation(pending)
                finishOperation(if (current.processing) api.confirm(pending) else current, pending)
            } catch (error: Throwable) {
                handleOperationFailure(error, pending)
            } finally {
                _state.value = _state.value.copy(busy = false)
            }
        }
    }

    fun clearResult() { _state.value = _state.value.copy(result = null, error = "") }

    fun changePoint() {
        if (_state.value.busy || _state.value.uncertain) return
        heartbeatJob?.cancel()
        viewModelScope.launch { runCatching { api.closeSession() } }
        stored = stored.copy(mobileSessionToken = "", checkpointId = 0, deviceId = 0, expiresAt = "", pendingOperation = null)
        store.save(stored)
        api.setCredentials(stored.authToken, "")
        _state.value = _state.value.copy(screen = VerifyScreen.TARGETS, connection = ConnectionState.OFFLINE, preview = null, result = null, recent = emptyList())
    }

    fun logout() {
        if (_state.value.busy || _state.value.uncertain) return
        heartbeatJob?.cancel()
        viewModelScope.launch { runCatching { if (stored.mobileSessionToken.isNotBlank()) api.closeSession() } }
        stored = StoredSession()
        store.clear()
        api.setCredentials("", "")
        _state.value = MobileVerifyUiState()
    }

    private fun restore() {
        _state.value = _state.value.copy(busy = true, tenantName = stored.tenantName, connection = ConnectionState.CHECKING)
        api.setCredentials(stored.authToken, stored.mobileSessionToken)
        viewModelScope.launch {
            try {
                loadTargetsInternal()
                if (stored.mobileSessionToken.isNotBlank()) {
                    api.heartbeat()
                    _state.value = _state.value.copy(screen = VerifyScreen.VERIFY, connection = ConnectionState.ONLINE)
                    startHeartbeat()
                    stored.pendingOperation?.let { pending ->
                        val result = api.operation(pending)
                        finishOperation(if (result.processing) api.confirm(pending) else result, pending)
                    }
                } else {
                    _state.value = _state.value.copy(screen = VerifyScreen.TARGETS, connection = ConnectionState.OFFLINE)
                }
            } catch (error: Throwable) {
                if (error is ApiException && error.statusCode == 401) {
                    stored = StoredSession()
                    store.clear()
                    _state.value = MobileVerifyUiState(error = "登录已失效，请重新登录")
                } else if (stored.pendingOperation != null) {
                    _state.value = _state.value.copy(screen = VerifyScreen.VERIFY, uncertain = true, connection = ConnectionState.DEGRADED, error = "上一笔核销结果待确认，请恢复后再继续")
                } else {
                    _state.value = _state.value.copy(screen = if (stored.mobileSessionToken.isBlank()) VerifyScreen.TARGETS else VerifyScreen.VERIFY, connection = ConnectionState.DEGRADED, error = error.message ?: "连接失败")
                }
            } finally {
                _state.value = _state.value.copy(busy = false)
            }
        }
    }

    private suspend fun loadTargetsInternal() {
        val targets = api.targets()
        val checkpointId = stored.checkpointId.takeIf { saved -> targets.checkpoints.any { it.id == saved } }
            ?: targets.checkpoints.singleOrNull()?.id ?: 0
        val eligible = targets.devices.filter { it.checkpointId == checkpointId }
        val deviceId = stored.deviceId.takeIf { saved -> eligible.any { it.id == saved } } ?: eligible.singleOrNull()?.id ?: 0
        _state.value = _state.value.copy(checkpoints = targets.checkpoints, devices = targets.devices, selectedCheckpointId = checkpointId, selectedDeviceId = deviceId)
    }

    private fun finishOperation(result: VerificationResult, pending: PendingOperation) {
        if (result.processing) {
            _state.value = _state.value.copy(uncertain = true, connection = ConnectionState.DEGRADED, error = "核销结果待确认，请重试原操作")
            return
        }
        stored = stored.copy(pendingOperation = null)
        store.save(stored)
        val checkpoint = _state.value.checkpoints.firstOrNull { it.id == stored.checkpointId }?.name.orEmpty()
        val recent = RecentVerification(result.displayText, result.allowed, result.quantity.takeIf { it > 0 } ?: pending.quantity, checkpoint)
        _state.value = _state.value.copy(
            preview = null, quantity = 1, continuationConfirmed = false, result = result,
            uncertain = false, connection = ConnectionState.ONLINE, error = "",
            recent = listOf(recent) + _state.value.recent.take(4),
        )
    }

    private fun handleOperationFailure(error: Throwable, pending: PendingOperation) {
        if (error is ApiException && error.statusCode == 401) {
            stored = StoredSession()
            store.clear()
            _state.value = MobileVerifyUiState(error = "会话已过期，请重新登录")
            return
        }
        if (error is ApiException && error.requiresConfirmation) {
            stored = stored.copy(pendingOperation = null)
            store.save(stored)
            _state.value = _state.value.copy(preview = null, uncertain = false, error = "该票码已有新的核销记录，请重新读取并核对剩余次数")
            return
        }
        if (error is ApiException && error.statusCode in 400..499 && error.statusCode != 409) {
            stored = stored.copy(pendingOperation = null)
            store.save(stored)
            _state.value = _state.value.copy(preview = null, uncertain = false, result = VerificationResult(pending.operationId, "denied", "deny", error.reasonCode, error.message ?: "核销未通过", 0, -1, ""), error = "")
            return
        }
        _state.value = _state.value.copy(uncertain = true, connection = ConnectionState.DEGRADED, error = "网络未返回最终结果，只能恢复本次核销")
    }

    private fun startHeartbeat() {
        heartbeatJob?.cancel()
        heartbeatJob = viewModelScope.launch {
            while (isActive) {
                delay(60_000)
                runCatching { api.heartbeat() }.onSuccess {
                    _state.value = _state.value.copy(connection = ConnectionState.ONLINE)
                }.onFailure {
                    _state.value = _state.value.copy(connection = ConnectionState.DEGRADED)
                }
            }
        }
    }

    private fun launchBusy(block: suspend () -> Unit) {
        if (_state.value.busy) return
        _state.value = _state.value.copy(busy = true, error = "")
        viewModelScope.launch {
            try { block() }
            catch (error: Throwable) { _state.value = _state.value.copy(error = error.message ?: "操作失败") }
            finally { _state.value = _state.value.copy(busy = false) }
        }
    }

    private fun normalizeTicketCode(raw: String): String {
        val text = raw.trim()
        return runCatching {
            val uri = android.net.Uri.parse(text)
            if (uri.scheme == "http" || uri.scheme == "https") uri.getQueryParameter("ticket_code") ?: uri.getQueryParameter("code") ?: text else text
        }.getOrDefault(text)
    }
}
