package top.edvulcan.ticket.verify

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class MobileVerifyLaunchTest {
    @get:Rule
    val composeRule = createAndroidComposeRule<MainActivity>()

    @Test
    fun loginScreenShowsTheThreeRequiredCredentialsAndPrimaryAction() {
        composeRule.onNodeWithText("移动核销").assertIsDisplayed()
        composeRule.onNodeWithText("系统编号").assertIsDisplayed()
        composeRule.onNodeWithText("员工工号").assertIsDisplayed()
        composeRule.onNodeWithText("密码").assertIsDisplayed()
        composeRule.onNodeWithText("登录并开始").assertIsDisplayed()
    }
}
