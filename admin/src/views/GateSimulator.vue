<template>
  <section class="max-w-[900px] mx-auto">
    <el-card>
      <template #header>
        <div class="flex items-center justify-between gap-3">
          <span class="font-semibold">闸机联调说明</span>
          <el-tag type="warning">等待真实硬件协议</el-tag>
        </div>
      </template>

      <el-alert
        title="浏览器不能直接模拟真实闸机"
        type="warning"
        :closable="false"
        show-icon
      >
        当前硬件入口要求设备使用独立密钥签名请求。管理端不会收集或提交设备原始密钥，也不会伪造核销或开闸成功。
      </el-alert>

      <div class="mt-6 space-y-4 text-sm text-gray-600">
        <p>请在 Linux 闸机控制机上运行 gate-client，通过本机扫码入口和测试驱动进行联调。</p>
        <p>真实三辊闸的继电器、串口、RS485、TCP 或厂商 SDK 协议确定后，再接入对应硬件适配器。</p>
        <p>本页面只保留说明入口；不再从浏览器直接调用硬件核销接口。</p>
      </div>

      <el-divider />

      <div class="grid gap-3 md:grid-cols-3">
        <div class="rounded-lg bg-gray-50 p-4">
          <div class="font-medium text-gray-900">扫码入口</div>
          <code class="mt-2 block text-xs text-gray-500">POST http://127.0.0.1:19300/scan</code>
        </div>
        <div class="rounded-lg bg-gray-50 p-4">
          <div class="font-medium text-gray-900">恢复入口</div>
          <code class="mt-2 block text-xs text-gray-500">POST http://127.0.0.1:19300/recovery</code>
        </div>
        <div class="rounded-lg bg-gray-50 p-4">
          <div class="font-medium text-gray-900">状态检查</div>
          <code class="mt-2 block text-xs text-gray-500">GET http://127.0.0.1:19300/status</code>
        </div>
      </div>
    </el-card>
  </section>
</template>
