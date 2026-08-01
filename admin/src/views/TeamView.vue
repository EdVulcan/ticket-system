<template>
  <section class="space-y-5">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-xl font-semibold text-gray-900">团队业务</h2>
        <p class="mt-1 text-sm text-gray-500">旅行社维护团队计划，景区供应商完成名单核对、分批入园和双方结算。</p>
      </div>
      <el-button :icon="Refresh" circle title="刷新当前页面" @click="refreshActiveTab" />
    </div>

    <div class="flex flex-wrap gap-2" v-if="isTravelAgency">
      <el-button type="primary" :icon="Plus" @click="openGroupDialog">新建团队</el-button>
      <el-button :icon="DocumentAdd" @click="openContractDialog">新建合同</el-button>
    </div>

    <el-tabs v-model="activeTab" @tab-change="handleTabChange">
      <el-tab-pane label="团队计划" name="groups">
        <el-table :data="groups" v-loading="loading" stripe empty-text="暂无团队计划">
          <el-table-column prop="group_no" label="团号" min-width="180" />
          <el-table-column prop="name" label="团队名称" min-width="150" />
          <el-table-column label="业务视角" width="110">
            <template #default="{ row }">
              <el-tag v-if="isGroupOwner(row)" type="primary" effect="plain">旅行社</el-tag>
              <el-tag v-else type="success" effect="plain">景区履约</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="游玩日期" width="125"><template #default="{ row }">{{ dateOnly(row.visit_date) }}</template></el-table-column>
          <el-table-column label="人数" width="90"><template #default="{ row }">{{ row.expected_count }} 人</template></el-table-column>
          <el-table-column label="状态" width="120">
            <template #default="{ row }"><el-tag :type="groupStatusType(row.status)">{{ groupStatusText(row.status) }}</el-tag></template>
          </el-table-column>
          <el-table-column label="结算" width="110"><template #default="{ row }">{{ settlementStatusText(row.settlement_status) }}</template></el-table-column>
          <el-table-column label="操作" width="290" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="openGroupDetail(row)">{{ isGroupSupplier(row) ? '履约入园' : '名单详情' }}</el-button>
              <el-button v-if="isGroupOwner(row)" link type="primary" :disabled="row.status !== 'draft'" @click="openAttachOrder(row)">绑定订单</el-button>
              <el-button v-if="canGenerateSettlement(row)" link type="success" @click="generateSettlement(row)">生成结算单</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="合同" name="contracts">
        <el-table :data="contracts" v-loading="contractsLoading" stripe empty-text="暂无团队合同">
          <el-table-column prop="contract_no" label="合同号" min-width="180" />
          <el-table-column label="旅行社" width="110"><template #default="{ row }">租户 {{ row.travel_tenant_id }}</template></el-table-column>
          <el-table-column label="供应商" width="110"><template #default="{ row }">租户 {{ row.supplier_tenant_id }}</template></el-table-column>
          <el-table-column label="授信额度" width="130"><template #default="{ row }">¥{{ cents(row.credit_limit_cents) }}</template></el-table-column>
          <el-table-column label="账期" width="100"><template #default="{ row }">{{ row.settlement_days }} 天</template></el-table-column>
          <el-table-column prop="status" label="状态" width="110" />
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="双方结算" name="settlements">
        <el-table :data="settlements" v-loading="settlementsLoading" stripe empty-text="暂无团队结算单">
          <el-table-column prop="statement_no" label="结算单" min-width="200" />
          <el-table-column label="团队" width="100"><template #default="{ row }">#{{ row.group_id }}</template></el-table-column>
          <el-table-column label="旅行社" width="110"><template #default="{ row }">租户 {{ row.travel_tenant_id }}</template></el-table-column>
          <el-table-column label="供应商" width="110"><template #default="{ row }">租户 {{ row.supplier_tenant_id }}</template></el-table-column>
          <el-table-column label="总额" width="120"><template #default="{ row }">¥{{ cents(row.gross_cents) }}</template></el-table-column>
          <el-table-column label="退款" width="120"><template #default="{ row }">¥{{ cents(row.refund_cents) }}</template></el-table-column>
          <el-table-column label="应付" width="120"><template #default="{ row }"><strong>¥{{ cents(row.net_cents) }}</strong></template></el-table-column>
          <el-table-column label="状态" width="130"><template #default="{ row }"><el-tag :type="settlementStatusType(row.status)">{{ settlementStatusText(row.status) }}</el-tag></template></el-table-column>
          <el-table-column label="操作" width="100" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openSettlement(row)">处理</el-button></template></el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="资金与对账" name="accounts">
        <div class="mb-3 text-sm text-gray-500">仅汇总旅行社与景区供应商之间的团队预付款、授信占用和结算结果。</div>
        <el-table :data="accounts" v-loading="accountsLoading" stripe empty-text="暂无团队资金往来">
          <el-table-column label="旅行社" width="110"><template #default="{ row }">租户 {{ row.travel_tenant_id }}</template></el-table-column>
          <el-table-column label="供应商" width="110"><template #default="{ row }">租户 {{ row.supplier_tenant_id }}</template></el-table-column>
          <el-table-column prop="active_contract_count" label="有效合同" width="100" />
          <el-table-column prop="group_count" label="团队数" width="90" />
          <el-table-column label="合同总额" min-width="120"><template #default="{ row }">¥{{ cents(row.contract_amount_cents) }}</template></el-table-column>
          <el-table-column label="预付款" min-width="110"><template #default="{ row }">¥{{ cents(row.deposit_cents) }}</template></el-table-column>
          <el-table-column label="授信额度" min-width="120"><template #default="{ row }">¥{{ cents(row.credit_limit_cents) }}</template></el-table-column>
          <el-table-column label="已占授信" min-width="120"><template #default="{ row }">¥{{ cents(row.credit_used_cents) }}</template></el-table-column>
          <el-table-column label="可用授信" min-width="120"><template #default="{ row }"><span :class="row.available_credit_cents < 0 ? 'text-red-600 font-medium' : ''">{{ signedCents(row.available_credit_cents) }}</span></template></el-table-column>
          <el-table-column label="待结" min-width="120"><template #default="{ row }">¥{{ cents(row.pending_cents) }}</template></el-table-column>
          <el-table-column label="已付" min-width="120"><template #default="{ row }">¥{{ cents(row.paid_cents) }}</template></el-table-column>
          <el-table-column prop="disputed_count" label="争议单" width="90" />
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="groupDialog" title="新建团队计划" width="560px">
      <el-form :model="groupForm" label-position="top">
        <el-form-item label="团队名称" required><el-input v-model="groupForm.name" maxlength="100" /></el-form-item>
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="供应商租户 ID" required><el-input-number v-model="groupForm.supplier_tenant_id" :min="1" class="w-full" /></el-form-item>
          <el-form-item label="供应商景区 ID" required><el-input-number v-model="groupForm.scenic_area_id" :min="1" class="w-full" /></el-form-item>
          <el-form-item label="合同 ID" required><el-input-number v-model="groupForm.contract_id" :min="1" class="w-full" /></el-form-item>
          <el-form-item label="计划人数" required><el-input-number v-model="groupForm.expected_count" :min="1" class="w-full" /></el-form-item>
        </div>
        <el-form-item label="游玩日期" required><el-date-picker v-model="groupForm.visit_date" type="date" value-format="YYYY-MM-DD" class="w-full" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="groupDialog = false">取消</el-button><el-button type="primary" :loading="saving" @click="createGroup">创建</el-button></template>
    </el-dialog>

    <el-dialog v-model="contractDialog" title="新建旅行社合同" width="560px">
      <el-form :model="contractForm" label-position="top">
        <el-form-item label="供应商租户 ID" required><el-input-number v-model="contractForm.supplier_tenant_id" :min="1" class="w-full" /></el-form-item>
        <el-form-item label="合同号" required><el-input v-model="contractForm.contract_no" maxlength="100" /></el-form-item>
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="账期（天）"><el-input-number v-model="contractForm.settlement_days" :min="0" class="w-full" /></el-form-item>
          <el-form-item label="授信额度（元）"><el-input-number v-model="contractCreditYuan" :min="0" :precision="2" class="w-full" /></el-form-item>
        </div>
      </el-form>
      <template #footer><el-button @click="contractDialog = false">取消</el-button><el-button type="primary" :loading="savingContract" @click="createContract">创建</el-button></template>
    </el-dialog>

    <el-dialog v-model="detailDialog" :title="`团队履约：${selectedGroup?.name || ''}`" width="1040px" :close-on-click-modal="false">
      <div v-if="selectedGroup" class="space-y-5">
        <el-descriptions :column="4" border>
          <el-descriptions-item label="团号">{{ selectedGroup.group_no }}</el-descriptions-item>
          <el-descriptions-item label="游玩日期">{{ dateOnly(selectedGroup.visit_date) }}</el-descriptions-item>
          <el-descriptions-item label="计划人数">{{ selectedGroup.expected_count }} 人</el-descriptions-item>
          <el-descriptions-item label="状态">{{ groupStatusText(selectedGroup.status) }}</el-descriptions-item>
        </el-descriptions>

        <section>
          <div class="mb-3 flex items-center justify-between">
            <div>
              <h3 class="font-medium text-gray-900">游客与票权益</h3>
              <p class="text-xs text-gray-500">只有已匹配有效门票的游客可以进入本批次。</p>
            </div>
            <el-button :icon="Refresh" circle title="刷新名单和批次" @click="loadGroupDetail" />
          </div>
          <el-table :data="members" v-loading="detailLoading" height="300" stripe @selection-change="entryMemberSelection = $event">
            <el-table-column v-if="canEnterSelectedGroup" type="selection" width="48" :selectable="(member: any) => member.status === 'ticketed'" />
            <el-table-column type="index" width="50" />
            <el-table-column prop="name" label="姓名" min-width="120" />
            <el-table-column prop="identity_no" label="证件号" min-width="180" />
            <el-table-column prop="phone" label="手机号" width="135" />
            <el-table-column prop="ticket_code" label="票码" min-width="180" />
            <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="memberStatusType(row.status)" effect="plain">{{ memberStatusText(row.status) }}</el-tag></template></el-table-column>
            <el-table-column v-if="canChangeMembers" label="操作" width="90" fixed="right">
              <template #default="{ row }"><el-button v-if="!['entered', 'cancelled'].includes(row.status)" link type="danger" @click="removeTemporaryMember(row)">临时减员</el-button></template>
            </el-table-column>
          </el-table>
          <div v-if="canChangeMembers" class="mt-3 flex justify-end"><el-button type="primary" plain :icon="Plus" @click="openTemporaryMemberDialog">临时加员</el-button></div>
        </section>

        <section>
          <div class="mb-3 flex items-center justify-between">
            <div>
              <h3 class="font-medium text-gray-900">团队确认单</h3>
              <p class="text-xs text-gray-500">每次提交形成新版本，供应商确认收到后仍保留原记录。</p>
            </div>
            <el-button v-if="canSubmitConfirmation" type="primary" plain @click="openConfirmationDialog">提交新版本</el-button>
          </div>
          <el-table :data="confirmations" size="small" border empty-text="尚未提交确认单">
            <el-table-column prop="sequence" label="版本" width="70" />
            <el-table-column prop="confirmed_count" label="确认人数" width="100" />
            <el-table-column label="导游" min-width="130"><template #default="{ row }">{{ row.guide_name || '-' }}<span v-if="row.guide_phone" class="text-gray-500"> {{ row.guide_phone }}</span></template></el-table-column>
            <el-table-column prop="plate_number" label="车辆" width="110" />
            <el-table-column prop="notes" label="现场说明" min-width="180" />
            <el-table-column label="提交时间" width="180"><template #default="{ row }">{{ dateTime(row.submitted_at) }}</template></el-table-column>
            <el-table-column label="供应商确认" width="130">
              <template #default="{ row }">
                <el-tag v-if="row.supplier_acknowledged_at" type="success" effect="plain">已确认</el-tag>
                <el-button v-else-if="isGroupSupplier(selectedGroup)" link type="primary" @click="acknowledgeConfirmation(row)">确认收到</el-button>
                <el-tag v-else type="warning" effect="plain">待确认</el-tag>
              </template>
            </el-table-column>
          </el-table>
        </section>

        <section v-if="canEnterSelectedGroup" class="rounded border border-gray-200 bg-gray-50 p-4">
          <div class="flex flex-wrap items-end gap-3">
            <el-form-item label="本次入园设备" class="mb-0 min-w-64">
              <el-select v-model="entryDeviceID" placeholder="选择本景区在线闸机" filterable>
                <el-option v-for="device in admissionDevices" :key="device.id" :label="`${device.name}（${device.serial_number}）`" :value="device.id" />
              </el-select>
            </el-form-item>
            <div class="pb-1 text-sm text-gray-600">已选择 {{ entryMemberSelection.length }} 人</div>
            <el-button type="success" :loading="entering" :disabled="!entryDeviceID || !entryMemberSelection.length" @click="enterBatch">确认本批入园</el-button>
          </div>
        </section>

        <section>
          <h3 class="mb-3 font-medium text-gray-900">分批入园记录</h3>
          <el-table :data="entryBatches" size="small" border empty-text="尚无入园批次">
            <el-table-column prop="batch_no" label="批次号" min-width="190" />
            <el-table-column prop="entered_count" label="入园人数" width="100" />
            <el-table-column prop="device_id" label="设备 ID" width="100" />
            <el-table-column prop="operator_id" label="操作员 ID" width="110" />
            <el-table-column label="入园时间" width="180"><template #default="{ row }">{{ dateTime(row.entered_at) }}</template></el-table-column>
          </el-table>
        </section>

        <section v-if="memberChanges.length">
          <h3 class="mb-3 font-medium text-gray-900">临时人数变更</h3>
          <el-table :data="memberChanges" size="small" border>
            <el-table-column prop="sequence" label="序号" width="70" />
            <el-table-column label="类型" width="90"><template #default="{ row }"><el-tag :type="row.change_type === 'add' ? 'success' : 'danger'" effect="plain">{{ row.change_type === 'add' ? '加员' : '减员' }}</el-tag></template></el-table-column>
            <el-table-column prop="member_name" label="游客" min-width="120" />
            <el-table-column label="人数变化" width="110"><template #default="{ row }">{{ row.before_expected_count }} → {{ row.after_expected_count }}</template></el-table-column>
            <el-table-column prop="reason" label="原因" min-width="220" />
            <el-table-column label="时间" width="180"><template #default="{ row }">{{ dateTime(row.created_at) }}</template></el-table-column>
          </el-table>
        </section>

        <section v-if="canEditRoster">
          <el-divider />
          <el-form label-position="top">
            <el-form-item label="替换名单（每行：姓名,证件号,手机号；支持 CSV/Tab）">
              <el-input v-model="rosterText" type="textarea" :rows="5" placeholder="张三,110101...,13800000000" />
            </el-form-item>
          </el-form>
          <div class="flex justify-end"><el-button type="primary" :loading="savingRoster" :disabled="!rosterText.trim()" @click="replaceRoster">替换名单</el-button></div>
        </section>
      </div>
      <template #footer><el-button @click="detailDialog = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="confirmationDialog" title="提交团队确认单" width="560px" append-to-body>
      <el-form label-position="top">
        <el-form-item label="现场确认人数" required><el-input-number v-model="confirmationForm.confirmed_count" :min="1" class="w-full" /></el-form-item>
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="导游"><el-select v-model="confirmationForm.guide_id" clearable filterable class="w-full"><el-option v-for="guide in guides" :key="guide.id" :label="`${guide.name} ${guide.phone || ''}`" :value="guide.id" /></el-select></el-form-item>
          <el-form-item label="车辆"><el-select v-model="confirmationForm.vehicle_id" clearable filterable class="w-full"><el-option v-for="vehicle in vehicles" :key="vehicle.id" :label="`${vehicle.plate_number} ${vehicle.driver_name || ''}`" :value="vehicle.id" /></el-select></el-form-item>
        </div>
        <el-form-item label="现场说明"><el-input v-model="confirmationForm.notes" type="textarea" :rows="3" maxlength="500" show-word-limit /></el-form-item>
      </el-form>
      <template #footer><el-button @click="confirmationDialog = false">取消</el-button><el-button type="primary" :loading="confirmationSaving" @click="submitConfirmation">提交版本</el-button></template>
    </el-dialog>

    <el-dialog v-model="temporaryMemberDialog" title="临时加员" width="500px" append-to-body>
      <el-form label-position="top">
        <el-form-item label="姓名" required><el-input v-model="temporaryMemberForm.name" maxlength="80" /></el-form-item>
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="证件号"><el-input v-model="temporaryMemberForm.identity_no" maxlength="80" /></el-form-item>
          <el-form-item label="手机号"><el-input v-model="temporaryMemberForm.phone" maxlength="30" /></el-form-item>
        </div>
        <el-form-item label="加员原因" required><el-input v-model="temporaryMemberForm.reason" type="textarea" :rows="3" maxlength="255" show-word-limit /></el-form-item>
      </el-form>
      <template #footer><el-button @click="temporaryMemberDialog = false">取消</el-button><el-button type="primary" :loading="memberChangeSaving" @click="addTemporaryMember">确认加员</el-button></template>
    </el-dialog>

    <el-dialog v-model="attachOrderDialog" title="绑定已支付团队订单" width="440px">
      <el-form label-position="top"><el-form-item label="订单 ID" required><el-input-number v-model="attachOrderId" :min="1" class="w-full" /></el-form-item></el-form>
      <template #footer><el-button @click="attachOrderDialog = false">取消</el-button><el-button type="primary" :loading="attachingOrder" @click="attachOrder">绑定</el-button></template>
    </el-dialog>

    <el-dialog v-model="settlementDialog" title="团队结算处理" width="720px" :close-on-click-modal="false">
      <el-descriptions v-if="selectedSettlement" :column="2" border>
        <el-descriptions-item label="结算单" :span="2">{{ selectedSettlement.statement_no }}</el-descriptions-item>
        <el-descriptions-item label="履约总额">¥{{ cents(selectedSettlement.gross_cents) }}</el-descriptions-item>
        <el-descriptions-item label="退款冲减">¥{{ cents(selectedSettlement.refund_cents) }}</el-descriptions-item>
        <el-descriptions-item label="已付预款">¥{{ cents(selectedSettlement.deposit_cents) }}</el-descriptions-item>
        <el-descriptions-item label="争议调整">{{ signedCents(selectedSettlement.adjustment_cents) }}</el-descriptions-item>
        <el-descriptions-item label="最终应付"><strong>¥{{ cents(Number(selectedSettlement.net_cents || 0) + Number(selectedSettlement.adjustment_cents || 0)) }}</strong></el-descriptions-item>
        <el-descriptions-item label="状态" :span="2">{{ settlementStatusText(selectedSettlement.status) }}</el-descriptions-item>
        <el-descriptions-item v-if="selectedSettlement.dispute_reason" label="争议原因" :span="2">{{ selectedSettlement.dispute_reason }}</el-descriptions-item>
        <el-descriptions-item v-if="selectedSettlement.payment_proof" label="付款凭证" :span="2">{{ selectedSettlement.payment_proof }}</el-descriptions-item>
      </el-descriptions>
      <section v-if="selectedSettlement?.adjustments?.length" class="mt-5">
        <h3 class="mb-3 font-medium text-gray-900">调整记录</h3>
        <el-table :data="selectedSettlement.adjustments" size="small" border>
          <el-table-column prop="sequence" label="序号" width="70" />
          <el-table-column label="调整金额" width="130"><template #default="{ row }">{{ signedCents(row.amount_cents) }}</template></el-table-column>
          <el-table-column prop="actor_tenant_id" label="操作租户" width="110" />
          <el-table-column prop="reason" label="调整原因" min-width="240" />
          <el-table-column label="时间" width="180"><template #default="{ row }">{{ dateTime(row.created_at) }}</template></el-table-column>
        </el-table>
      </section>
      <template #footer>
        <el-button @click="settlementDialog = false">关闭</el-button>
        <el-button v-if="canDisputeSettlement" type="warning" plain :loading="settlementActionLoading" @click="disputeSettlement">提出争议</el-button>
        <el-button v-if="canAdjustSettlement" type="warning" :loading="settlementActionLoading" @click="openSettlementAdjustment">追加调整</el-button>
        <el-button v-if="canSupplierConfirmSettlement" type="primary" :loading="settlementActionLoading" @click="updateSettlementStatus('supplier_confirmed')">供应商确认</el-button>
        <el-button v-if="canTravelConfirmSettlement" type="success" :loading="settlementActionLoading" @click="updateSettlementStatus('confirmed')">旅行社确认</el-button>
        <el-button v-if="canMarkSettlementPaid" type="success" :loading="settlementActionLoading" @click="markSettlementPaid">登记付款</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="settlementAdjustmentDialog" title="追加结算调整" width="500px" append-to-body>
      <el-form label-position="top">
        <el-form-item label="调整金额（元）" required>
          <el-input-number v-model="settlementAdjustment.amount" :precision="2" :step="1" :controls="false" class="w-full" />
          <div class="mt-1 text-xs text-gray-500">正数增加应付，负数减少应付；原始结算金额不会被覆盖。</div>
        </el-form-item>
        <el-form-item label="调整原因" required><el-input v-model="settlementAdjustment.reason" type="textarea" :rows="3" maxlength="255" show-word-limit /></el-form-item>
      </el-form>
      <template #footer><el-button @click="settlementAdjustmentDialog = false">取消</el-button><el-button type="primary" :loading="settlementActionLoading" @click="submitSettlementAdjustment">追加并重新确认</el-button></template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { DocumentAdd, Plus, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'

const user = computed<any>(() => { try { return JSON.parse(localStorage.getItem('user') || '{}') } catch { return {} } })
const currentTenantID = computed(() => Number(user.value.tenant_id || 0))
const capabilities = computed(() => new Set((user.value.capabilities || []).filter((item: any) => item.status === 'active').map((item: any) => item.capability)))
const isTravelAgency = computed(() => capabilities.value.has('travel_agency'))

const activeTab = ref('groups')
const groups = ref<any[]>([])
const contracts = ref<any[]>([])
const settlements = ref<any[]>([])
const accounts = ref<any[]>([])
const loading = ref(false)
const contractsLoading = ref(false)
const settlementsLoading = ref(false)
const accountsLoading = ref(false)
const isGroupOwner = (row: any) => currentTenantID.value > 0 && Number(row?.tenant_id) === currentTenantID.value
const isGroupSupplier = (row: any) => currentTenantID.value > 0 && Number(row?.supplier_tenant_id) === currentTenantID.value && !isGroupOwner(row)

const cents = (value: number) => (Number(value || 0) / 100).toFixed(2)
const signedCents = (value: number) => `${Number(value || 0) > 0 ? '+' : Number(value || 0) < 0 ? '-' : ''}¥${cents(Math.abs(Number(value || 0)))}`
const dateOnly = (value: string) => value ? value.slice(0, 10) : '-'
const dateTime = (value: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-'
const groupStatusText = (status: string) => ({ draft: '草稿', confirmed: '待入园', partial_entry: '部分入园', entered: '已全部入园', cancelled: '已取消' } as Record<string, string>)[status] || status
const groupStatusType = (status: string) => status === 'entered' ? 'success' : status === 'partial_entry' ? 'warning' : status === 'cancelled' ? 'danger' : status === 'confirmed' ? 'primary' : 'info'
const memberStatusText = (status: string) => ({ planned: '待出票', ticketed: '可入园', entered: '已入园', cancelled: '已取消' } as Record<string, string>)[status] || status
const memberStatusType = (status: string) => status === 'entered' ? 'success' : status === 'ticketed' ? 'primary' : status === 'cancelled' ? 'danger' : 'info'
const settlementStatusText = (status: string) => ({ open: '未生成', statement: '已生成', settled: '已结清', draft: '待供应商确认', supplier_confirmed: '待旅行社确认', confirmed: '待付款', disputed: '有争议', paid: '已付款' } as Record<string, string>)[status] || status || '-'
const settlementStatusType = (status: string) => status === 'paid' ? 'success' : status === 'disputed' ? 'danger' : status === 'confirmed' ? 'warning' : status === 'supplier_confirmed' ? 'primary' : 'info'

const loadGroups = async () => {
  loading.value = true
  try { groups.value = (await request.get('/teams', { params: { page: 1, page_size: 100 } })).data.data || [] }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '团队加载失败') }
  finally { loading.value = false }
}
const loadContracts = async () => {
  contractsLoading.value = true
  try { contracts.value = (await request.get('/teams/contracts')).data.data || [] }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '合同加载失败') }
  finally { contractsLoading.value = false }
}
const loadSettlements = async () => {
  settlementsLoading.value = true
  try { settlements.value = (await request.get('/teams/settlements', { params: { page: 1, page_size: 100 } })).data.data || [] }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '结算单加载失败') }
  finally { settlementsLoading.value = false }
}
const loadAccounts = async () => {
  accountsLoading.value = true
  try { accounts.value = (await request.get('/teams/accounts')).data.data || [] }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '团队资金汇总加载失败') }
  finally { accountsLoading.value = false }
}
const handleTabChange = (tab: string) => { if (tab === 'contracts') loadContracts(); if (tab === 'settlements') loadSettlements(); if (tab === 'accounts') loadAccounts() }
const refreshActiveTab = () => activeTab.value === 'contracts' ? loadContracts() : activeTab.value === 'settlements' ? loadSettlements() : activeTab.value === 'accounts' ? loadAccounts() : loadGroups()

const saving = ref(false)
const groupDialog = ref(false)
const groupForm = reactive({ name: '', supplier_tenant_id: 0, scenic_area_id: 0, contract_id: 0, visit_date: '', expected_count: 1 })
const openGroupDialog = () => { Object.assign(groupForm, { name: '', supplier_tenant_id: 0, scenic_area_id: 0, contract_id: 0, visit_date: '', expected_count: 1 }); groupDialog.value = true }
const createGroup = async () => {
  if (!groupForm.name.trim() || !groupForm.supplier_tenant_id || !groupForm.scenic_area_id || !groupForm.contract_id || !groupForm.visit_date) { ElMessage.warning('团队名称、供应商、景区、合同和日期均必填'); return }
  saving.value = true
  try { await request.post('/teams', { ...groupForm }); groupDialog.value = false; ElMessage.success('团队已创建'); await loadGroups() }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '团队创建失败') }
  finally { saving.value = false }
}

const contractDialog = ref(false)
const savingContract = ref(false)
const contractCreditYuan = ref(0)
const contractForm = reactive({ supplier_tenant_id: 0, contract_no: '', settlement_days: 0, status: 'active' })
const openContractDialog = () => { Object.assign(contractForm, { supplier_tenant_id: 0, contract_no: '', settlement_days: 0, status: 'active' }); contractCreditYuan.value = 0; contractDialog.value = true }
const createContract = async () => {
  if (!contractForm.supplier_tenant_id || !contractForm.contract_no.trim()) { ElMessage.warning('供应商和合同号必填'); return }
  savingContract.value = true
  try { await request.post('/teams/contracts', { ...contractForm, credit_limit_cents: Math.round(contractCreditYuan.value * 100) }); contractDialog.value = false; ElMessage.success('合同已创建'); await loadContracts() }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '合同创建失败') }
  finally { savingContract.value = false }
}

const detailDialog = ref(false)
const detailLoading = ref(false)
const selectedGroup = ref<any>(null)
const members = ref<any[]>([])
const entryBatches = ref<any[]>([])
const confirmations = ref<any[]>([])
const memberChanges = ref<any[]>([])
const devices = ref<any[]>([])
const guides = ref<any[]>([])
const vehicles = ref<any[]>([])
const entryDeviceID = ref(0)
const entryMemberSelection = ref<any[]>([])
const entering = ref(false)
const pendingEntryRequest = ref({ fingerprint: '', key: '' })
const rosterText = ref('')
const savingRoster = ref(false)
const canEditRoster = computed(() => isGroupOwner(selectedGroup.value) && selectedGroup.value?.status === 'draft' && !selectedGroup.value?.sales_order_id)
const canChangeMembers = computed(() => isGroupOwner(selectedGroup.value) && ['confirmed', 'partial_entry'].includes(selectedGroup.value?.status))
const canSubmitConfirmation = computed(() => canChangeMembers.value)
const canEnterSelectedGroup = computed(() => isGroupSupplier(selectedGroup.value) && ['confirmed', 'partial_entry'].includes(selectedGroup.value?.status))
const admissionDevices = computed(() => devices.value.filter(device => device.status === 'online' && Number(device.scenic_area_id) === Number(selectedGroup.value?.scenic_area_id) && device.check_point_id))
const loadGroupDetail = async () => {
  if (!selectedGroup.value) return
  detailLoading.value = true
  try {
    const [memberResponse, batchResponse, confirmationResponse, changeResponse] = await Promise.all([
      request.get(`/teams/${selectedGroup.value.id}/members`),
      request.get(`/teams/${selectedGroup.value.id}/entry-batches`),
      request.get(`/teams/${selectedGroup.value.id}/confirmations`),
      request.get(`/teams/${selectedGroup.value.id}/member-changes`),
    ])
    members.value = memberResponse.data.data || []
    entryBatches.value = batchResponse.data.data || []
    confirmations.value = confirmationResponse.data.data || []
    memberChanges.value = changeResponse.data.data || []
    entryMemberSelection.value = []
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '团队履约信息加载失败') }
  finally { detailLoading.value = false }
}
const loadAdmissionDevices = async () => {
  try { devices.value = (await request.get('/devices', { params: { page: 1, page_size: 100 } })).data.data || [] }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '入园设备加载失败') }
}
const loadTravelResources = async () => {
  try {
    const [guideResponse, vehicleResponse] = await Promise.all([request.get('/teams/guides'), request.get('/teams/vehicles')])
    guides.value = (guideResponse.data.data || []).filter((row: any) => row.status === 'active')
    vehicles.value = (vehicleResponse.data.data || []).filter((row: any) => row.status === 'active')
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '导游和车辆加载失败') }
}
const openGroupDetail = async (row: any) => {
  selectedGroup.value = row
  rosterText.value = ''
  entryDeviceID.value = 0
  pendingEntryRequest.value = { fingerprint: '', key: '' }
  detailDialog.value = true
  await Promise.all([
    loadGroupDetail(),
    isGroupSupplier(row) ? loadAdmissionDevices() : Promise.resolve(),
    isGroupOwner(row) ? loadTravelResources() : Promise.resolve(),
  ])
}
const newRequestKey = () => typeof crypto !== 'undefined' && crypto.randomUUID ? crypto.randomUUID() : `entry-${Date.now()}-${Math.random().toString(36).slice(2)}`
const enterBatch = async () => {
  if (!selectedGroup.value || !entryDeviceID.value || !entryMemberSelection.value.length) return
  const memberIDs = entryMemberSelection.value.map(member => Number(member.id)).sort((a, b) => a - b)
  const fingerprint = `${entryDeviceID.value}:${memberIDs.join(',')}`
  if (pendingEntryRequest.value.fingerprint !== fingerprint) pendingEntryRequest.value = { fingerprint, key: newRequestKey() }
  entering.value = true
  try {
    const response = await request.post(`/teams/${selectedGroup.value.id}/enter-batch`, { device_id: entryDeviceID.value, member_ids: memberIDs, idempotency_key: pendingEntryRequest.value.key })
    ElMessage.success(`本批 ${response.data.entered_count} 人已入园`)
    pendingEntryRequest.value = { fingerprint: '', key: '' }
    await loadGroups()
    selectedGroup.value = groups.value.find(row => row.id === selectedGroup.value.id) || selectedGroup.value
    await loadGroupDetail()
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '团队入园失败') }
  finally { entering.value = false }
}
const parseRoster = () => {
  const rows: any[] = []
  const lines = rosterText.value.split(/\r?\n/).map(line => line.trim()).filter(Boolean)
  for (const [index, line] of lines.entries()) {
    const cells = line.split(/[\t,，]/).map(cell => cell.trim())
    if (index === 0 && /姓名|name/i.test(cells[0] || '')) continue
    if (!cells[0]) throw new Error(`第 ${index + 1} 行姓名为空`)
    rows.push({ name: cells[0], identity_no: cells[1] || '', phone: cells[2] || '' })
  }
  if (!rows.length) throw new Error('名单不能为空')
  return rows
}
const replaceRoster = async () => {
  if (!selectedGroup.value) return
  savingRoster.value = true
  try { const roster = parseRoster(); await request.put(`/teams/${selectedGroup.value.id}/members`, { members: roster }); ElMessage.success(`已替换 ${roster.length} 名成员`); rosterText.value = ''; await Promise.all([loadGroupDetail(), loadGroups()]) }
  catch (e: any) { ElMessage.error(e.response?.data?.error || e.message || '名单替换失败') }
  finally { savingRoster.value = false }
}

const confirmationDialog = ref(false)
const confirmationSaving = ref(false)
const confirmationForm = reactive({ confirmed_count: 1, guide_id: 0, vehicle_id: 0, notes: '' })
const openConfirmationDialog = () => {
  Object.assign(confirmationForm, {
    confirmed_count: Number(selectedGroup.value?.expected_count || 1),
    guide_id: Number(selectedGroup.value?.guide_id || 0),
    vehicle_id: Number(selectedGroup.value?.vehicle_id || 0),
    notes: '',
  })
  confirmationDialog.value = true
}
const submitConfirmation = async () => {
  if (!selectedGroup.value || confirmationForm.confirmed_count < 1) return
  confirmationSaving.value = true
  try {
    await request.post(`/teams/${selectedGroup.value.id}/confirmations`, { ...confirmationForm })
    confirmationDialog.value = false
    ElMessage.success('团队确认单新版本已提交')
    await loadGroupDetail()
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '确认单提交失败') }
  finally { confirmationSaving.value = false }
}
const acknowledgeConfirmation = async (row: any) => {
  if (!selectedGroup.value) return
  try {
    await request.post(`/teams/${selectedGroup.value.id}/confirmations/${row.id}/acknowledge`)
    ElMessage.success('已确认收到团队现场信息')
    await loadGroupDetail()
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '确认失败') }
}

const temporaryMemberDialog = ref(false)
const memberChangeSaving = ref(false)
const temporaryMemberForm = reactive({ name: '', identity_no: '', phone: '', reason: '' })
const openTemporaryMemberDialog = () => { Object.assign(temporaryMemberForm, { name: '', identity_no: '', phone: '', reason: '' }); temporaryMemberDialog.value = true }
const addTemporaryMember = async () => {
  if (!selectedGroup.value || !temporaryMemberForm.name.trim() || !temporaryMemberForm.reason.trim()) { ElMessage.warning('姓名和加员原因必填'); return }
  memberChangeSaving.value = true
  try {
    await request.post(`/teams/${selectedGroup.value.id}/member-changes`, {
      action: 'add', reason: temporaryMemberForm.reason.trim(),
      member: { name: temporaryMemberForm.name.trim(), identity_no: temporaryMemberForm.identity_no.trim(), phone: temporaryMemberForm.phone.trim() },
    })
    temporaryMemberDialog.value = false
    ElMessage.success('临时加员已记录，可入园状态以名单中的票权益为准')
    await Promise.all([loadGroups(), loadGroupDetail()])
    selectedGroup.value = groups.value.find(row => row.id === selectedGroup.value.id) || selectedGroup.value
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '临时加员失败') }
  finally { memberChangeSaving.value = false }
}
const removeTemporaryMember = async (row: any) => {
  if (!selectedGroup.value) return
  try {
    const result = await ElMessageBox.prompt('请输入临时减员原因。已绑定有效门票的游客需先完成退票或作废。', `临时减员：${row.name}`, { inputType: 'textarea', inputValidator: value => value.trim() ? true : '减员原因必填' })
    memberChangeSaving.value = true
    await request.post(`/teams/${selectedGroup.value.id}/member-changes`, { action: 'remove', member_id: row.id, reason: result.value.trim() })
    ElMessage.success('临时减员已记录')
    await Promise.all([loadGroups(), loadGroupDetail()])
    selectedGroup.value = groups.value.find(group => group.id === selectedGroup.value.id) || selectedGroup.value
  } catch (action: any) {
    if (action !== 'cancel' && action !== 'close') ElMessage.error(action.response?.data?.error || '临时减员失败')
  } finally { memberChangeSaving.value = false }
}

const attachOrderDialog = ref(false)
const attachOrderId = ref(0)
const attachingOrder = ref(false)
const openAttachOrder = (row: any) => { selectedGroup.value = row; attachOrderId.value = 0; attachOrderDialog.value = true }
const attachOrder = async () => {
  if (!selectedGroup.value || !attachOrderId.value) { ElMessage.warning('请输入订单 ID'); return }
  attachingOrder.value = true
  try { await request.post(`/teams/${selectedGroup.value.id}/attach-order`, { order_id: attachOrderId.value }); attachOrderDialog.value = false; ElMessage.success('订单已绑定，成员票权益已匹配'); await loadGroups() }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '订单绑定失败') }
  finally { attachingOrder.value = false }
}

const canGenerateSettlement = (row: any) => isGroupOwner(row) && row.sales_order_id && row.settlement_status === 'open' && row.status !== 'cancelled'
const generateSettlement = async (row: any) => {
  try { await request.post(`/teams/${row.id}/settlement`); ElMessage.success('团队结算单已生成'); activeTab.value = 'settlements'; await Promise.all([loadGroups(), loadSettlements()]) }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '团队结算单生成失败') }
}
const settlementDialog = ref(false)
const selectedSettlement = ref<any>(null)
const settlementActionLoading = ref(false)
const openSettlement = (row: any) => { selectedSettlement.value = row; settlementDialog.value = true }
const isSettlementSupplier = computed(() => Number(selectedSettlement.value?.supplier_tenant_id) === currentTenantID.value)
const isSettlementTravel = computed(() => Number(selectedSettlement.value?.travel_tenant_id) === currentTenantID.value)
const canSupplierConfirmSettlement = computed(() => isSettlementSupplier.value && selectedSettlement.value?.status === 'draft')
const canTravelConfirmSettlement = computed(() => isSettlementTravel.value && selectedSettlement.value?.status === 'supplier_confirmed')
const canDisputeSettlement = computed(() => (isSettlementSupplier.value || isSettlementTravel.value) && ['supplier_confirmed', 'confirmed'].includes(selectedSettlement.value?.status))
const canAdjustSettlement = computed(() => (isSettlementSupplier.value || isSettlementTravel.value) && selectedSettlement.value?.status === 'disputed')
const canMarkSettlementPaid = computed(() => isSettlementTravel.value && selectedSettlement.value?.status === 'confirmed')
const settlementAdjustmentDialog = ref(false)
const settlementAdjustment = reactive({ amount: 0, reason: '' })
const updateSettlementStatus = async (status: string, detail = '') => {
  if (!selectedSettlement.value) return
  settlementActionLoading.value = true
  try {
    await request.patch(`/teams/settlements/${selectedSettlement.value.id}/status`, { status, detail })
    ElMessage.success('结算状态已更新')
    await Promise.all([loadSettlements(), loadGroups()])
    selectedSettlement.value = settlements.value.find(row => row.id === selectedSettlement.value.id) || selectedSettlement.value
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '结算状态更新失败') }
  finally { settlementActionLoading.value = false }
}
const disputeSettlement = async () => {
  try { const result = await ElMessageBox.prompt('请输入具体差异或争议原因', '提出结算争议', { inputType: 'textarea', inputValidator: value => value.trim() ? true : '争议原因必填' }); await updateSettlementStatus('disputed', result.value.trim()) }
  catch (action: any) { if (action !== 'cancel' && action !== 'close') throw action }
}
const openSettlementAdjustment = () => { settlementAdjustment.amount = 0; settlementAdjustment.reason = ''; settlementAdjustmentDialog.value = true }
const submitSettlementAdjustment = async () => {
  if (!selectedSettlement.value || !settlementAdjustment.amount || !settlementAdjustment.reason.trim()) { ElMessage.warning('调整金额不能为 0，且必须填写原因'); return }
  settlementActionLoading.value = true
  try {
    await request.post(`/teams/settlements/${selectedSettlement.value.id}/adjustments`, { amount_cents: Math.round(settlementAdjustment.amount * 100), reason: settlementAdjustment.reason.trim() })
    settlementAdjustmentDialog.value = false
    ElMessage.success('调整已追加，结算单已回到双方重新确认流程')
    await loadSettlements()
    selectedSettlement.value = settlements.value.find(row => row.id === selectedSettlement.value.id) || selectedSettlement.value
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '结算调整失败') }
  finally { settlementActionLoading.value = false }
}
const markSettlementPaid = async () => {
  try { const result = await ElMessageBox.prompt('填写银行流水号、转账单号或付款凭证位置', '登记付款', { inputValidator: value => value.trim() ? true : '付款凭证必填' }); await updateSettlementStatus('paid', result.value.trim()) }
  catch (action: any) { if (action !== 'cancel' && action !== 'close') throw action }
}

onMounted(loadGroups)
</script>
