<template>
  <section class="space-y-5">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-xl font-semibold text-gray-900">团队业务</h2>
        <p class="mt-1 text-sm text-gray-500">旅行社维护团队计划，景区供应商完成名单核对、分批入园和双方结算。</p>
      </div>
      <el-button :icon="Refresh" circle title="刷新当前页面" @click="refreshActiveTab" />
    </div>

    <div class="flex flex-wrap gap-2">
      <el-button v-if="isTravelAgency && can('teams.write')" type="primary" :icon="Plus" @click="openGroupDialog">新建团队</el-button>
      <el-button v-if="isSupplier && can('teams.write')" type="primary" :icon="DocumentAdd" @click="openContractDialog()">新增旅行社合同</el-button>
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
          <el-table-column label="操作" width="360" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="openGroupDetail(row)">{{ isGroupSupplier(row) ? '履约入园' : '名单详情' }}</el-button>
              <el-button v-if="isGroupOwner(row) && can('teams.write')" link type="success" :disabled="row.status !== 'draft'" @click="openContractOrder(row)">生成订单</el-button>
              <el-button v-if="isGroupOwner(row) && can('teams.write')" link type="primary" :disabled="row.status !== 'draft'" @click="openAttachOrder(row)">绑定已有订单</el-button>
              <el-button v-if="canGenerateSettlement(row)" link type="success" @click="generateSettlement(row)">生成结算单</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="旅行社合同" name="contracts">
        <el-table :data="contracts" v-loading="contractsLoading" stripe empty-text="暂无团队合同">
          <el-table-column prop="contract_no" label="合同号" min-width="180" />
          <el-table-column prop="travel_tenant_name" label="旅行社" min-width="150" />
          <el-table-column prop="supplier_tenant_name" label="景区供应商" min-width="150" />
          <el-table-column label="产品结算价" min-width="220">
            <template #default="{ row }">
              <div v-for="rule in row.price_rules || []" :key="rule.product_id" class="leading-6">
                <span>{{ rule.product_name }}</span><strong class="ml-2">¥{{ cents(rule.price_cents) }}</strong>
                <span v-if="rule.max_quantity" class="ml-2 text-xs text-gray-400">最多 {{ rule.max_quantity }} 张/单</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="授信额度" width="130"><template #default="{ row }">¥{{ cents(row.credit_limit_cents) }}</template></el-table-column>
          <el-table-column label="账期" width="100"><template #default="{ row }">{{ row.settlement_days }} 天</template></el-table-column>
          <el-table-column label="状态" width="110"><template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'warning'">{{ row.status === 'active' ? '有效' : '已暂停' }}</el-tag></template></el-table-column>
          <el-table-column v-if="isSupplier && can('teams.write')" label="操作" width="90" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openContractDialog(row)">编辑</el-button></template></el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="双方结算" name="settlements">
        <el-table :data="settlements" v-loading="settlementsLoading" stripe empty-text="暂无团队结算单">
          <el-table-column prop="statement_no" label="结算单" min-width="200" />
          <el-table-column label="类型" width="110"><template #default="{ row }">{{ settlementKindText(row.kind) }}</template></el-table-column>
          <el-table-column label="团队" width="100"><template #default="{ row }">#{{ row.group_id }}</template></el-table-column>
          <el-table-column label="旅行社" width="110"><template #default="{ row }">租户 {{ row.travel_tenant_id }}</template></el-table-column>
          <el-table-column label="供应商" width="110"><template #default="{ row }">租户 {{ row.supplier_tenant_id }}</template></el-table-column>
          <el-table-column label="总额" width="120"><template #default="{ row }">¥{{ cents(row.gross_cents) }}</template></el-table-column>
          <el-table-column label="退款" width="120"><template #default="{ row }">¥{{ cents(row.refund_cents) }}</template></el-table-column>
          <el-table-column label="应付/冲减" width="130"><template #default="{ row }"><strong>{{ signedCents(Number(row.net_cents || 0) + Number(row.adjustment_cents || 0)) }}</strong></template></el-table-column>
          <el-table-column label="状态" width="130"><template #default="{ row }"><el-tag :type="settlementStatusType(row.status)">{{ teamSettlementStatusText(row) }}</el-tag></template></el-table-column>
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
          <el-form-item label="合作合同" required>
            <el-select v-model="groupForm.contract_id" filterable class="w-full" placeholder="选择景区供应商合同" @change="applyGroupContract">
              <el-option v-for="contract in activeTravelContracts" :key="contract.id" :label="`${contract.supplier_tenant_name} · ${contract.contract_no}`" :value="contract.id" />
            </el-select>
          </el-form-item>
          <el-form-item label="入园景区" required>
            <el-select v-model="groupForm.scenic_area_id" class="w-full" placeholder="选择合同产品所属景区">
              <el-option v-for="area in groupScenicOptions" :key="area.id" :label="area.name" :value="area.id" />
            </el-select>
          </el-form-item>
          <el-form-item label="计划人数" required><el-input-number v-model="groupForm.expected_count" :min="1" class="w-full" /></el-form-item>
		  <el-form-item label="已付预款（元）"><el-input-number v-model="groupForm.deposit_yuan" :min="0" :precision="2" :step="100" class="w-full" /></el-form-item>
        </div>
        <el-form-item label="游玩日期" required><el-date-picker v-model="groupForm.visit_date" type="date" value-format="YYYY-MM-DD" class="w-full" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="groupDialog = false">取消</el-button><el-button type="primary" :loading="saving" @click="createGroup">创建</el-button></template>
    </el-dialog>

    <el-dialog v-model="contractDialog" :title="contractForm.id ? '编辑旅行社合同' : '新增旅行社合同'" width="760px" :close-on-click-modal="false">
      <el-form :model="contractForm" label-position="top">
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="旅行社" required>
            <el-select v-model="contractForm.travel_tenant_id" :disabled="Boolean(contractForm.id)" filterable class="w-full" placeholder="选择已合作旅行社">
              <el-option v-for="partner in contractPartners" :key="partner.tenant_id" :label="`${partner.name}（${partner.system_code}）`" :value="partner.tenant_id" />
            </el-select>
          </el-form-item>
          <el-form-item label="合同号" required><el-input v-model="contractForm.contract_no" :disabled="Boolean(contractForm.id)" maxlength="100" /></el-form-item>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="账期（天）"><el-input-number v-model="contractForm.settlement_days" :min="0" class="w-full" /></el-form-item>
          <el-form-item label="授信额度（元）"><el-input-number v-model="contractCreditYuan" :min="0" :precision="2" class="w-full" /></el-form-item>
        </div>
        <el-form-item label="合同状态"><el-radio-group v-model="contractForm.status"><el-radio-button label="active">有效</el-radio-button><el-radio-button label="suspended">暂停</el-radio-button></el-radio-group></el-form-item>
        <div class="mb-2 flex items-center justify-between">
          <div><div class="font-medium text-gray-900">产品结算价</div><div class="text-xs text-gray-500">保存后同时作为该旅行社的供货结算价；已产生订单仍保留原价格快照。</div></div>
          <el-button size="small" :icon="Plus" @click="addContractPriceRule">添加产品</el-button>
        </div>
        <div v-for="(rule, index) in contractPriceRules" :key="index" class="mb-3 grid grid-cols-[1fr_150px_150px_40px] items-end gap-3 rounded border border-gray-200 p-3">
          <el-form-item label="产品" class="mb-0"><el-select v-model="rule.product_id" filterable class="w-full" placeholder="选择产品"><el-option v-for="product in contractProducts" :key="product.id" :label="product.name" :value="product.id" /></el-select></el-form-item>
          <el-form-item label="结算价（元）" class="mb-0"><el-input-number v-model="rule.price_yuan" :min="0.01" :precision="2" :controls="false" class="w-full" /></el-form-item>
          <el-form-item label="每单上限（0不限）" class="mb-0"><el-input-number v-model="rule.max_quantity" :min="0" :precision="0" class="w-full" /></el-form-item>
          <el-button :icon="Delete" circle title="删除这项产品价格" @click="contractPriceRules.splice(index, 1)" />
        </div>
      </el-form>
      <template #footer><el-button @click="contractDialog = false">取消</el-button><el-button type="primary" :loading="savingContract" @click="saveContract">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="detailDialog" :title="`团队履约：${selectedGroup?.name || ''}`" width="1040px" :close-on-click-modal="false">
      <div v-if="selectedGroup" class="space-y-5">
        <el-descriptions :column="4" border>
          <el-descriptions-item label="团号">{{ selectedGroup.group_no }}</el-descriptions-item>
          <el-descriptions-item label="游玩日期">{{ dateOnly(selectedGroup.visit_date) }}</el-descriptions-item>
          <el-descriptions-item label="计划人数">{{ selectedGroup.expected_count }} 人</el-descriptions-item>
          <el-descriptions-item label="状态">{{ groupStatusText(selectedGroup.status) }}</el-descriptions-item>
        </el-descriptions>

        <div v-if="isGroupOwner(selectedGroup) && selectedGroup.sales_order_no" class="flex items-center justify-between rounded border border-gray-200 bg-gray-50 px-4 py-3">
          <div>
            <div class="text-sm font-medium text-gray-900">关联销售订单 {{ selectedGroup.sales_order_no }}</div>
            <div class="text-xs text-gray-500">退票、改期、换票、作废和补打统一在现有售后工作台处理。</div>
          </div>
          <el-button type="primary" plain @click="openTeamAfterSales">查看订单售后</el-button>
        </div>

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
                <el-button v-else-if="isGroupSupplier(selectedGroup) && can('teams.write')" link type="primary" @click="acknowledgeConfirmation(row)">确认收到</el-button>
                <el-tag v-else type="warning" effect="plain">待确认</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="80" fixed="right">
              <template #default="{ row }"><el-button link type="primary" :icon="Printer" @click="printConfirmation(row)">打印</el-button></template>
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
            <el-table-column prop="device_id" label="设备编号" width="100" />
            <el-table-column prop="operator_id" label="操作员编号" width="110" />
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
            <el-form-item label="替换名单（每行：姓名、证件号、手机号；支持逗号或制表符分隔）">
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

    <el-dialog v-model="attachOrderDialog" title="绑定已支付团队订单" width="560px">
      <el-form label-position="top">
        <el-form-item label="选择订单" required>
          <el-select v-model="attachOrderId" filterable class="w-full" placeholder="按订单号、联系人或手机号搜索" :loading="attachOrdersLoading">
            <el-option v-for="order in attachOrderCandidates" :key="order.id" :value="order.id" :label="`${order.order_no} · ${order.contact_name || order.contact_phone || '未填写联系人'} · ¥${Number(order.total_amount || 0).toFixed(2)}`" />
          </el-select>
          <div class="mt-1 text-xs text-gray-500">仅显示游玩日期、供应商和景区与当前团队一致的可绑定订单。</div>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="attachOrderDialog = false">取消</el-button><el-button type="primary" :loading="attachingOrder" @click="attachOrder">绑定</el-button></template>
    </el-dialog>

    <el-dialog v-model="contractOrderDialog" title="按合同生成团队订单" width="560px">
      <el-alert title="系统按当前游客名单逐人出票，并直接记入团队合同预付款或授信；价格、景区和供应商均从合同读取。" type="info" show-icon :closable="false" class="mb-4" />
      <el-form label-position="top">
        <el-form-item label="合同产品" required>
          <el-select v-model="contractOrderForm.product_id" class="w-full" placeholder="选择本团队景区的合同产品">
            <el-option v-for="rule in contractOrderProducts" :key="rule.product_id" :value="rule.product_id" :label="`${rule.product_name} · ¥${cents(rule.price_cents)}/人`" />
          </el-select>
        </el-form-item>
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="联系人"><el-input v-model="contractOrderForm.contact_name" maxlength="50" /></el-form-item>
          <el-form-item label="联系电话"><el-input v-model="contractOrderForm.contact_phone" maxlength="20" /></el-form-item>
        </div>
        <div class="text-sm text-gray-500">出票人数取当前团队的完整游客名单，不允许手工改数量。</div>
      </el-form>
      <template #footer><el-button @click="contractOrderDialog = false">取消</el-button><el-button type="primary" :loading="creatingContractOrder" @click="createContractOrder">确认生成并出票</el-button></template>
    </el-dialog>

    <el-dialog v-model="settlementDialog" title="团队结算处理" width="720px" :close-on-click-modal="false">
      <el-descriptions v-if="selectedSettlement" :column="2" border>
        <el-descriptions-item label="结算单" :span="2">{{ selectedSettlement.statement_no }}</el-descriptions-item>
        <el-descriptions-item label="单据类型" :span="2">{{ settlementKindText(selectedSettlement.kind) }}</el-descriptions-item>
        <el-descriptions-item label="履约总额">¥{{ cents(selectedSettlement.gross_cents) }}</el-descriptions-item>
        <el-descriptions-item label="退款冲减">¥{{ cents(selectedSettlement.refund_cents) }}</el-descriptions-item>
        <el-descriptions-item label="已付预款">¥{{ cents(selectedSettlement.deposit_cents) }}</el-descriptions-item>
        <el-descriptions-item label="争议调整">{{ signedCents(selectedSettlement.adjustment_cents) }}</el-descriptions-item>
        <el-descriptions-item label="最终应付/冲减"><strong>{{ signedCents(Number(selectedSettlement.net_cents || 0) + Number(selectedSettlement.adjustment_cents || 0)) }}</strong></el-descriptions-item>
        <el-descriptions-item label="状态" :span="2">{{ teamSettlementStatusText(selectedSettlement) }}</el-descriptions-item>
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
        <el-button :icon="Download" :loading="settlementExporting" @click="downloadTeamSettlement">导出对账单</el-button>
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
import { useRouter } from 'vue-router'
import { Delete, DocumentAdd, Download, Plus, Printer, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'
import { hasPermission } from '@/utils/permissions'

const router = useRouter()

const user = computed<any>(() => { try { return JSON.parse(localStorage.getItem('user') || '{}') } catch { return {} } })
const currentTenantID = computed(() => Number(user.value.tenant_id || 0))
const can = (permission: string) => hasPermission(user.value, permission)
const capabilities = computed(() => new Set((user.value.capabilities || []).filter((item: any) => item.status === 'active').map((item: any) => item.capability)))
const isTravelAgency = computed(() => capabilities.value.has('travel_agency'))
const isSupplier = computed(() => capabilities.value.has('supplier'))

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
const activeTravelContracts = computed(() => contracts.value.filter((contract: any) => Number(contract.travel_tenant_id) === currentTenantID.value && contract.status === 'active'))

const cents = (value: number) => (Number(value || 0) / 100).toFixed(2)
const signedCents = (value: number) => `${Number(value || 0) > 0 ? '+' : Number(value || 0) < 0 ? '-' : ''}¥${cents(Math.abs(Number(value || 0)))}`
const dateOnly = (value: string) => value ? value.slice(0, 10) : '-'
const dateTime = (value: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-'
const groupStatusText = (status: string) => ({ draft: '草稿', confirmed: '待入园', partial_entry: '部分入园', entered: '已全部入园', cancelled: '已取消' } as Record<string, string>)[status] || '未知状态'
const groupStatusType = (status: string) => status === 'entered' ? 'success' : status === 'partial_entry' ? 'warning' : status === 'cancelled' ? 'danger' : status === 'confirmed' ? 'primary' : 'info'
const memberStatusText = (status: string) => ({ planned: '待出票', ticketed: '可入园', entered: '已入园', cancelled: '已取消' } as Record<string, string>)[status] || '未知状态'
const memberStatusType = (status: string) => status === 'entered' ? 'success' : status === 'ticketed' ? 'primary' : status === 'cancelled' ? 'danger' : 'info'
const settlementStatusText = (status: string) => ({ open: '未生成', statement: '已生成', settled: '已结清', draft: '待供应商确认', supplier_confirmed: '待旅行社确认', confirmed: '待付款', disputed: '有争议', paid: '已付款' } as Record<string, string>)[status] || '未知状态'
const settlementKindText = (kind: string) => kind === 'refund_correction' ? '退款冲减' : '团队结算'
const teamSettlementStatusText = (row: any) => row?.kind === 'refund_correction' && row?.status === 'paid' ? '已完成冲减' : settlementStatusText(row?.status)
const settlementStatusType = (status: string) => status === 'paid' ? 'success' : status === 'disputed' ? 'danger' : status === 'confirmed' ? 'warning' : status === 'supplier_confirmed' ? 'primary' : 'info'

const openTeamAfterSales = () => {
  if (!selectedGroup.value?.sales_order_no) return
  detailDialog.value = false
  router.push({ name: 'after-sales', query: { order_no: selectedGroup.value.sales_order_no } })
}

const printConfirmation = (row: any) => {
  if (!selectedGroup.value) return
  const printWindow = window.open('', '_blank', 'width=760,height=900')
  if (!printWindow) { ElMessage.warning('浏览器阻止了打印窗口，请允许本页面打开新窗口'); return }
  const document = printWindow.document
  document.title = `${selectedGroup.value.group_no}-确认单-v${row.sequence}`
  const style = document.createElement('style')
  style.textContent = 'body{font-family:Arial,"Microsoft YaHei",sans-serif;color:#111;padding:36px;line-height:1.6}h1{text-align:center;font-size:24px;margin:0 0 28px}table{width:100%;border-collapse:collapse}th,td{border:1px solid #555;padding:10px;text-align:left;vertical-align:top}th{width:22%;background:#f4f4f4}.note{margin-top:18px;color:#555;font-size:12px}@media print{body{padding:0}.note{display:none}}'
  document.head.appendChild(style)
  const title = document.createElement('h1')
  title.textContent = '团队接待确认单'
  document.body.appendChild(title)
  const table = document.createElement('table')
  const values = [
    ['团队', `${selectedGroup.value.name}（${selectedGroup.value.group_no}）`],
    ['游玩日期', dateOnly(selectedGroup.value.visit_date)],
    ['确认版本', `第 ${row.sequence} 版`],
    ['确认人数', `${row.confirmed_count} 人`],
    ['导游', `${row.guide_name || '-'}${row.guide_phone ? ` / ${row.guide_phone}` : ''}`],
    ['车辆', row.plate_number || '-'],
    ['现场说明', row.notes || '-'],
    ['提交时间', dateTime(row.submitted_at)],
    ['供应商确认', row.supplier_acknowledged_at ? `已确认（${dateTime(row.supplier_acknowledged_at)}）` : '待确认'],
  ]
  values.forEach(([label, value]) => {
    const tr = document.createElement('tr'); const th = document.createElement('th'); const td = document.createElement('td')
    th.textContent = label; td.textContent = value; tr.append(th, td); table.appendChild(tr)
  })
  document.body.appendChild(table)
  const note = document.createElement('p')
  note.className = 'note'; note.textContent = '本确认单按该次提交时的确认信息生成，不代表游客名单的历史快照。'
  document.body.appendChild(note)
  document.close()
  printWindow.focus()
  window.setTimeout(() => printWindow.print(), 150)
}

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
const groupForm = reactive({ name: '', supplier_tenant_id: 0, scenic_area_id: 0, contract_id: 0, visit_date: '', expected_count: 1, deposit_yuan: 0 })
const selectedGroupContract = computed(() => contracts.value.find((contract: any) => Number(contract.id) === Number(groupForm.contract_id)))
const groupScenicOptions = computed(() => {
  const seen = new Set<number>()
  return (selectedGroupContract.value?.price_rules || []).flatMap((rule: any) => {
    const id = Number(rule.scenic_area_id || 0)
    if (!id || seen.has(id)) return []
    seen.add(id)
    return [{ id, name: rule.scenic_area_name || `景区 ${id}` }]
  })
})
const applyGroupContract = () => {
  groupForm.supplier_tenant_id = Number(selectedGroupContract.value?.supplier_tenant_id || 0)
  groupForm.scenic_area_id = groupScenicOptions.value[0]?.id || 0
}
const openGroupDialog = async () => {
  if (!contracts.value.length) await loadContracts()
  Object.assign(groupForm, { name: '', supplier_tenant_id: 0, scenic_area_id: 0, contract_id: 0, visit_date: '', expected_count: 1, deposit_yuan: 0 })
  groupDialog.value = true
}
const createGroup = async () => {
  if (!groupForm.name.trim() || !groupForm.supplier_tenant_id || !groupForm.scenic_area_id || !groupForm.contract_id || !groupForm.visit_date) { ElMessage.warning('团队名称、供应商、景区、合同和日期均必填'); return }
  saving.value = true
  try { await request.post('/teams', { ...groupForm, deposit_cents: Math.round(groupForm.deposit_yuan * 100) }); groupDialog.value = false; ElMessage.success('团队已创建'); await loadGroups() }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '团队创建失败') }
  finally { saving.value = false }
}

const contractDialog = ref(false)
const savingContract = ref(false)
const contractCreditYuan = ref(0)
const contractPartners = ref<any[]>([])
const contractProducts = ref<any[]>([])
const contractPriceRules = ref<any[]>([])
const contractForm = reactive({ id: 0, travel_tenant_id: 0, contract_no: '', settlement_days: 0, status: 'active' })
const loadContractFormOptions = async () => {
  const [partnersResponse, productsResponse] = await Promise.all([
    request.get('/teams/contract-partners'),
    request.get('/products', { params: { page: 1, page_size: 100 } }),
  ])
  contractPartners.value = partnersResponse.data.data || []
  contractProducts.value = (productsResponse.data.data || []).filter((product: any) => product.status === 'online' && product.is_distributable)
}
const addContractPriceRule = () => contractPriceRules.value.push({ product_id: 0, price_yuan: 0, max_quantity: 0 })
const openContractDialog = async (row?: any) => {
  try {
    await loadContractFormOptions()
    Object.assign(contractForm, {
      id: Number(row?.id || 0), travel_tenant_id: Number(row?.travel_tenant_id || contractPartners.value[0]?.tenant_id || 0),
      contract_no: row?.contract_no || '', settlement_days: Number(row?.settlement_days || 0), status: row?.status || 'active',
    })
    contractCreditYuan.value = Number(row?.credit_limit_cents || 0) / 100
    contractPriceRules.value = (row?.price_rules || []).map((rule: any) => ({ product_id: rule.product_id, price_yuan: Number(rule.price_cents || 0) / 100, max_quantity: Number(rule.max_quantity || 0) }))
    if (!contractPriceRules.value.length) addContractPriceRule()
    contractDialog.value = true
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '合同可选项加载失败') }
}
const saveContract = async () => {
  if (!contractForm.travel_tenant_id || !contractForm.contract_no.trim() || !contractPriceRules.value.length || contractPriceRules.value.some(rule => !rule.product_id || rule.price_yuan <= 0)) { ElMessage.warning('请选择旅行社，并完整填写至少一个产品结算价'); return }
  if (new Set(contractPriceRules.value.map(rule => rule.product_id)).size !== contractPriceRules.value.length) { ElMessage.warning('同一个产品不能重复添加'); return }
  savingContract.value = true
  try {
    const payload = {
      travel_tenant_id: contractForm.travel_tenant_id, contract_no: contractForm.contract_no.trim(), status: contractForm.status,
      settlement_days: contractForm.settlement_days, credit_limit_cents: Math.round(contractCreditYuan.value * 100),
      price_rules: contractPriceRules.value.map(rule => ({ product_id: rule.product_id, price_cents: Math.round(rule.price_yuan * 100), max_quantity: rule.max_quantity })),
    }
    if (contractForm.id) await request.put(`/teams/contracts/${contractForm.id}`, payload)
    else await request.post('/teams/contracts', payload)
    contractDialog.value = false
    ElMessage.success(contractForm.id ? '合同价格已更新' : '旅行社合同已创建')
    await loadContracts()
  }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '合同保存失败') }
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
const canEditRoster = computed(() => can('teams.write') && isGroupOwner(selectedGroup.value) && selectedGroup.value?.status === 'draft' && !selectedGroup.value?.sales_order_id)
const canChangeMembers = computed(() => can('teams.write') && isGroupOwner(selectedGroup.value) && ['confirmed', 'partial_entry'].includes(selectedGroup.value?.status))
const canSubmitConfirmation = computed(() => canChangeMembers.value)
const canEnterSelectedGroup = computed(() => can('teams.write') && isGroupSupplier(selectedGroup.value) && ['confirmed', 'partial_entry'].includes(selectedGroup.value?.status))
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
const attachOrdersLoading = ref(false)
const attachOrders = ref<any[]>([])
const attachOrderCandidates = computed(() => {
  if (!selectedGroup.value) return []
  const visitDate = dateOnly(selectedGroup.value.visit_date)
  return attachOrders.value.filter((order: any) => ['paid', 'completed', 'partial_refunded'].includes(order.status) &&
    (order.items || []).some((item: any) => Number(item.fulfillment_tenant_id) === Number(selectedGroup.value.supplier_tenant_id) &&
      Number(item.fulfillment_scenic_area_id) === Number(selectedGroup.value.scenic_area_id) && dateOnly(item.use_date) === visitDate))
})
const openAttachOrder = async (row: any) => {
  selectedGroup.value = row
  attachOrderId.value = 0
  attachOrderDialog.value = true
  attachOrdersLoading.value = true
  try { attachOrders.value = (await request.get('/orders', { params: { page: 1, page_size: 100 } })).data.data || [] }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '可绑定订单加载失败') }
  finally { attachOrdersLoading.value = false }
}
const attachOrder = async () => {
  if (!selectedGroup.value || !attachOrderId.value) { ElMessage.warning('请选择要绑定的订单'); return }
  attachingOrder.value = true
  try { await request.post(`/teams/${selectedGroup.value.id}/attach-order`, { order_id: attachOrderId.value }); attachOrderDialog.value = false; ElMessage.success('订单已绑定，成员票权益已匹配'); await loadGroups() }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '订单绑定失败') }
  finally { attachingOrder.value = false }
}

const contractOrderDialog = ref(false)
const creatingContractOrder = ref(false)
const contractOrderForm = reactive({ product_id: 0, contact_name: '', contact_phone: '' })
const selectedOrderContract = computed(() => contracts.value.find((contract: any) => Number(contract.id) === Number(selectedGroup.value?.contract_id)))
const contractOrderProducts = computed(() => (selectedOrderContract.value?.price_rules || []).filter((rule: any) => Number(rule.scenic_area_id) === Number(selectedGroup.value?.scenic_area_id)))
const openContractOrder = async (row: any) => {
  selectedGroup.value = row
  if (!contracts.value.length) await loadContracts()
  Object.assign(contractOrderForm, { product_id: contractOrderProducts.value[0]?.product_id || 0, contact_name: '', contact_phone: '' })
  contractOrderDialog.value = true
}
const createContractOrder = async () => {
  if (!selectedGroup.value || !contractOrderForm.product_id) { ElMessage.warning('请选择合同产品'); return }
  creatingContractOrder.value = true
  try {
    const response = await request.post(`/teams/${selectedGroup.value.id}/contract-order`, contractOrderForm)
    contractOrderDialog.value = false
    ElMessage.success(`团队订单 ${response.data.order_no} 已生成并出票`)
    await loadGroups()
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '团队订单生成失败') }
  finally { creatingContractOrder.value = false }
}

const canGenerateSettlement = (row: any) => can('settlements.write') && isGroupOwner(row) && row.sales_order_id && row.settlement_status === 'open' && row.status !== 'cancelled'
const generateSettlement = async (row: any) => {
  try { await request.post(`/teams/${row.id}/settlement`); ElMessage.success('团队结算单已生成'); activeTab.value = 'settlements'; await Promise.all([loadGroups(), loadSettlements()]) }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '团队结算单生成失败') }
}
const settlementDialog = ref(false)
const selectedSettlement = ref<any>(null)
const settlementActionLoading = ref(false)
const settlementExporting = ref(false)
const openSettlement = (row: any) => { selectedSettlement.value = row; settlementDialog.value = true }
const downloadTeamSettlement = async () => {
  if (!selectedSettlement.value) return
  settlementExporting.value = true
  try {
    const response = await request.get(`/teams/settlements/${selectedSettlement.value.id}/export`, { responseType: 'blob' })
    const url = URL.createObjectURL(response.data)
    const link = document.createElement('a')
    link.href = url
    link.download = `${selectedSettlement.value.statement_no}.csv`
    document.body.appendChild(link)
    link.click()
    link.remove()
    URL.revokeObjectURL(url)
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '团队对账单导出失败') }
  finally { settlementExporting.value = false }
}
const isSettlementSupplier = computed(() => Number(selectedSettlement.value?.supplier_tenant_id) === currentTenantID.value)
const isSettlementTravel = computed(() => Number(selectedSettlement.value?.travel_tenant_id) === currentTenantID.value)
const canSupplierConfirmSettlement = computed(() => can('settlements.write') && isSettlementSupplier.value && selectedSettlement.value?.status === 'draft')
const canTravelConfirmSettlement = computed(() => can('settlements.write') && isSettlementTravel.value && selectedSettlement.value?.status === 'supplier_confirmed')
const canDisputeSettlement = computed(() => can('settlements.write') && (isSettlementSupplier.value || isSettlementTravel.value) && ['supplier_confirmed', 'confirmed'].includes(selectedSettlement.value?.status))
const canAdjustSettlement = computed(() => can('settlements.write') && (isSettlementSupplier.value || isSettlementTravel.value) && selectedSettlement.value?.status === 'disputed')
const canMarkSettlementPaid = computed(() => can('settlements.write') && isSettlementTravel.value && selectedSettlement.value?.status === 'confirmed')
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
