package top.edvulcan.ticket.verify.ui.theme

import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

private val VerifyColorScheme = lightColorScheme(
    primary = VerifyTeal,
    onPrimary = Color.White,
    primaryContainer = VerifyMint,
    onPrimaryContainer = VerifyTealDark,
    secondary = VerifyTealDark,
    background = VerifyCanvas,
    onBackground = VerifyInk,
    surface = Color.White,
    onSurface = VerifyInk,
    surfaceVariant = Color(0xFFEAF0F0),
    onSurfaceVariant = VerifyMuted,
    outline = VerifyBorder,
    error = VerifyDanger,
)

private val VerifyShapes = Shapes(
    extraSmall = RoundedCornerShape(6.dp),
    small = RoundedCornerShape(10.dp),
    medium = RoundedCornerShape(14.dp),
    large = RoundedCornerShape(20.dp),
    extraLarge = RoundedCornerShape(26.dp),
)

@Composable
fun YuejuyouTheme(
    @Suppress("UNUSED_PARAMETER") darkTheme: Boolean = false,
    @Suppress("UNUSED_PARAMETER") dynamicColor: Boolean = false,
    content: @Composable () -> Unit,
) {
    MaterialTheme(
        colorScheme = VerifyColorScheme,
        typography = VerifyTypography,
        shapes = VerifyShapes,
        content = content,
    )
}
