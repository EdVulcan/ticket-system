package top.edvulcan.ticket.verify

import android.Manifest
import android.content.pm.PackageManager
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.core.content.ContextCompat
import androidx.lifecycle.viewmodel.compose.viewModel
import top.edvulcan.ticket.verify.ui.MobileVerifyApp
import top.edvulcan.ticket.verify.ui.theme.YuejuyouTheme

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            YuejuyouTheme(dynamicColor = false) {
                TicketVerifyRoot()
            }
        }
    }
}

@Composable
private fun TicketVerifyRoot(viewModel: MobileVerifyViewModel = viewModel()) {
    val context = androidx.compose.ui.platform.LocalContext.current
    var cameraGranted by remember {
        mutableStateOf(ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED)
    }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted -> cameraGranted = granted }
    MobileVerifyApp(viewModel, cameraGranted) { launcher.launch(Manifest.permission.CAMERA) }
}
