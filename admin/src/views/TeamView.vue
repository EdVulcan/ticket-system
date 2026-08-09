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
      <el-button v-if="isSupplier && can('teams.contracts.write')" type="primary" :icon="DocumentAdd" @click="openContractDialog()">新增旅行社合同</el-button>
    </div>

    <el-tabs v-model="activeTab" @tab-change="handleTabChange">
      <el-tab-pane label="团队计划" name="groups">
        <div class="mb-4 flex flex-wrap items-center gap-2">
          <el-input
            v-model="groupQuery.keyword"
            clearable
            class="w-64"
            placeholder="搜索团号、团队名称或订单号"
            :prefix-icon="Search"
            @keyup.enter="applyGroupFilters"
          />
          <el-select v-model="groupQuery.status" clearable class="w-36" placeholder="全部状态">
            <el-option label="草稿" value="draft" />
            <el-option label="待入园" value="confirmed" />
            <el-option label="部分入园" value="partial_entry" />
            <el-option label="已全部入园" value="entered" />
            <el-option label="已取消" value="cancelled" />
          </el-select>
          <el-date-picker
            v-model="groupVisitRange"
            type="daterange"
            value-format="YYYY-MM-DD"
            range-separator="至"
            start-placeholder="开始日期"
            end-placeholder="结束日期"
            class="w-72"
          />
          <el-button type="primary" :icon="Search" @click="applyGroupFilters">查询</el-button>
          <el-button :disabled="!hasGroupFilters" @click="clearGroupFilters">清空</el-button>
        </div>
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
          <el-table-column label="操作" width="390" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="openGroupDetail(row)">{{ groupDetailActionText(row) }}</el-button>
              <el-button v-if="isGroupOwner(row) && can('teams.write')" link type="success" :disabled="row.status !== 'draft'" @click="openContractOrder(row)">生成订单</el-button>
              <el-button v-if="isGroupOwner(row) && can('teams.write')" link type="primary" :disabled="row.status !== 'draft'" @click="openAttachOrder(row)">绑定已有订单</el-button>
              <el-button
                v-if="showSettlementAction(row)"
                link
                type="success"
                :disabled="!canGenerateSettlement(row)"
                :title="settlementGenerationHint(row)"
                @click="generateSettlement(row)"
              >{{ canGenerateSettlement(row) ? '生成结算单' : '待全部入园' }}</el-button>
              <el-dropdown
                v-if="isGroupOwner(row) && can('teams.write') && !['entered', 'cancelled'].includes(row.status)"
                class="ml-2 align-middle"
                trigger="click"
                @command="handleGroupPlanCommand($event, row)"
              >
                <el-button link type="primary" :icon="MoreFilled" title="计划管理" aria-label="计划管理" />
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item command="edit">调整计划</el-dropdown-item>
                    <el-dropdown-item command="cancel" divided>取消计划</el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
            </template>
          </el-table-column>
        </el-table>
        <div class="mt-4 flex justify-end">
          <el-pagination
            :current-page="groupPagination.page"
            :page-size="groupPagination.page_size"
            :page-sizes="[10, 20, 40]"
            :total="groupPagination.total"
            layout="total, sizes, prev, pager, next"
            @size-change="handleGroupSizeChange"
            @current-change="handleGroupPageChange"
          />
        </div>
      </el-tab-pane>

      <el-tab-pane v-if="isTravelAgency" label="合作景区" name="supplier-partners">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <span class="text-sm text-gray-500">申请合作后，由景区确认并配置团队合同。</span>
          <el-button v-if="can('teams.write')" type="primary" :icon="Connection" @click="openSupplierPartnerDialog">申请合作景区</el-button>
        </div>
        <el-table :data="supplierPartners" v-loading="supplierPartnersLoading" stripe empty-text="暂无合作景区">
          <el-table-column prop="supplier_name" label="景区供应商" min-width="180" />
          <el-table-column prop="supplier_code" label="系统编号" min-width="150" />
          <el-table-column prop="contact" label="联系人" min-width="130"><template #default="{ row }">{{ row.contact || '-' }}</template></el-table-column>
          <el-table-column label="申请时间" width="180"><template #default="{ row }">{{ dateTime(row.created_at) }}</template></el-table-column>
          <el-table-column label="合作状态" width="120"><template #default="{ row }"><el-tag :type="partnerStatusType(row.status)">{{ partnerStatusText(row.status) }}</el-tag></template></el-table-column>
          <el-table-column label="下一步" width="140" fixed="right">
            <template #default="{ row }">
              <el-button v-if="row.status === 'active'" link type="primary" @click="showContracts">查看团队合同</el-button>
              <span v-else-if="row.status === 'pending'" class="text-xs text-gray-400">等待景区确认</span>
              <el-button v-else-if="can('teams.write')" link type="primary" @click="reapplySupplierPartner(row)">重新申请</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-if="isTravelAgency" label="团队档案" name="references">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div class="flex flex-wrap items-center gap-3">
            <el-segmented v-model="referenceType" :options="referenceTypeOptions" />
            <span class="text-sm text-gray-500">停用档案不会出现在新计划中，历史团队记录仍会保留。</span>
          </div>
          <el-button v-if="can('teams.write')" type="primary" :icon="Plus" @click="openReferenceDialog()">新增{{ referenceTypeText }}</el-button>
        </div>

        <el-table v-if="referenceType === 'agent'" :data="agents" v-loading="referencesLoading" stripe empty-text="暂无业务员档案">
          <el-table-column prop="job_number" label="工号" min-width="150" />
          <el-table-column prop="name" label="姓名" min-width="150" />
          <el-table-column prop="phone" label="手机号" min-width="150"><template #default="{ row }">{{ row.phone || '-' }}</template></el-table-column>
          <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'info'">{{ row.status === 'active' ? '启用' : '停用' }}</el-tag></template></el-table-column>
          <el-table-column v-if="can('teams.write')" label="操作" width="150" fixed="right">
            <template #default="{ row }"><el-button link type="primary" @click="openReferenceDialog(row)">编辑</el-button><el-button link :type="row.status === 'active' ? 'warning' : 'success'" @click="toggleReferenceStatus(row)">{{ row.status === 'active' ? '停用' : '启用' }}</el-button></template>
          </el-table-column>
        </el-table>

        <el-table v-else-if="referenceType === 'guide'" :data="guides" v-loading="referencesLoading" stripe empty-text="暂无导游档案">
          <el-table-column prop="name" label="姓名" min-width="150" />
          <el-table-column prop="phone" label="手机号" min-width="150"><template #default="{ row }">{{ row.phone || '-' }}</template></el-table-column>
          <el-table-column prop="license_no" label="导游证号" min-width="180"><template #default="{ row }">{{ row.license_no || '-' }}</template></el-table-column>
          <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'info'">{{ row.status === 'active' ? '启用' : '停用' }}</el-tag></template></el-table-column>
          <el-table-column v-if="can('teams.write')" label="操作" width="150" fixed="right">
            <template #default="{ row }"><el-button link type="primary" @click="openReferenceDialog(row)">编辑</el-button><el-button link :type="row.status === 'active' ? 'warning' : 'success'" @click="toggleReferenceStatus(row)">{{ row.status === 'active' ? '停用' : '启用' }}</el-button></template>
          </el-table-column>
        </el-table>

        <el-table v-else :data="vehicles" v-loading="referencesLoading" stripe empty-text="暂无车辆档案">
          <el-table-column prop="plate_number" label="车牌号" min-width="150" />
          <el-table-column prop="driver_name" label="司机" min-width="140"><template #default="{ row }">{{ row.driver_name || '-' }}</template></el-table-column>
          <el-table-column prop="driver_phone" label="司机电话" min-width="150"><template #default="{ row }">{{ row.driver_phone || '-' }}</template></el-table-column>
          <el-table-column prop="capacity" label="座位数" width="100"><template #default="{ row }">{{ row.capacity || '-' }}</template></el-table-column>
          <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'info'">{{ row.status === 'active' ? '启用' : '停用' }}</el-tag></template></el-table-column>
          <el-table-column v-if="can('teams.write')" label="操作" width="150" fixed="right">
            <template #default="{ row }"><el-button link type="primary" @click="openReferenceDialog(row)">编辑</el-button><el-button link :type="row.status === 'active' ? 'warning' : 'success'" @click="toggleReferenceStatus(row)">{{ row.status === 'active' ? '停用' : '启用' }}</el-button></template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-if="isSupplier" label="合作旅行社" name="travel-partners">
        <div class="mb-4 text-sm text-gray-500">确认旅行社合作申请后，即可为其创建专属产品价格和授信合同。</div>
        <el-table :data="travelAgencyPartners" v-loading="travelAgencyPartnersLoading" stripe empty-text="暂无旅行社合作申请">
          <el-table-column prop="travel_name" label="旅行社" min-width="180" />
          <el-table-column prop="travel_code" label="系统编号" min-width="150" />
          <el-table-column label="联系方式" min-width="180"><template #default="{ row }">{{ row.contact || '-' }}<span v-if="row.phone" class="ml-2 text-gray-400">{{ row.phone }}</span></template></el-table-column>
          <el-table-column label="申请时间" width="180"><template #default="{ row }">{{ dateTime(row.created_at) }}</template></el-table-column>
          <el-table-column label="合作状态" width="120"><template #default="{ row }"><el-tag :type="partnerStatusType(row.status)">{{ partnerStatusText(row.status) }}</el-tag></template></el-table-column>
          <el-table-column v-if="can('teams.write') || can('teams.contracts.write')" label="操作" width="210" fixed="right">
            <template #default="{ row }">
              <template v-if="row.status === 'pending' && can('teams.write')">
                <el-button link type="success" @click="auditTravelPartner(row, 'active')">通过</el-button>
                <el-button link type="danger" @click="auditTravelPartner(row, 'rejected')">拒绝</el-button>
              </template>
              <el-button v-else-if="row.status === 'active' && can('teams.contracts.write')" link type="primary" @click="openContractForPartner(row)">创建合同</el-button>
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
                <span class="ml-2 text-xs text-gray-400">至少 {{ rule.min_quantity || 1 }} 张/单</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="授信额度" width="130"><template #default="{ row }">¥{{ cents(row.credit_limit_cents) }}</template></el-table-column>
          <el-table-column label="账期" width="100"><template #default="{ row }">{{ row.settlement_days }} 天</template></el-table-column>
          <el-table-column label="状态" width="110"><template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'warning'">{{ row.status === 'active' ? '有效' : '已暂停' }}</el-tag></template></el-table-column>
          <el-table-column v-if="isSupplier && can('teams.contracts.write')" label="操作" width="90" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openContractDialog(row)">编辑</el-button></template></el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="双方结算" name="settlements">
        <el-table :data="settlements" v-loading="settlementsLoading" stripe empty-text="暂无团队结算单">
          <el-table-column prop="statement_no" label="结算单" min-width="200" />
          <el-table-column label="类型" width="110"><template #default="{ row }">{{ settlementKindText(row.kind) }}</template></el-table-column>
          <el-table-column label="团队" min-width="180"><template #default="{ row }"><div>{{ row.group_name || '团队信息缺失' }}</div><div v-if="row.group_no" class="text-xs text-gray-400">{{ row.group_no }}</div></template></el-table-column>
          <el-table-column label="旅行社" min-width="150"><template #default="{ row }">{{ row.travel_tenant_name || '旅行社信息缺失' }}</template></el-table-column>
          <el-table-column label="景区供应商" min-width="150"><template #default="{ row }">{{ row.supplier_tenant_name || '供应商信息缺失' }}</template></el-table-column>
          <el-table-column label="总额" width="120"><template #default="{ row }">¥{{ cents(row.gross_cents) }}</template></el-table-column>
          <el-table-column label="退款" width="120"><template #default="{ row }">¥{{ cents(row.refund_cents) }}</template></el-table-column>
          <el-table-column label="应付/冲减" width="130"><template #default="{ row }"><strong>{{ signedCents(Number(row.net_cents || 0) + Number(row.adjustment_cents || 0)) }}</strong></template></el-table-column>
          <el-table-column label="到期日" width="150"><template #default="{ row }"><span>{{ row.due_at ? dateOnly(row.due_at) : '未设置' }}</span><el-tag v-if="isSettlementOverdue(row)" type="danger" size="small" class="ml-2">已逾期</el-tag></template></el-table-column>
          <el-table-column label="状态" width="130"><template #default="{ row }"><el-tag :type="settlementStatusType(row.status)">{{ teamSettlementStatusText(row) }}</el-tag></template></el-table-column>
          <el-table-column label="操作" width="100" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openSettlement(row)">处理</el-button></template></el-table-column>
        </el-table>
        <div class="mt-4 flex justify-end">
          <el-pagination
            :current-page="settlementPagination.page"
            :page-size="settlementPagination.page_size"
            :page-sizes="[10, 20, 40]"
            :total="settlementPagination.total"
            layout="total, sizes, prev, pager, next"
            @size-change="handleSettlementSizeChange"
            @current-change="handleSettlementPageChange"
          />
        </div>
      </el-tab-pane>

      <el-tab-pane label="资金与对账" name="accounts">
        <div class="mb-3 text-sm text-gray-500">仅汇总旅行社与景区供应商之间的已付/已覆盖金额、合同授信占用和结算结果。</div>
        <el-table :data="accounts" v-loading="accountsLoading" stripe empty-text="暂无团队资金往来">
          <el-table-column label="旅行社" min-width="160"><template #default="{ row }">{{ row.travel_tenant_name || '旅行社信息缺失' }}</template></el-table-column>
          <el-table-column label="景区供应商" min-width="160"><template #default="{ row }">{{ row.supplier_tenant_name || '供应商信息缺失' }}</template></el-table-column>
          <el-table-column prop="active_contract_count" label="有效合同" width="100" />
          <el-table-column prop="group_count" label="团队数" width="90" />
          <el-table-column label="合同总额" min-width="120"><template #default="{ row }">¥{{ cents(row.contract_amount_cents) }}</template></el-table-column>
          <el-table-column label="已付/已覆盖" min-width="125"><template #default="{ row }">¥{{ cents(row.deposit_cents) }}</template></el-table-column>
          <el-table-column label="授信额度" min-width="120"><template #default="{ row }">¥{{ cents(row.credit_limit_cents) }}</template></el-table-column>
          <el-table-column label="已占授信" min-width="120"><template #default="{ row }">¥{{ cents(row.credit_used_cents) }}</template></el-table-column>
          <el-table-column label="可用授信" min-width="120"><template #default="{ row }"><span :class="row.available_credit_cents < 0 ? 'text-red-600 font-medium' : ''">{{ signedCents(row.available_credit_cents) }}</span></template></el-table-column>
          <el-table-column label="待结" min-width="120"><template #default="{ row }">¥{{ cents(row.pending_cents) }}</template></el-table-column>
          <el-table-column label="已付" min-width="120"><template #default="{ row }">¥{{ cents(row.paid_cents) }}</template></el-table-column>
          <el-table-column prop="disputed_count" label="争议单" width="90" />
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="supplierPartnerDialog" title="申请合作景区" width="520px" :close-on-click-modal="false">
      <el-form label-position="top">
        <el-form-item label="景区系统编号" required>
          <div class="flex w-full gap-2">
            <el-input v-model="supplierSearchCode" placeholder="输入景区提供的系统编号" clearable @keyup.enter="searchSupplierPartner" />
            <el-button :icon="Search" :loading="supplierSearching" @click="searchSupplierPartner">查询</el-button>
          </div>
        </el-form-item>
      </el-form>
      <el-descriptions v-if="supplierSearchResult" :column="1" border>
        <el-descriptions-item label="景区供应商">{{ supplierSearchResult.supplier_name }}</el-descriptions-item>
        <el-descriptions-item label="系统编号">{{ supplierSearchResult.supplier_code }}</el-descriptions-item>
        <el-descriptions-item label="联系人">{{ supplierSearchResult.contact || '-' }}</el-descriptions-item>
        <el-descriptions-item v-if="supplierSearchResult.status" label="当前状态">{{ partnerStatusText(supplierSearchResult.status) }}</el-descriptions-item>
      </el-descriptions>
      <template #footer>
        <el-button @click="supplierPartnerDialog = false">取消</el-button>
        <el-button type="primary" :loading="supplierApplying" :disabled="!supplierSearchResult || ['pending', 'active'].includes(supplierSearchResult.status)" @click="applySupplierPartner">提交合作申请</el-button>
      </template>
    </el-dialog>

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
        </div>
        <el-form-item label="游玩日期" required><el-date-picker v-model="groupForm.visit_date" type="date" value-format="YYYY-MM-DD" class="w-full" /></el-form-item>
        <div class="grid grid-cols-3 gap-3">
          <el-form-item label="业务员">
            <el-select v-model="groupForm.agent_id" clearable filterable class="w-full" placeholder="选填"><el-option v-for="agent in activeAgents" :key="agent.id" :label="`${agent.name}（${agent.job_number}）`" :value="agent.id" /></el-select>
          </el-form-item>
          <el-form-item label="导游">
            <el-select v-model="groupForm.guide_id" clearable filterable class="w-full" placeholder="选填"><el-option v-for="guide in activeGuides" :key="guide.id" :label="`${guide.name}${guide.phone ? ` · ${guide.phone}` : ''}`" :value="guide.id" /></el-select>
          </el-form-item>
          <el-form-item label="车辆">
            <el-select v-model="groupForm.vehicle_id" clearable filterable class="w-full" placeholder="选填"><el-option v-for="vehicle in activeVehicles" :key="vehicle.id" :label="vehicle.plate_number" :value="vehicle.id" /></el-select>
          </el-form-item>
        </div>
      </el-form>
      <template #footer><el-button @click="groupDialog = false">取消</el-button><el-button type="primary" :loading="saving" @click="createGroup">创建</el-button></template>
    </el-dialog>

    <el-dialog v-model="groupPlanDialog" title="调整团队计划" width="620px" :close-on-click-modal="false">
      <el-alert
        title="已生成订单、提交确认单或开始入园后，团队名称和日期会受到保护；业务员、导游和车辆仍可按现场情况调整。"
        type="info"
        :closable="false"
        show-icon
        class="mb-4"
      />
      <el-form :model="groupPlanForm" label-position="top">
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="团队名称" required><el-input v-model="groupPlanForm.name" maxlength="120" /></el-form-item>
          <el-form-item label="游玩日期" required><el-date-picker v-model="groupPlanForm.visit_date" type="date" value-format="YYYY-MM-DD" class="w-full" /></el-form-item>
        </div>
        <div class="grid grid-cols-3 gap-3">
          <el-form-item label="业务员"><el-select v-model="groupPlanForm.agent_id" clearable filterable class="w-full" placeholder="不指定"><el-option v-for="agent in activeAgents" :key="agent.id" :label="`${agent.name}（${agent.job_number}）`" :value="agent.id" /></el-select></el-form-item>
          <el-form-item label="导游"><el-select v-model="groupPlanForm.guide_id" clearable filterable class="w-full" placeholder="不指定"><el-option v-for="guide in activeGuides" :key="guide.id" :label="guide.name" :value="guide.id" /></el-select></el-form-item>
          <el-form-item label="车辆"><el-select v-model="groupPlanForm.vehicle_id" clearable filterable class="w-full" placeholder="不指定"><el-option v-for="vehicle in activeVehicles" :key="vehicle.id" :label="vehicle.plate_number" :value="vehicle.id" /></el-select></el-form-item>
        </div>
        <div class="-mt-3 mb-3 text-xs text-gray-500">选择框只显示启用档案；原档案已停用时会留空，保存后将解除该安排。</div>
        <el-form-item label="调整原因" required>
          <el-input v-model="groupPlanForm.reason" type="textarea" :rows="3" maxlength="255" show-word-limit placeholder="例如：游客调整行程，改由李导游带队" />
          <div class="mt-1 text-xs text-gray-500">原因会写入审计记录，方便旅行社和景区核对变更。</div>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="groupPlanDialog = false">取消</el-button><el-button type="primary" :loading="savingGroupPlan" @click="saveGroupPlan">保存调整</el-button></template>
    </el-dialog>

    <el-dialog v-model="referenceDialog" :title="`${referenceForm.id ? '编辑' : '新增'}${referenceTypeText}`" width="520px" :close-on-click-modal="false">
      <el-form :model="referenceForm" label-position="top">
        <template v-if="referenceType === 'agent'">
          <div class="grid grid-cols-2 gap-3">
            <el-form-item label="姓名" required><el-input v-model="referenceForm.name" maxlength="80" /></el-form-item>
            <el-form-item label="工号" required><el-input v-model="referenceForm.job_number" maxlength="50" /></el-form-item>
          </div>
          <el-form-item label="手机号"><el-input v-model="referenceForm.phone" maxlength="30" /></el-form-item>
        </template>
        <template v-else-if="referenceType === 'guide'">
          <div class="grid grid-cols-2 gap-3">
            <el-form-item label="姓名" required><el-input v-model="referenceForm.name" maxlength="80" /></el-form-item>
            <el-form-item label="手机号"><el-input v-model="referenceForm.phone" maxlength="30" /></el-form-item>
          </div>
          <el-form-item label="导游证号"><el-input v-model="referenceForm.license_no" maxlength="80" /></el-form-item>
        </template>
        <template v-else>
          <el-form-item label="车牌号" required><el-input v-model="referenceForm.plate_number" maxlength="30" /></el-form-item>
          <div class="grid grid-cols-2 gap-3">
            <el-form-item label="司机姓名"><el-input v-model="referenceForm.driver_name" maxlength="80" /></el-form-item>
            <el-form-item label="司机电话"><el-input v-model="referenceForm.driver_phone" maxlength="30" /></el-form-item>
          </div>
          <el-form-item label="座位数"><el-input-number v-model="referenceForm.capacity" :min="0" :precision="0" class="w-full" /><div class="mt-1 text-xs text-gray-500">不确定时可填 0，不影响创建团队计划。</div></el-form-item>
        </template>
      </el-form>
      <template #footer><el-button @click="referenceDialog = false">取消</el-button><el-button type="primary" :loading="savingReference" @click="saveReference">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="contractDialog" :title="contractForm.id ? '编辑旅行社合同' : '新增旅行社合同'" width="760px" :close-on-click-modal="false">
      <el-form :model="contractForm" label-position="top">
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="旅行社" required>
            <el-select v-model="contractForm.travel_tenant_id" :disabled="contractPartnerLocked" filterable class="w-full" placeholder="选择已合作旅行社">
              <el-option v-for="partner in contractPartners" :key="partner.tenant_id" :label="`${partner.name}（${partner.system_code}）`" :value="partner.tenant_id" />
            </el-select>
          </el-form-item>
          <el-form-item label="合同号" required><el-input v-model="contractForm.contract_no" :disabled="Boolean(contractForm.id)" maxlength="100" /></el-form-item>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="账期（天）"><el-input-number v-model="contractForm.settlement_days" :min="0" class="w-full" /></el-form-item>
          <el-form-item label="授信额度（元）">
            <el-input-number v-model="contractCreditYuan" :min="0" :precision="2" class="w-full" />
            <div class="mt-1 text-xs text-gray-500">0 表示不提供授信；旅行社需有可用授信后才能按合同出单。</div>
          </el-form-item>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="合同开始日期">
            <el-date-picker v-model="contractForm.starts_at" type="date" value-format="YYYY-MM-DD" clearable class="w-full" placeholder="不限制开始日期" />
          </el-form-item>
          <el-form-item label="合同结束日期">
            <el-date-picker v-model="contractForm.ends_at" type="date" value-format="YYYY-MM-DD" clearable class="w-full" placeholder="不限制结束日期" />
          </el-form-item>
        </div>
        <el-form-item label="合同状态"><el-radio-group v-model="contractForm.status"><el-radio-button label="active">有效</el-radio-button><el-radio-button label="suspended">暂停</el-radio-button></el-radio-group></el-form-item>
        <div class="mb-2 flex items-center justify-between">
          <div><div class="font-medium text-gray-900">产品结算价</div><div class="text-xs text-gray-500">选择当前景区自己的已上架票种，与“允许分销商代理销售”设置无关；已产生订单仍保留原价格快照。</div></div>
          <el-button size="small" :icon="Plus" :disabled="!contractProducts.length" @click="addContractPriceRule">添加产品</el-button>
        </div>
        <el-alert v-if="!contractProducts.length" title="暂无可用于团队合同的票种，请先发布已上架且按人出票的票种" type="warning" :closable="false" show-icon class="mb-3" />
        <div v-for="(rule, index) in contractPriceRules" :key="index" class="mb-3 grid grid-cols-[1fr_150px_150px_40px] items-end gap-3 rounded border border-gray-200 p-3">
          <el-form-item label="产品" class="mb-0"><el-select v-model="rule.product_id" filterable class="w-full" placeholder="选择产品"><el-option v-for="product in contractProducts" :key="product.id" :label="`${product.scenic_area_name} · ${product.name} · ¥${Number(product.price || 0).toFixed(2)}`" :value="product.id" /></el-select></el-form-item>
          <el-form-item label="结算价（元）" class="mb-0"><el-input-number v-model="rule.price_yuan" :min="0.01" :precision="2" :controls="false" class="w-full" /></el-form-item>
          <el-form-item label="最低成团人数" class="mb-0"><el-input-number v-model="rule.min_quantity" :min="1" :precision="0" class="w-full" /></el-form-item>
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
              <template #default="{ row }"><el-button v-if="!['entered', 'cancelled'].includes(row.status)" link type="danger" @click="removeTemporaryMember(row)">{{ row.ticket_code ? '退票减员' : '临时减员' }}</el-button></template>
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
          <el-form-item label="导游"><el-select v-model="confirmationForm.guide_id" clearable filterable class="w-full"><el-option v-for="guide in activeGuides" :key="guide.id" :label="`${guide.name} ${guide.phone || ''}`" :value="guide.id" /></el-select></el-form-item>
          <el-form-item label="车辆"><el-select v-model="confirmationForm.vehicle_id" clearable filterable class="w-full"><el-option v-for="vehicle in activeVehicles" :key="vehicle.id" :label="`${vehicle.plate_number} ${vehicle.driver_name || ''}`" :value="vehicle.id" /></el-select></el-form-item>
        </div>
        <el-form-item label="现场说明"><el-input v-model="confirmationForm.notes" type="textarea" :rows="3" maxlength="500" show-word-limit /></el-form-item>
      </el-form>
      <template #footer><el-button @click="confirmationDialog = false">取消</el-button><el-button type="primary" :loading="confirmationSaving" @click="submitConfirmation">提交版本</el-button></template>
    </el-dialog>

    <el-dialog v-model="temporaryMemberDialog" title="临时加员" width="500px" append-to-body>
      <el-alert title="加员会使用当前团队订单中已预留、尚未分配的票；没有备用票时不会新增未付款票权。" type="info" :closable="false" class="mb-3" />
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
      <el-alert title="系统按当前游客名单逐人出票，并直接记入团队合同授信；价格、景区和供应商均从合同读取。" type="info" show-icon :closable="false" class="mb-4" />
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
        <el-descriptions-item label="已付/已覆盖金额">¥{{ cents(selectedSettlement.deposit_cents) }}</el-descriptions-item>
        <el-descriptions-item label="争议调整">{{ signedCents(selectedSettlement.adjustment_cents) }}</el-descriptions-item>
        <el-descriptions-item label="最终应付/冲减"><strong>{{ signedCents(Number(selectedSettlement.net_cents || 0) + Number(selectedSettlement.adjustment_cents || 0)) }}</strong></el-descriptions-item>
        <el-descriptions-item label="到期日"><span>{{ selectedSettlement.due_at ? dateOnly(selectedSettlement.due_at) : '未设置' }}</span><el-tag v-if="isSettlementOverdue(selectedSettlement)" type="danger" size="small" class="ml-2">已逾期</el-tag></el-descriptions-item>
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
        <el-button v-if="canSubmitSettlementPayment" type="success" :loading="settlementActionLoading" @click="submitSettlementPayment">登记付款</el-button>
        <el-button v-if="canConfirmSettlementReceipt" type="success" :loading="settlementActionLoading" @click="confirmSettlementReceipt">确认到账</el-button>
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
import { Connection, Delete, DocumentAdd, Download, MoreFilled, Plus, Printer, Refresh, Search } from '@element-plus/icons-vue'
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
const supplierPartners = ref<any[]>([])
const travelAgencyPartners = ref<any[]>([])
const loading = ref(false)
const contractsLoading = ref(false)
const settlementsLoading = ref(false)
const accountsLoading = ref(false)
const supplierPartnersLoading = ref(false)
const travelAgencyPartnersLoading = ref(false)
const referencesLoading = ref(false)
const isGroupOwner = (row: any) => currentTenantID.value > 0 && Number(row?.tenant_id) === currentTenantID.value
const isGroupSupplier = (row: any) => currentTenantID.value > 0 && Number(row?.supplier_tenant_id) === currentTenantID.value && !isGroupOwner(row)
const activeTravelContracts = computed(() => contracts.value.filter((contract: any) => Number(contract.id) > 0 && Number(contract.travel_tenant_id) === currentTenantID.value && contract.status === 'active'))

const cents = (value: number) => (Number(value || 0) / 100).toFixed(2)
const signedCents = (value: number) => `${Number(value || 0) > 0 ? '+' : Number(value || 0) < 0 ? '-' : ''}¥${cents(Math.abs(Number(value || 0)))}`
const dateOnly = (value: string) => value ? value.slice(0, 10) : '-'
const dateOnlyValue = (value: unknown) => typeof value === 'string' && value.length >= 10 ? value.slice(0, 10) : ''
const contractDateValue = (value: string) => value ? `${value}T00:00:00+08:00` : null
const dateTime = (value: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-'
const groupStatusText = (status: string) => ({ draft: '草稿', confirmed: '待入园', partial_entry: '部分入园', entered: '已全部入园', cancelled: '已取消' } as Record<string, string>)[status] || '未知状态'
const groupStatusType = (status: string) => status === 'entered' ? 'success' : status === 'partial_entry' ? 'warning' : status === 'cancelled' ? 'danger' : status === 'confirmed' ? 'primary' : 'info'
const memberStatusText = (status: string) => ({ planned: '待出票', ticketed: '可入园', entered: '已入园', cancelled: '已取消' } as Record<string, string>)[status] || '未知状态'
const memberStatusType = (status: string) => status === 'entered' ? 'success' : status === 'ticketed' ? 'primary' : status === 'cancelled' ? 'danger' : 'info'
const settlementStatusText = (status: string) => ({ open: '未生成', statement: '已生成', settled: '已结清', draft: '待供应商确认', supplier_confirmed: '待旅行社确认', confirmed: '待旅行社付款', payment_submitted: '待景区确认到账', disputed: '有争议', paid: '已到账' } as Record<string, string>)[status] || '未知状态'
const settlementKindText = (kind: string) => kind === 'refund_correction' ? '退款冲减' : '团队结算'
const teamSettlementStatusText = (row: any) => row?.kind === 'refund_correction' && row?.status === 'paid' ? '已完成冲减' : settlementStatusText(row?.status)
const settlementStatusType = (status: string) => status === 'paid' ? 'success' : status === 'disputed' ? 'danger' : ['confirmed', 'payment_submitted'].includes(status) ? 'warning' : status === 'supplier_confirmed' ? 'primary' : 'info'
const isSettlementOverdue = (row: any) => Boolean(row?.due_at) && row?.status !== 'paid' && new Date(row.due_at).getTime() < Date.now()
const partnerStatusText = (status: string) => ({ pending: '待确认', active: '合作中', rejected: '已拒绝', suspended: '已暂停' } as Record<string, string>)[status] || '未知状态'
const partnerStatusType = (status: string) => status === 'active' ? 'success' : status === 'rejected' ? 'danger' : status === 'pending' ? 'warning' : 'info'
const groupQuery = reactive({ keyword: '', status: '' })
const groupVisitRange = ref<[string, string] | null>(null)
const groupPagination = reactive({ page: 1, page_size: 20, total: 0 })
const settlementPagination = reactive({ page: 1, page_size: 20, total: 0 })
const hasGroupFilters = computed(() => Boolean(groupQuery.keyword.trim() || groupQuery.status || groupVisitRange.value?.length))

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

const loadGroups = async (options: { resetPage?: boolean } = {}) => {
  if (options.resetPage) groupPagination.page = 1
  loading.value = true
  try {
    const response = await request.get('/teams', {
      params: {
        page: groupPagination.page,
        page_size: groupPagination.page_size,
        keyword: groupQuery.keyword.trim() || undefined,
        status: groupQuery.status || undefined,
        visit_start: groupVisitRange.value?.[0] || undefined,
        visit_end: groupVisitRange.value?.[1] || undefined,
      },
    })
    groups.value = response.data.data || []
    groupPagination.page = Math.max(1, Number(response.data.page) || groupPagination.page)
    groupPagination.page_size = Math.max(1, Number(response.data.page_size) || groupPagination.page_size)
    groupPagination.total = Math.max(0, Number(response.data.total) || 0)
  }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '团队加载失败') }
  finally { loading.value = false }
}
const applyGroupFilters = () => loadGroups({ resetPage: true })
const clearGroupFilters = () => {
  groupQuery.keyword = ''
  groupQuery.status = ''
  groupVisitRange.value = null
  return loadGroups({ resetPage: true })
}
const handleGroupSizeChange = (pageSize: number) => {
  groupPagination.page_size = pageSize
  return loadGroups({ resetPage: true })
}
const handleGroupPageChange = (page: number) => {
  groupPagination.page = page
  return loadGroups()
}
const loadContracts = async () => {
  contractsLoading.value = true
  try {
    const rows = (await request.get('/teams/contracts')).data.data || []
    contracts.value = rows.filter((contract: any) => Number(contract.id) > 0).map((contract: any) => ({
      ...contract,
      id: Number(contract.id),
      travel_tenant_id: Number(contract.travel_tenant_id) || null,
      supplier_tenant_id: Number(contract.supplier_tenant_id) || null,
      price_rules: (contract.price_rules || []).filter((rule: any) => Number(rule.product_id) > 0).map((rule: any) => ({
        ...rule,
        product_id: Number(rule.product_id),
        scenic_area_id: Number(rule.scenic_area_id) || null,
      })),
    }))
  }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '合同加载失败') }
  finally { contractsLoading.value = false }
}
const loadSettlements = async (options: { resetPage?: boolean } = {}) => {
  if (options.resetPage) settlementPagination.page = 1
  settlementsLoading.value = true
  try {
    const response = await request.get('/teams/settlements', { params: { page: settlementPagination.page, page_size: settlementPagination.page_size } })
    settlements.value = response.data.data || []
    settlementPagination.page = Math.max(1, Number(response.data.page) || settlementPagination.page)
    settlementPagination.page_size = Math.max(1, Number(response.data.page_size) || settlementPagination.page_size)
    settlementPagination.total = Math.max(0, Number(response.data.total) || 0)
  }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '结算单加载失败') }
  finally { settlementsLoading.value = false }
}
const handleSettlementSizeChange = (pageSize: number) => {
  settlementPagination.page_size = pageSize
  return loadSettlements({ resetPage: true })
}
const handleSettlementPageChange = (page: number) => {
  settlementPagination.page = page
  return loadSettlements()
}
const loadAccounts = async () => {
  accountsLoading.value = true
  try { accounts.value = (await request.get('/teams/accounts')).data.data || [] }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '团队资金汇总加载失败') }
  finally { accountsLoading.value = false }
}
const loadSupplierPartners = async () => {
  supplierPartnersLoading.value = true
  try { supplierPartners.value = (await request.get('/teams/partners/suppliers')).data.data || [] }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '合作景区加载失败') }
  finally { supplierPartnersLoading.value = false }
}
const loadTravelAgencyPartners = async () => {
  travelAgencyPartnersLoading.value = true
  try { travelAgencyPartners.value = (await request.get('/teams/partners/travel-agencies')).data.data || [] }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '合作旅行社加载失败') }
  finally { travelAgencyPartnersLoading.value = false }
}
const handleTabChange = (tab: string) => {
  if (tab === 'supplier-partners') loadSupplierPartners()
  else if (tab === 'references') loadReferenceData()
  else if (tab === 'travel-partners') loadTravelAgencyPartners()
  else if (tab === 'contracts') loadContracts()
  else if (tab === 'settlements') loadSettlements()
  else if (tab === 'accounts') loadAccounts()
}
const refreshActiveTab = () => activeTab.value === 'supplier-partners' ? loadSupplierPartners() : activeTab.value === 'references' ? loadReferenceData() : activeTab.value === 'travel-partners' ? loadTravelAgencyPartners() : activeTab.value === 'contracts' ? loadContracts() : activeTab.value === 'settlements' ? loadSettlements() : activeTab.value === 'accounts' ? loadAccounts() : loadGroups()

const supplierPartnerDialog = ref(false)
const supplierSearchCode = ref('')
const supplierSearchResult = ref<any>(null)
const supplierSearching = ref(false)
const supplierApplying = ref(false)
const openSupplierPartnerDialog = () => {
  supplierSearchCode.value = ''
  supplierSearchResult.value = null
  supplierPartnerDialog.value = true
}
const searchSupplierPartner = async () => {
  if (!supplierSearchCode.value.trim()) { ElMessage.warning('请输入景区系统编号'); return }
  supplierSearching.value = true
  try { supplierSearchResult.value = (await request.get('/teams/partners/supplier-search', { params: { code: supplierSearchCode.value.trim() } })).data.data }
  catch (e: any) { supplierSearchResult.value = null; ElMessage.error(e.response?.data?.error || '未找到该景区供应商') }
  finally { supplierSearching.value = false }
}
const applySupplierPartner = async () => {
  if (!supplierSearchResult.value) return
  supplierApplying.value = true
  try {
    await request.post('/teams/partners/suppliers', { system_code: supplierSearchResult.value.supplier_code })
    supplierPartnerDialog.value = false
    ElMessage.success('合作申请已提交')
    await loadSupplierPartners()
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '合作申请提交失败') }
  finally { supplierApplying.value = false }
}
const reapplySupplierPartner = async (row: any) => {
  try {
    await request.post('/teams/partners/suppliers', { system_code: row.supplier_code })
    ElMessage.success('合作申请已重新提交')
    await loadSupplierPartners()
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '重新申请失败') }
}
const auditTravelPartner = async (row: any, status: string) => {
  const action = status === 'active' ? '通过' : '拒绝'
  try {
    await ElMessageBox.confirm(`${action}“${row.travel_name}”的合作申请？`, `${action}合作申请`, { type: status === 'active' ? 'success' : 'warning', confirmButtonText: `确认${action}`, cancelButtonText: '取消' })
    await request.post(`/teams/partners/travel-agencies/${row.relationship_id}/audit`, { status })
    ElMessage.success(`已${action}合作申请`)
    await loadTravelAgencyPartners()
  } catch (e: any) { if (e !== 'cancel' && e !== 'close') ElMessage.error(e.response?.data?.error || '合作申请处理失败') }
}
const showContracts = async () => { activeTab.value = 'contracts'; await loadContracts() }

const saving = ref(false)
const groupDialog = ref(false)
const groupForm = reactive<{ name: string; supplier_tenant_id: number | null; scenic_area_id: number | null; contract_id: number | null; visit_date: string; expected_count: number; agent_id: number | null; guide_id: number | null; vehicle_id: number | null }>({ name: '', supplier_tenant_id: null, scenic_area_id: null, contract_id: null, visit_date: '', expected_count: 1, agent_id: null, guide_id: null, vehicle_id: null })
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
  groupForm.supplier_tenant_id = Number(selectedGroupContract.value?.supplier_tenant_id) || null
  groupForm.scenic_area_id = groupScenicOptions.value[0]?.id || null
}
const openGroupDialog = async () => {
  await Promise.all([contracts.value.length ? Promise.resolve() : loadContracts(), loadReferenceData()])
  Object.assign(groupForm, { name: '', supplier_tenant_id: null, scenic_area_id: null, contract_id: null, visit_date: '', expected_count: 1, agent_id: null, guide_id: null, vehicle_id: null })
  groupDialog.value = true
}
const createGroup = async () => {
  if (!groupForm.name.trim() || !groupForm.supplier_tenant_id || !groupForm.scenic_area_id || !groupForm.contract_id || !groupForm.visit_date) { ElMessage.warning('团队名称、供应商、景区、合同和日期均必填'); return }
  saving.value = true
  try {
    await request.post('/teams', {
      name: groupForm.name.trim(), supplier_tenant_id: groupForm.supplier_tenant_id,
      scenic_area_id: groupForm.scenic_area_id, contract_id: groupForm.contract_id,
      visit_date: contractDateValue(groupForm.visit_date), expected_count: groupForm.expected_count,
      agent_id: groupForm.agent_id || 0, guide_id: groupForm.guide_id || 0, vehicle_id: groupForm.vehicle_id || 0,
    })
    groupDialog.value = false; ElMessage.success('团队已创建'); await loadGroups()
  }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '团队创建失败') }
  finally { saving.value = false }
}

type TeamReferenceType = 'agent' | 'guide' | 'vehicle'
const referenceType = ref<TeamReferenceType>('agent')
const referenceTypeOptions = [
  { label: '业务员', value: 'agent' },
  { label: '导游', value: 'guide' },
  { label: '车辆', value: 'vehicle' },
]
const referenceTypeText = computed(() => ({ agent: '业务员', guide: '导游', vehicle: '车辆' } as Record<TeamReferenceType, string>)[referenceType.value])
const referenceEndpoint = computed(() => ({ agent: 'agents', guide: 'guides', vehicle: 'vehicles' } as Record<TeamReferenceType, string>)[referenceType.value])
const referenceDialog = ref(false)
const savingReference = ref(false)
const referenceForm = reactive({
  id: 0,
  name: '',
  phone: '',
  job_number: '',
  license_no: '',
  plate_number: '',
  driver_name: '',
  driver_phone: '',
  capacity: 0,
})
const openReferenceDialog = (row?: any) => {
  Object.assign(referenceForm, {
    id: Number(row?.id || 0),
    name: row?.name || '',
    phone: row?.phone || '',
    job_number: row?.job_number || '',
    license_no: row?.license_no || '',
    plate_number: row?.plate_number || '',
    driver_name: row?.driver_name || '',
    driver_phone: row?.driver_phone || '',
    capacity: Number(row?.capacity || 0),
  })
  referenceDialog.value = true
}
const referencePayload = () => {
  if (referenceType.value === 'agent') return { name: referenceForm.name.trim(), phone: referenceForm.phone.trim(), job_number: referenceForm.job_number.trim() }
  if (referenceType.value === 'guide') return { name: referenceForm.name.trim(), phone: referenceForm.phone.trim(), license_no: referenceForm.license_no.trim() }
  return { plate_number: referenceForm.plate_number.trim(), driver_name: referenceForm.driver_name.trim(), driver_phone: referenceForm.driver_phone.trim(), capacity: Number(referenceForm.capacity || 0) }
}
const saveReference = async () => {
  if (referenceType.value === 'agent' && (!referenceForm.name.trim() || !referenceForm.job_number.trim())) { ElMessage.warning('姓名和工号必填'); return }
  if (referenceType.value === 'guide' && !referenceForm.name.trim()) { ElMessage.warning('导游姓名必填'); return }
  if (referenceType.value === 'vehicle' && !referenceForm.plate_number.trim()) { ElMessage.warning('车牌号必填'); return }
  savingReference.value = true
  try {
    const path = `/teams/${referenceEndpoint.value}${referenceForm.id ? `/${referenceForm.id}` : ''}`
    if (referenceForm.id) await request.put(path, referencePayload())
    else await request.post(path, referencePayload())
    referenceDialog.value = false
    ElMessage.success(`${referenceTypeText.value}档案已保存`)
    await loadReferenceData()
  } catch (e: any) { ElMessage.error(e.response?.data?.error || `${referenceTypeText.value}档案保存失败`) }
  finally { savingReference.value = false }
}
const toggleReferenceStatus = async (row: any) => {
  const nextStatus = row.status === 'active' ? 'inactive' : 'active'
  const action = nextStatus === 'active' ? '启用' : '停用'
  try {
    const result = await ElMessageBox.prompt(
      nextStatus === 'active' ? `请输入重新启用${referenceTypeText.value}“${row.name || row.plate_number}”的原因。` : `请输入停用${referenceTypeText.value}“${row.name || row.plate_number}”的原因。历史团队不会受影响。`,
      `${action}${referenceTypeText.value}`,
      { confirmButtonText: `确认${action}`, cancelButtonText: '取消', inputType: 'textarea', inputValidator: value => value.trim() ? true : `${action}原因必填` },
    )
    await request.patch(`/teams/${referenceEndpoint.value}/${row.id}/status`, { status: nextStatus, reason: result.value.trim() })
    ElMessage.success(`${referenceTypeText.value}已${action}`)
    await loadReferenceData()
  } catch (e: any) { if (e !== 'cancel' && e !== 'close') ElMessage.error(e.response?.data?.error || `${action}失败`) }
}

const groupPlanDialog = ref(false)
const savingGroupPlan = ref(false)
const editingGroup = ref<any>(null)
const groupPlanForm = reactive<{ name: string; visit_date: string; agent_id: number | null; guide_id: number | null; vehicle_id: number | null; reason: string }>({ name: '', visit_date: '', agent_id: null, guide_id: null, vehicle_id: null, reason: '' })
const activeReferenceID = (rows: any[], id: unknown) => rows.some(row => row.status === 'active' && Number(row.id) === Number(id)) ? Number(id) : null
const openGroupPlanDialog = async (row: any) => {
  await loadReferenceData()
  editingGroup.value = row
  Object.assign(groupPlanForm, {
    name: row.name || '',
    visit_date: dateOnlyValue(row.visit_date),
    agent_id: activeReferenceID(agents.value, row.agent_id),
    guide_id: activeReferenceID(guides.value, row.guide_id),
    vehicle_id: activeReferenceID(vehicles.value, row.vehicle_id),
    reason: '',
  })
  groupPlanDialog.value = true
}
const saveGroupPlan = async () => {
  if (!editingGroup.value || !groupPlanForm.name.trim() || !groupPlanForm.visit_date) { ElMessage.warning('团队名称和游玩日期必填'); return }
  if (!groupPlanForm.reason.trim()) { ElMessage.warning('请填写调整原因'); return }
  savingGroupPlan.value = true
  try {
    await request.patch(`/teams/${editingGroup.value.id}`, {
      name: groupPlanForm.name.trim(),
      visit_date: contractDateValue(groupPlanForm.visit_date),
      agent_id: groupPlanForm.agent_id || 0,
      guide_id: groupPlanForm.guide_id || 0,
      vehicle_id: groupPlanForm.vehicle_id || 0,
      reason: groupPlanForm.reason.trim(),
    })
    groupPlanDialog.value = false
    ElMessage.success('团队计划已调整')
    await loadGroups()
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '团队计划调整失败') }
  finally { savingGroupPlan.value = false }
}
const cancelGroupPlan = async (row: any) => {
  try {
    const result = await ElMessageBox.prompt(
      '取消只处理尚未绑定订单、没有入园和结算事实的计划；已有订单请先在售后工作台完成退票或作废。',
      `取消团队：${row.name}`,
      { type: 'warning', inputType: 'textarea', confirmButtonText: '确认取消', cancelButtonText: '暂不取消', inputPlaceholder: '填写取消原因', inputValidator: value => value.trim() ? true : '取消原因必填' },
    )
    await request.post(`/teams/${row.id}/cancel`, { reason: result.value.trim() })
    ElMessage.success('团队计划已取消')
    await loadGroups()
  } catch (e: any) { if (e !== 'cancel' && e !== 'close') ElMessage.error(e.response?.data?.error || '团队计划取消失败') }
}
const handleGroupPlanCommand = (command: string, row: any) => command === 'edit' ? openGroupPlanDialog(row) : command === 'cancel' ? cancelGroupPlan(row) : undefined

const contractDialog = ref(false)
const contractPartnerLocked = ref(false)
const savingContract = ref(false)
const contractCreditYuan = ref(0)
const contractPartners = ref<any[]>([])
const contractProducts = ref<any[]>([])
type ContractPriceRuleForm = { product_id: number | null; price_yuan: number; min_quantity: number }
const contractPriceRules = ref<ContractPriceRuleForm[]>([])
const contractForm = reactive<{ id: number; travel_tenant_id: number | null; contract_no: string; settlement_days: number; starts_at: string; ends_at: string; status: string }>({
  id: 0,
  travel_tenant_id: null,
  contract_no: '',
  settlement_days: 0,
  starts_at: '',
  ends_at: '',
  status: 'active',
})
const loadContractFormOptions = async () => {
  const [partnersResponse, productsResponse] = await Promise.all([
    request.get('/teams/contract-partners'),
    request.get('/teams/contract-products'),
  ])
  contractPartners.value = (partnersResponse.data.data || [])
    .filter((partner: any) => Number(partner.tenant_id) > 0)
    .map((partner: any) => ({ ...partner, tenant_id: Number(partner.tenant_id) }))
  contractProducts.value = (productsResponse.data.data || [])
    .filter((product: any) => Number(product.id) > 0)
    .map((product: any) => ({ ...product, id: Number(product.id) }))
}
const addContractPriceRule = () => contractPriceRules.value.push({ product_id: null, price_yuan: 0, min_quantity: 1 })
const openContractDialog = async (row?: any, presetTravelTenantID?: number) => {
  try {
    await loadContractFormOptions()
    contractPartnerLocked.value = Boolean(row || presetTravelTenantID)
    Object.assign(contractForm, {
      id: Number(row?.id || 0), travel_tenant_id: row ? Number(row.travel_tenant_id) || null : (Number(presetTravelTenantID) || null),
      contract_no: row?.contract_no || '', settlement_days: Number(row?.settlement_days || 0), status: row?.status || 'active',
      starts_at: dateOnlyValue(row?.starts_at), ends_at: dateOnlyValue(row?.ends_at),
    })
    contractCreditYuan.value = Number(row?.credit_limit_cents || 0) / 100
    contractPriceRules.value = (row?.price_rules || []).map((rule: any) => ({ product_id: Number(rule.product_id) || null, price_yuan: Number(rule.price_cents || 0) / 100, min_quantity: Math.max(1, Number(rule.min_quantity || 1)) }))
    if (!contractPriceRules.value.length && contractProducts.value.length) addContractPriceRule()
    contractDialog.value = true
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '合同可选项加载失败') }
}
const openContractForPartner = (row: any) => openContractDialog(undefined, Number(row.travel_tenant_id))
const saveContract = async () => {
  if (!contractForm.travel_tenant_id || !contractForm.contract_no.trim() || !contractPriceRules.value.length || contractPriceRules.value.some(rule => !rule.product_id || rule.price_yuan <= 0)) { ElMessage.warning('请选择旅行社，并完整填写至少一个产品结算价'); return }
  if (new Set(contractPriceRules.value.map(rule => rule.product_id)).size !== contractPriceRules.value.length) { ElMessage.warning('同一个产品不能重复添加'); return }
  if (contractForm.starts_at && contractForm.ends_at && contractForm.ends_at < contractForm.starts_at) { ElMessage.warning('合同结束日期不能早于开始日期'); return }
  savingContract.value = true
  try {
    const payload = {
      travel_tenant_id: contractForm.travel_tenant_id, contract_no: contractForm.contract_no.trim(), status: contractForm.status,
      settlement_days: contractForm.settlement_days, credit_limit_cents: Math.round(contractCreditYuan.value * 100),
      starts_at: contractDateValue(contractForm.starts_at), ends_at: contractDateValue(contractForm.ends_at),
      price_rules: contractPriceRules.value.map(rule => ({ product_id: rule.product_id, price_cents: Math.round(rule.price_yuan * 100), min_quantity: rule.min_quantity })),
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
const agents = ref<any[]>([])
const guides = ref<any[]>([])
const vehicles = ref<any[]>([])
const activeAgents = computed(() => agents.value.filter(row => row.status === 'active'))
const activeGuides = computed(() => guides.value.filter(row => row.status === 'active'))
const activeVehicles = computed(() => vehicles.value.filter(row => row.status === 'active'))
const entryDeviceID = ref<number | null>(null)
const entryMemberSelection = ref<any[]>([])
const entering = ref(false)
const pendingEntryRequest = ref({ fingerprint: '', key: '' })
const rosterText = ref('')
const savingRoster = ref(false)
const canEditRoster = computed(() => can('teams.write') && isGroupOwner(selectedGroup.value) && selectedGroup.value?.status === 'draft' && !selectedGroup.value?.sales_order_id)
const canChangeMembers = computed(() => can('teams.write') && isGroupOwner(selectedGroup.value) && ['confirmed', 'partial_entry'].includes(selectedGroup.value?.status))
const canSubmitConfirmation = computed(() => canChangeMembers.value)
const canEnterSelectedGroup = computed(() => can('teams.write') && isGroupSupplier(selectedGroup.value) && ['confirmed', 'partial_entry'].includes(selectedGroup.value?.status))
const groupDetailActionText = (row: any) => {
  if (!isGroupSupplier(row)) return '名单详情'
  return ['confirmed', 'partial_entry'].includes(row.status) ? '履约入园' : '履约详情'
}
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
const loadReferenceData = async () => {
  referencesLoading.value = true
  try {
    const [agentResponse, guideResponse, vehicleResponse] = await Promise.all([
      request.get('/teams/agents'),
      request.get('/teams/guides'),
      request.get('/teams/vehicles'),
    ])
    agents.value = agentResponse.data.data || []
    guides.value = guideResponse.data.data || []
    vehicles.value = vehicleResponse.data.data || []
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '团队档案加载失败') }
  finally { referencesLoading.value = false }
}
const openGroupDetail = async (row: any) => {
  selectedGroup.value = row
  rosterText.value = ''
  entryDeviceID.value = null
  pendingEntryRequest.value = { fingerprint: '', key: '' }
  detailDialog.value = true
  await Promise.all([
    loadGroupDetail(),
    isGroupSupplier(row) && ['confirmed', 'partial_entry'].includes(row.status) ? loadAdmissionDevices() : Promise.resolve(),
    isGroupOwner(row) ? loadReferenceData() : Promise.resolve(),
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
const confirmationForm = reactive<{ confirmed_count: number; guide_id: number | null; vehicle_id: number | null; notes: string }>({ confirmed_count: 1, guide_id: null, vehicle_id: null, notes: '' })
const openConfirmationDialog = () => {
  Object.assign(confirmationForm, {
    confirmed_count: Number(selectedGroup.value?.expected_count || 1),
    guide_id: activeReferenceID(guides.value, selectedGroup.value?.guide_id),
    vehicle_id: activeReferenceID(vehicles.value, selectedGroup.value?.vehicle_id),
    notes: '',
  })
  confirmationDialog.value = true
}
const submitConfirmation = async () => {
  if (!selectedGroup.value || confirmationForm.confirmed_count < 1) return
  confirmationSaving.value = true
  try {
    await request.post(`/teams/${selectedGroup.value.id}/confirmations`, {
      ...confirmationForm, guide_id: confirmationForm.guide_id || 0, vehicle_id: confirmationForm.vehicle_id || 0,
    })
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
    if (row.ticket_code) {
      await ElMessageBox.confirm('该游客已有门票，减员以退票结果为准。退款成功后系统会自动更新团队人数并保留变更记录。', `退票减员：${row.name}`, { type: 'warning', confirmButtonText: '前往退票', cancelButtonText: '暂不处理' })
      detailDialog.value = false
      openTeamAfterSales()
      return
    }
    const result = await ElMessageBox.prompt('请输入临时减员原因。', `临时减员：${row.name}`, { inputType: 'textarea', inputValidator: value => value.trim() ? true : '减员原因必填' })
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
const attachOrderId = ref<number | null>(null)
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
  attachOrderId.value = null
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
const contractOrderForm = reactive<{ product_id: number | null; contact_name: string; contact_phone: string }>({ product_id: null, contact_name: '', contact_phone: '' })
const selectedOrderContract = computed(() => contracts.value.find((contract: any) => Number(contract.id) === Number(selectedGroup.value?.contract_id)))
const contractOrderProducts = computed(() => (selectedOrderContract.value?.price_rules || []).filter((rule: any) => Number(rule.scenic_area_id) === Number(selectedGroup.value?.scenic_area_id)))
const openContractOrder = async (row: any) => {
  selectedGroup.value = row
  if (!contracts.value.length) await loadContracts()
  Object.assign(contractOrderForm, { product_id: contractOrderProducts.value[0]?.product_id || null, contact_name: '', contact_phone: '' })
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

const showSettlementAction = (row: any) => can('settlements.write') && isGroupOwner(row) && Boolean(row.sales_order_id) && row.settlement_status === 'open' && row.status !== 'cancelled'
const canGenerateSettlement = (row: any) => showSettlementAction(row) && row.status === 'entered'
const settlementGenerationHint = (row: any) => canGenerateSettlement(row) ? '根据实际核销生成结算单' : '团队全部入园后才能生成结算单'
const generateSettlement = async (row: any) => {
  try { await request.post(`/teams/${row.id}/settlement`); ElMessage.success('团队结算单已生成'); activeTab.value = 'settlements'; await Promise.all([loadGroups(), loadSettlements({ resetPage: true })]) }
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
const canDisputeSettlement = computed(() => can('settlements.write') && (isSettlementSupplier.value || isSettlementTravel.value) && ['supplier_confirmed', 'confirmed', 'payment_submitted'].includes(selectedSettlement.value?.status))
const canAdjustSettlement = computed(() => can('settlements.write') && (isSettlementSupplier.value || isSettlementTravel.value) && selectedSettlement.value?.status === 'disputed')
const canSubmitSettlementPayment = computed(() => can('settlements.write') && isSettlementTravel.value && selectedSettlement.value?.status === 'confirmed')
const canConfirmSettlementReceipt = computed(() => can('settlements.write') && isSettlementSupplier.value && selectedSettlement.value?.status === 'payment_submitted')
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
const submitSettlementPayment = async () => {
  try { const result = await ElMessageBox.prompt('填写银行流水号、转账单号或付款凭证位置；提交后由景区确认到账', '登记付款', { confirmButtonText: '提交付款凭证', inputValidator: value => value.trim() ? true : '付款凭证必填' }); await updateSettlementStatus('payment_submitted', result.value.trim()) }
  catch (action: any) { if (action !== 'cancel' && action !== 'close') throw action }
}
const confirmSettlementReceipt = async () => {
  try {
    await ElMessageBox.confirm('请确认款项已经实际到账。确认后该结算单将标记为已到账。', '确认到账', { type: 'warning', confirmButtonText: '确认已到账', cancelButtonText: '暂不确认' })
    await updateSettlementStatus('paid')
  } catch (action: any) { if (action !== 'cancel' && action !== 'close') throw action }
}

onMounted(loadGroups)
</script>
