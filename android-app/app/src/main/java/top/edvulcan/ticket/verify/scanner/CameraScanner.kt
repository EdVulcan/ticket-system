package top.edvulcan.ticket.verify.scanner

import android.annotation.SuppressLint
import androidx.camera.core.CameraSelector
import androidx.camera.core.Camera
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.google.mlkit.vision.barcode.BarcodeScanning
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.common.InputImage
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

@SuppressLint("UnsafeOptInUsageError")
@Composable
fun CameraScanner(enabled: Boolean, torchEnabled: Boolean, modifier: Modifier = Modifier, onCode: (String) -> Unit) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val currentOnCode by rememberUpdatedState(onCode)
    val currentEnabled by rememberUpdatedState(enabled)
    val executor = remember { Executors.newSingleThreadExecutor() }
    val scanner = remember { BarcodeScanning.getClient() }
    val delivering = remember { AtomicBoolean(false) }
    var provider by remember { mutableStateOf<ProcessCameraProvider?>(null) }
    var analysis by remember { mutableStateOf<ImageAnalysis?>(null) }
    var preview by remember { mutableStateOf<Preview?>(null) }
    var camera by remember { mutableStateOf<Camera?>(null) }

    LaunchedEffect(enabled) {
        if (enabled) delivering.set(false)
    }
    LaunchedEffect(torchEnabled, camera) {
        camera?.cameraControl?.enableTorch(torchEnabled)
    }

    AndroidView(
        modifier = modifier,
        factory = { viewContext ->
            PreviewView(viewContext).apply {
                scaleType = PreviewView.ScaleType.FILL_CENTER
                implementationMode = PreviewView.ImplementationMode.COMPATIBLE
                ProcessCameraProvider.getInstance(viewContext).addListener({
                    val cameraProvider = ProcessCameraProvider.getInstance(viewContext).get()
                    val previewUseCase = Preview.Builder().build().also { it.surfaceProvider = surfaceProvider }
                    val analysisUseCase = ImageAnalysis.Builder()
                        .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                        .build()
                    analysisUseCase.setAnalyzer(executor) { imageProxy ->
                        val mediaImage = imageProxy.image
                        if (!currentEnabled || mediaImage == null || delivering.get()) {
                            imageProxy.close()
                            return@setAnalyzer
                        }
                        val input = InputImage.fromMediaImage(mediaImage, imageProxy.imageInfo.rotationDegrees)
                        scanner.process(input)
                            .addOnSuccessListener { barcodes ->
                                val value = barcodes.firstOrNull {
                                    it.format == Barcode.FORMAT_QR_CODE && !it.rawValue.isNullOrBlank()
                                }?.rawValue?.trim()
                                if (!value.isNullOrBlank() && delivering.compareAndSet(false, true)) {
                                    currentOnCode(value)
                                }
                            }
                            .addOnCompleteListener { imageProxy.close() }
                    }
                    runCatching {
                        cameraProvider.unbindAll()
                        camera = cameraProvider.bindToLifecycle(lifecycleOwner, CameraSelector.DEFAULT_BACK_CAMERA, previewUseCase, analysisUseCase)
                        provider = cameraProvider
                        preview = previewUseCase
                        analysis = analysisUseCase
                    }
                }, ContextCompat.getMainExecutor(viewContext))
            }
        },
    )

    DisposableEffect(Unit) {
        onDispose {
            analysis?.clearAnalyzer()
            provider?.unbind(*(listOfNotNull(preview, analysis).toTypedArray()))
            camera = null
            scanner.close()
            executor.shutdown()
        }
    }
}
