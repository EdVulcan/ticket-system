package top.edvulcan.ticket.verify.ui

import android.media.AudioManager
import android.media.ToneGenerator
import android.os.Build
import android.os.VibrationEffect
import android.os.Vibrator
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.KeyboardArrowRight
import androidx.compose.material.icons.automirrored.rounded.Logout
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.Bolt
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.Edit
import androidx.compose.material.icons.rounded.FlashOff
import androidx.compose.material.icons.rounded.FlashOn
import androidx.compose.material.icons.rounded.LocationOn
import androidx.compose.material.icons.rounded.Remove
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CheckboxDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.delay
import top.edvulcan.ticket.verify.ConnectionState
import top.edvulcan.ticket.verify.MobileVerifyViewModel
import top.edvulcan.ticket.verify.VerifyScreen
import top.edvulcan.ticket.verify.data.Checkpoint
import top.edvulcan.ticket.verify.data.MobileDevice
import top.edvulcan.ticket.verify.data.VerificationPreview
import top.edvulcan.ticket.verify.data.VerificationResult
import top.edvulcan.ticket.verify.scanner.CameraScanner
import top.edvulcan.ticket.verify.ui.theme.VerifyBorder
import top.edvulcan.ticket.verify.ui.theme.VerifyDanger
import top.edvulcan.ticket.verify.ui.theme.VerifyInk
import top.edvulcan.ticket.verify.ui.theme.VerifyMint
import top.edvulcan.ticket.verify.ui.theme.VerifyMuted
import top.edvulcan.ticket.verify.ui.theme.VerifySuccess
import top.edvulcan.ticket.verify.ui.theme.VerifyTeal
import top.edvulcan.ticket.verify.ui.theme.VerifyWarning

@Composable
fun MobileVerifyApp(viewModel: MobileVerifyViewModel, cameraGranted: Boolean, requestCamera: () -> Unit) {
    val state by viewModel.state.collectAsState()
    val context = LocalContext.current
    LaunchedEffect(state.result?.operationId) {
        state.result?.let { result ->
            val tone = ToneGenerator(AudioManager.STREAM_NOTIFICATION, 85)
            tone.startTone(if (result.allowed) ToneGenerator.TONE_PROP_ACK else ToneGenerator.TONE_PROP_NACK, 220)
            val vibrator = context.getSystemService(Vibrator::class.java)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                vibrator?.vibrate(VibrationEffect.createOneShot(if (result.allowed) 70 else 160, VibrationEffect.DEFAULT_AMPLITUDE))
            } else {
                @Suppress("DEPRECATION") vibrator?.vibrate(if (result.allowed) 70 else 160)
            }
            delay(260)
            tone.release()
        }
    }
    when (state.screen) {
        VerifyScreen.LOGIN -> LoginScreen(state.busy, state.error, viewModel::login)
        VerifyScreen.TARGETS -> TargetScreen(viewModel)
        VerifyScreen.VERIFY -> VerifyWorkspace(viewModel, cameraGranted, requestCamera)
    }
}

@Composable
private fun LoginScreen(busy: Boolean, error: String, login: (String, String, String) -> Unit) {
    var systemCode by remember { mutableStateOf("") }
    var jobNumber by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    Surface(color = MaterialTheme.colorScheme.background, modifier = Modifier.fillMaxSize()) {
        Column(
            modifier = Modifier.fillMaxSize().safeDrawingPadding().verticalScroll(rememberScrollState()).padding(horizontal = 24.dp, vertical = 30.dp),
            verticalArrangement = Arrangement.SpaceBetween,
        ) {
            Column {
                BrandMark()
                Spacer(Modifier.height(44.dp))
                Text("移动核销", style = MaterialTheme.typography.headlineMedium)
                Text("为现场验票而设计", style = MaterialTheme.typography.bodyLarge, color = VerifyMuted, modifier = Modifier.padding(top = 6.dp))
            }
            Column(modifier = Modifier.padding(vertical = 30.dp)) {
                VerifyTextField(systemCode, { systemCode = it }, "系统编号")
                VerifyTextField(jobNumber, { jobNumber = it }, "员工工号", Modifier.padding(top = 12.dp))
                OutlinedTextField(
                    value = password, onValueChange = { password = it }, label = { Text("密码") }, singleLine = true,
                    visualTransformation = PasswordVisualTransformation(), shape = RoundedCornerShape(14.dp), modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
                )
                ErrorBanner(error)
                PrimaryButton(
                    label = "登录并开始", busy = busy,
                    enabled = systemCode.isNotBlank() && jobNumber.isNotBlank() && password.isNotBlank(),
                    onClick = { login(systemCode, jobNumber, password) },
                    modifier = Modifier.padding(top = 18.dp),
                )
            }
            Text("账号权限和检票范围由管理后台统一控制", style = MaterialTheme.typography.bodySmall, color = VerifyMuted)
        }
    }
}

@Composable
private fun TargetScreen(viewModel: MobileVerifyViewModel) {
    val state by viewModel.state.collectAsState()
    val eligibleDevices = state.devices.filter { it.checkpointId == state.selectedCheckpointId }
    Surface(color = MaterialTheme.colorScheme.background, modifier = Modifier.fillMaxSize()) {
        Column(Modifier.fillMaxSize().safeDrawingPadding()) {
            PlainTopBar(state.tenantName.ifBlank { "现场验票" }, "选择工作点", state.connection, viewModel::logout)
            Column(Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(horizontal = 18.dp, vertical = 12.dp)) {
                SectionLabel("检票点")
                state.checkpoints.forEach { checkpoint ->
                    SelectableRow(
                        title = checkpoint.name,
                        subtitle = checkpoint.location,
                        selected = checkpoint.id == state.selectedCheckpointId,
                        onClick = { viewModel.selectCheckpoint(checkpoint.id) },
                    )
                }
                Spacer(Modifier.height(22.dp))
                SectionLabel("移动终端")
                if (state.selectedCheckpointId == 0L) {
                    EmptyMessage("先选择一个检票点")
                } else if (eligibleDevices.isEmpty()) {
                    EmptyMessage("该点位尚未绑定可用移动终端")
                } else {
                    eligibleDevices.forEach { device ->
                        SelectableRow(
                            title = device.name,
                            subtitle = device.serialNumber,
                            selected = device.id == state.selectedDeviceId,
                            onClick = { viewModel.selectDevice(device.id) },
                        )
                    }
                }
                ErrorBanner(state.error)
            }
            Column(Modifier.fillMaxWidth().background(Color.White).padding(horizontal = 18.dp, vertical = 14.dp)) {
                PrimaryButton("进入核销", state.busy, state.selectedCheckpointId != 0L && state.selectedDeviceId != 0L, viewModel::startSession)
                TextButton(onClick = viewModel::refreshTargets, enabled = !state.busy, modifier = Modifier.align(Alignment.CenterHorizontally)) {
                    Icon(Icons.Rounded.Refresh, null, Modifier.size(17.dp)); Spacer(Modifier.width(6.dp)); Text("刷新列表")
                }
            }
        }
    }
}

@Composable
private fun VerifyWorkspace(viewModel: MobileVerifyViewModel, cameraGranted: Boolean, requestCamera: () -> Unit) {
    val state by viewModel.state.collectAsState()
    var manualVisible by remember { mutableStateOf(false) }
    var manualCode by remember { mutableStateOf("") }
    var torchEnabled by remember { mutableStateOf(false) }
    val checkpoint = state.checkpoints.firstOrNull { it.id == state.selectedCheckpointId }
    val device = state.devices.firstOrNull { it.id == state.selectedDeviceId }
    val scanning = cameraGranted && !state.busy && !state.uncertain && state.preview == null && state.result == null && !manualVisible
    Box(Modifier.fillMaxSize().background(Color(0xFF101A1D))) {
        if (cameraGranted) {
            CameraScanner(scanning, torchEnabled, Modifier.fillMaxSize(), viewModel::inspectCode)
        } else {
            CameraPermissionState(requestCamera, Modifier.align(Alignment.Center))
        }
        if (cameraGranted) ScanFrame(scanning)
        ScannerTopBar(
            tenant = state.tenantName,
            checkpoint = checkpoint,
            connection = state.connection,
            onChangePoint = viewModel::changePoint,
            onLogout = viewModel::logout,
            modifier = Modifier.align(Alignment.TopCenter),
        )
        ScannerDock(
            state = state,
            device = device,
            torchEnabled = torchEnabled,
            onTorch = { torchEnabled = !torchEnabled },
            onManual = { manualVisible = true },
            onContinue = viewModel::clearResult,
            onRetry = viewModel::retryPending,
            modifier = Modifier.align(Alignment.BottomCenter),
        )
        AnimatedVisibility(state.busy, enter = fadeIn(), exit = fadeOut(), modifier = Modifier.align(Alignment.Center)) {
            Surface(color = Color(0xDD102024), shape = RoundedCornerShape(18.dp)) {
                Row(Modifier.padding(horizontal = 18.dp, vertical = 14.dp), verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(Modifier.size(22.dp), strokeWidth = 2.dp, color = Color.White)
                    Text("正在${if (state.preview == null) "读取票券" else "确认核销"}", color = Color.White, modifier = Modifier.padding(start = 10.dp))
                }
            }
        }
    }
    state.preview?.let {
        ConfirmationSheet(it, state.quantity, state.continuationConfirmed, state.busy, viewModel::changeQuantity, viewModel::setContinuationConfirmed, viewModel::dismissPreview, viewModel::confirmPreview)
    }
    if (manualVisible) {
        AlertDialog(
            onDismissRequest = { manualVisible = false },
            title = { Text("输入票码") },
            text = { VerifyTextField(manualCode, { manualCode = it }, "票码") },
            confirmButton = {
                Button(onClick = { manualVisible = false; viewModel.inspectCode(manualCode); manualCode = "" }, enabled = manualCode.isNotBlank()) { Text("读取票券") }
            },
            dismissButton = { TextButton(onClick = { manualVisible = false }) { Text("取消") } },
        )
    }
}

@Composable
private fun ScannerTopBar(tenant: String, checkpoint: Checkpoint?, connection: ConnectionState, onChangePoint: () -> Unit, onLogout: () -> Unit, modifier: Modifier = Modifier) {
    Surface(color = Color(0xEFFFFFFF), modifier = modifier.fillMaxWidth(), shadowElevation = 2.dp) {
        Row(Modifier.safeDrawingPadding().padding(horizontal = 14.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(38.dp).background(VerifyMint, CircleShape), contentAlignment = Alignment.Center) {
                Icon(Icons.Rounded.LocationOn, null, tint = VerifyTeal, modifier = Modifier.size(21.dp))
            }
            Column(Modifier.weight(1f).padding(horizontal = 10.dp)) {
                Text(tenant.ifBlank { "现场验票" }, style = MaterialTheme.typography.labelMedium, color = VerifyMuted, maxLines = 1)
                Text(checkpoint?.name ?: "移动核销", style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            ConnectionDot(connection)
            IconButton(onClick = onChangePoint) { Icon(Icons.Rounded.Edit, "更换点位", tint = VerifyInk) }
            IconButton(onClick = onLogout) { Icon(Icons.AutoMirrored.Rounded.Logout, "退出登录", tint = VerifyInk) }
        }
    }
}

@Composable
private fun ScanFrame(active: Boolean) {
    val transition = rememberInfiniteTransition(label = "scan")
    val progress by transition.animateFloat(0f, 1f, infiniteRepeatable(tween(1800), RepeatMode.Reverse), label = "line")
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Box(Modifier.size(260.dp).border(2.dp, if (active) Color(0xFFD3FFF4) else Color(0x88FFFFFF), RoundedCornerShape(22.dp))) {
            if (active) Box(Modifier.fillMaxWidth().height(2.dp).offset { androidx.compose.ui.unit.IntOffset(0, (236.dp.toPx() * progress).toInt()) }.padding(horizontal = 16.dp).background(Color(0xFF7CE5CA), CircleShape))
        }
        Text(
            if (active) "对准二维码，识别后先确认再核销" else "相机已暂停",
            color = Color.White,
            style = MaterialTheme.typography.bodySmall,
            modifier = Modifier.offset(y = 160.dp).background(Color(0xB3132023), RoundedCornerShape(20.dp)).padding(horizontal = 13.dp, vertical = 7.dp),
        )
    }
}

@Composable
private fun ScannerDock(state: top.edvulcan.ticket.verify.MobileVerifyUiState, device: MobileDevice?, torchEnabled: Boolean, onTorch: () -> Unit, onManual: () -> Unit, onContinue: () -> Unit, onRetry: () -> Unit, modifier: Modifier = Modifier) {
    Surface(color = Color.White, shape = RoundedCornerShape(topStart = 24.dp, topEnd = 24.dp), shadowElevation = 14.dp, modifier = modifier.fillMaxWidth()) {
        Column(Modifier.safeDrawingPadding().padding(horizontal = 18.dp, vertical = 14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(device?.name ?: "移动终端", style = MaterialTheme.typography.titleMedium)
                    Text(device?.serialNumber.orEmpty(), style = MaterialTheme.typography.bodySmall, color = VerifyMuted)
                }
                IconButton(onClick = onTorch, enabled = !state.busy && !state.uncertain) {
                    Icon(if (torchEnabled) Icons.Rounded.FlashOn else Icons.Rounded.FlashOff, "手电筒", tint = if (torchEnabled) VerifyWarning else VerifyInk)
                }
                IconButton(onClick = onManual, enabled = !state.busy && !state.uncertain && state.preview == null) {
                    Icon(Icons.Rounded.Edit, "输入票码", tint = VerifyInk)
                }
            }
            state.result?.let { CompactResult(it, onContinue) }
            if (state.uncertain) UncertainResult(state.error, state.busy, onRetry)
            if (state.result == null && !state.uncertain) {
                Text("扫描只读取票券信息，不会自动扣除次数", style = MaterialTheme.typography.bodySmall, color = VerifyMuted, modifier = Modifier.padding(top = 6.dp))
                if (state.error.isNotBlank()) ErrorBanner(state.error)
                state.recent.firstOrNull()?.let { recent ->
                    HorizontalDivider(Modifier.padding(vertical = 10.dp), color = VerifyBorder)
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Box(Modifier.size(8.dp).background(if (recent.allowed) VerifySuccess else VerifyDanger, CircleShape))
                        Text("上一笔  ${if (recent.allowed) "通过" else "拒绝"} · ${recent.quantity} 人", style = MaterialTheme.typography.labelMedium, modifier = Modifier.padding(start = 8.dp))
                    }
                }
            }
        }
    }
}

@Composable
private fun CompactResult(result: VerificationResult, onContinue: () -> Unit) {
    val color = if (result.allowed) VerifySuccess else VerifyDanger
    Column(Modifier.fillMaxWidth().padding(top = 8.dp).background(if (result.allowed) Color(0xFFE6F4ED) else Color(0xFFFBE9E6), RoundedCornerShape(16.dp)).padding(14.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(36.dp).background(color, CircleShape), contentAlignment = Alignment.Center) {
                Icon(if (result.allowed) Icons.Rounded.Check else Icons.Rounded.Close, null, tint = Color.White)
            }
            Column(Modifier.weight(1f).padding(start = 11.dp)) {
                Text(if (result.allowed) "核销成功" else "核销未通过", style = MaterialTheme.typography.titleLarge, color = color)
                Text(result.displayText, style = MaterialTheme.typography.bodyMedium, color = VerifyInk, maxLines = 2, overflow = TextOverflow.Ellipsis)
                if (result.allowed && result.quantity > 0) Text("本次 ${result.quantity} 人${if (result.pointRemaining >= 0) " · 本点剩余 ${result.pointRemaining} 次" else ""}", style = MaterialTheme.typography.labelMedium, color = color)
            }
        }
        Button(onClick = onContinue, colors = ButtonDefaults.buttonColors(containerColor = color), modifier = Modifier.fillMaxWidth().padding(top = 12.dp)) { Text("继续扫码") }
    }
}

@Composable
private fun UncertainResult(message: String, busy: Boolean, retry: () -> Unit) {
    Column(Modifier.fillMaxWidth().padding(top = 8.dp).background(Color(0xFFFFF3D8), RoundedCornerShape(16.dp)).padding(14.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.Rounded.Warning, null, tint = VerifyWarning)
            Text("结果待确认", style = MaterialTheme.typography.titleMedium, color = VerifyWarning, modifier = Modifier.padding(start = 8.dp))
        }
        Text(message.ifBlank { "不能继续扫描下一张票，只能恢复刚才的操作。" }, style = MaterialTheme.typography.bodyMedium, color = VerifyInk, modifier = Modifier.padding(top = 6.dp))
        Button(onClick = retry, enabled = !busy, colors = ButtonDefaults.buttonColors(containerColor = VerifyWarning), modifier = Modifier.fillMaxWidth().padding(top = 10.dp)) { Text("恢复核销结果") }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ConfirmationSheet(preview: VerificationPreview, quantity: Int, continuationConfirmed: Boolean, busy: Boolean, changeQuantity: (Int) -> Unit, setContinuation: (Boolean) -> Unit, dismiss: () -> Unit, confirm: () -> Unit) {
    ModalBottomSheet(onDismissRequest = { if (!busy) dismiss() }, containerColor = Color.White, dragHandle = null) {
        Column(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 18.dp)) {
            Row(verticalAlignment = Alignment.Top) {
                Column(Modifier.weight(1f)) {
                    Text("确认核销", style = MaterialTheme.typography.headlineSmall)
                    Text("确认前不会扣除任何次数", style = MaterialTheme.typography.bodyMedium, color = VerifyMuted, modifier = Modifier.padding(top = 3.dp))
                }
                IconButton(onClick = dismiss, enabled = !busy) { Icon(Icons.Rounded.Close, "取消核销") }
            }
            Column(Modifier.fillMaxWidth().padding(top = 14.dp).background(MaterialTheme.colorScheme.surfaceVariant, RoundedCornerShape(16.dp)).padding(15.dp)) {
                Text(preview.productName, style = MaterialTheme.typography.titleLarge, maxLines = 2, overflow = TextOverflow.Ellipsis)
                Text("本点已使用 ${preview.pointUsed} 次 · 剩余 ${preview.pointRemaining} 次", style = MaterialTheme.typography.bodyMedium, color = VerifyMuted, modifier = Modifier.padding(top = 5.dp))
            }
            if (preview.batchAllowed && preview.codeMode == "order") {
                Row(Modifier.fillMaxWidth().padding(vertical = 18.dp), verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text("本次核销人数", style = MaterialTheme.typography.titleMedium)
                        Text("最多 ${preview.maxQuantity} 人，默认 1 人", style = MaterialTheme.typography.bodySmall, color = VerifyMuted)
                    }
                    QuantityStepper(quantity, preview.maxQuantity, busy, changeQuantity)
                }
            } else {
                Row(Modifier.fillMaxWidth().padding(vertical = 18.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text("本次核销人数", style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                    Text("1 人", style = MaterialTheme.typography.titleLarge)
                }
            }
            if (preview.requiresRepeatConfirmation) {
                Column(Modifier.fillMaxWidth().background(Color(0xFFFFF3D8), RoundedCornerShape(16.dp)).padding(14.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Icon(Icons.Rounded.Warning, null, tint = VerifyWarning, modifier = Modifier.size(20.dp))
                        Text("该票码刚刚有核销记录", style = MaterialTheme.typography.titleMedium, color = VerifyWarning, modifier = Modifier.padding(start = 7.dp))
                    }
                    preview.recentOperation?.let { Text("上次核销 ${it.quantity} 人。继续前请重新核对同行人数。", style = MaterialTheme.typography.bodyMedium, color = VerifyInk, modifier = Modifier.padding(top = 6.dp)) }
                    Row(Modifier.fillMaxWidth().clickable(enabled = !busy) { setContinuation(!continuationConfirmed) }.padding(top = 5.dp), verticalAlignment = Alignment.CenterVertically) {
                        Checkbox(continuationConfirmed, setContinuation, enabled = !busy, colors = CheckboxDefaults.colors(checkedColor = VerifyWarning))
                        Text("我确认这是继续核销", style = MaterialTheme.typography.labelLarge)
                    }
                }
            }
            PrimaryButton(
                label = "确认核销 $quantity 人",
                busy = busy,
                enabled = !preview.requiresRepeatConfirmation || continuationConfirmed,
                onClick = confirm,
                modifier = Modifier.padding(top = 16.dp),
            )
            TextButton(onClick = dismiss, enabled = !busy, modifier = Modifier.align(Alignment.CenterHorizontally)) { Text("取消，不核销") }
        }
    }
}

@Composable
private fun QuantityStepper(quantity: Int, maximum: Int, busy: Boolean, change: (Int) -> Unit) {
    Surface(shape = RoundedCornerShape(14.dp), color = Color.White, border = BorderStroke(1.dp, VerifyBorder)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = { change(-1) }, enabled = quantity > 1 && !busy) { Icon(Icons.Rounded.Remove, "减少人数") }
            Text(quantity.toString(), style = MaterialTheme.typography.titleLarge, modifier = Modifier.padding(horizontal = 4.dp))
            IconButton(onClick = { change(1) }, enabled = quantity < maximum && !busy) { Icon(Icons.Rounded.Add, "增加人数") }
        }
    }
}

@Composable
private fun PlainTopBar(tenant: String, title: String, connection: ConnectionState, logout: () -> Unit) {
    Row(Modifier.fillMaxWidth().background(Color.White).safeDrawingPadding().padding(horizontal = 18.dp, vertical = 12.dp), verticalAlignment = Alignment.CenterVertically) {
        BrandMark(42)
        Column(Modifier.weight(1f).padding(start = 11.dp)) {
            Text(tenant, style = MaterialTheme.typography.labelMedium, color = VerifyMuted)
            Text(title, style = MaterialTheme.typography.titleLarge)
        }
        ConnectionDot(connection)
        IconButton(onClick = logout) { Icon(Icons.AutoMirrored.Rounded.Logout, "退出登录") }
    }
}

@Composable
private fun SelectableRow(title: String, subtitle: String, selected: Boolean, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().padding(top = 9.dp).clip(RoundedCornerShape(14.dp)).background(if (selected) VerifyMint else Color.White)
            .border(1.dp, if (selected) VerifyTeal else VerifyBorder, RoundedCornerShape(14.dp)).clickable(onClick = onClick).padding(15.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(36.dp).background(if (selected) VerifyTeal else MaterialTheme.colorScheme.surfaceVariant, CircleShape), contentAlignment = Alignment.Center) {
            Icon(if (selected) Icons.Rounded.Check else Icons.Rounded.LocationOn, null, tint = if (selected) Color.White else VerifyMuted, modifier = Modifier.size(19.dp))
        }
        Column(Modifier.weight(1f).padding(start = 11.dp)) {
            Text(title, style = MaterialTheme.typography.titleMedium)
            if (subtitle.isNotBlank()) Text(subtitle, style = MaterialTheme.typography.bodySmall, color = VerifyMuted)
        }
        Icon(Icons.AutoMirrored.Rounded.KeyboardArrowRight, null, tint = VerifyMuted)
    }
}

@Composable
private fun ConnectionDot(state: ConnectionState) {
    val color = when (state) {
        ConnectionState.ONLINE -> VerifySuccess
        ConnectionState.DEGRADED -> VerifyWarning
        else -> VerifyMuted
    }
    Row(verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.size(7.dp).background(color, CircleShape))
        Text(when (state) { ConnectionState.ONLINE -> "正常"; ConnectionState.CHECKING -> "连接中"; ConnectionState.DEGRADED -> "不稳定"; ConnectionState.OFFLINE -> "离线" }, style = MaterialTheme.typography.labelMedium, color = color, modifier = Modifier.padding(start = 5.dp))
    }
}

@Composable
private fun BrandMark(size: Int = 48) {
    Box(Modifier.size(size.dp).background(VerifyTeal, RoundedCornerShape(14.dp)), contentAlignment = Alignment.Center) {
        Icon(Icons.Rounded.Bolt, null, tint = Color.White, modifier = Modifier.size((size * .55f).dp))
    }
}

@Composable
private fun CameraPermissionState(request: () -> Unit, modifier: Modifier = Modifier) {
    Column(modifier, horizontalAlignment = Alignment.CenterHorizontally) {
        Box(Modifier.size(68.dp).background(Color(0xFF24363A), CircleShape), contentAlignment = Alignment.Center) { Icon(Icons.Rounded.Bolt, null, tint = Color.White) }
        Text("开启相机开始验票", color = Color.White, style = MaterialTheme.typography.titleLarge, modifier = Modifier.padding(top = 15.dp))
        Button(onClick = request, colors = ButtonDefaults.buttonColors(containerColor = VerifyTeal), modifier = Modifier.padding(top = 12.dp)) { Text("允许相机") }
    }
}

@Composable
private fun PrimaryButton(label: String, busy: Boolean, enabled: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Button(
        onClick = onClick, enabled = enabled && !busy,
        colors = ButtonDefaults.buttonColors(containerColor = VerifyTeal),
        shape = RoundedCornerShape(14.dp),
        modifier = modifier.fillMaxWidth().height(54.dp),
    ) {
        if (busy) CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp, color = Color.White) else Text(label, style = MaterialTheme.typography.labelLarge)
    }
}

@Composable
private fun VerifyTextField(value: String, onValueChange: (String) -> Unit, label: String, modifier: Modifier = Modifier) {
    OutlinedTextField(value, onValueChange, label = { Text(label) }, singleLine = true, shape = RoundedCornerShape(14.dp), modifier = modifier.fillMaxWidth())
}

@Composable private fun SectionLabel(text: String) { Text(text, style = MaterialTheme.typography.labelLarge, color = VerifyMuted) }
@Composable private fun EmptyMessage(text: String) { Text(text, style = MaterialTheme.typography.bodyMedium, color = VerifyMuted, modifier = Modifier.fillMaxWidth().padding(vertical = 22.dp)) }
@Composable private fun ErrorBanner(error: String) { if (error.isNotBlank()) Text(error, style = MaterialTheme.typography.bodyMedium, color = VerifyDanger, modifier = Modifier.fillMaxWidth().padding(top = 12.dp).background(Color(0xFFFBE9E6), RoundedCornerShape(12.dp)).padding(12.dp)) }
