<template>
  <section class="hotel-page" v-loading="loading">
    <header class="page-heading">
      <div class="page-heading-copy">
        <h1>酒店经营</h1>
        <p>酒店、房型、价格计划和每日房量独立管理，不与景区门票库存混用。</p>
      </div>
      <div class="page-actions">
        <el-button v-if="canWrite" type="primary" @click="openHotelDialog()">
          <el-icon><Plus /></el-icon>新增酒店
        </el-button>
      </div>
    </header>

    <el-alert v-if="historyOnly" type="warning" :closable="false" show-icon title="供应商身份或酒店住宿业态已暂停，当前仅可查看历史配置与已有预订。" />

    <div v-if="hotels.length" class="hotel-switcher">
      <div class="hotel-switcher-label">当前酒店</div>
      <el-select v-model="selectedHotelId" filterable @change="switchHotel">
        <el-option v-for="hotel in hotels" :key="hotel.id" :label="hotel.name" :value="hotel.id" />
      </el-select>
      <div v-if="selectedHotel" class="hotel-summary">
        <strong>{{ selectedHotel.code }}</strong>
        <span>{{ selectedHotel.address || '暂未填写地址' }}</span>
        <el-tag :type="selectedHotel.status === 'active' ? 'success' : 'info'" effect="light">
          {{ selectedHotel.status === 'active' ? '营业中' : '已暂停' }}
        </el-tag>
      </div>
      <div class="hotel-switcher-actions">
        <el-button v-if="canWrite" @click="openHotelDialog(selectedHotel)">编辑酒店</el-button>
        <el-button v-if="canWrite" type="danger" plain @click="removeHotel">删除</el-button>
      </div>
    </div>

    <el-empty v-else-if="!loading" description="还没有酒店资料">
      <el-button v-if="canWrite" type="primary" @click="openHotelDialog()">创建第一家酒店</el-button>
    </el-empty>

    <el-tabs v-if="selectedHotel" v-model="activeTab" class="hotel-tabs" @tab-change="changeHotelTab">
      <el-tab-pane label="房型与价格" name="rooms">
        <div class="section-toolbar">
          <div>
            <h2>房型与价格</h2>
            <p>一个房型可设置多个售卖价格，所有价格计划共享该房型的每日房量。</p>
          </div>
          <el-button v-if="canWrite && selectedHotel.status === 'active'" type="primary" @click="openRoomDialog()">
            <el-icon><Plus /></el-icon>新增房型
          </el-button>
        </div>

        <div class="data-panel">
          <el-table :data="roomTypes" row-key="id" empty-text="暂无房型">
            <el-table-column prop="code" label="房型编号" width="140" />
            <el-table-column prop="name" label="房型名称" min-width="160" />
            <el-table-column label="入住人数" width="110">
              <template #default="{ row }">最多 {{ row.max_guests }} 人</template>
            </el-table-column>
            <el-table-column prop="bed_type" label="床型" min-width="150" show-overflow-tooltip />
            <el-table-column label="价格计划" width="110">
              <template #default="{ row }">{{ (ratePlans[row.id] || []).length }} 个</template>
            </el-table-column>
            <el-table-column label="状态" width="100">
              <template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'info'">{{ row.status === 'active' ? '启用' : '暂停' }}</el-tag></template>
            </el-table-column>
            <el-table-column label="操作" width="310" fixed="right">
              <template #default="{ row }">
                <el-button link type="primary" @click="openRateDialog(row)">价格计划</el-button>
                <el-button link type="primary" @click="openInventory(row)">设置房量</el-button>
                <el-button v-if="canWrite" link type="primary" @click="openRoomDialog(row)">编辑</el-button>
                <el-button v-if="canWrite" link type="danger" @click="removeRoom(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </el-tab-pane>

      <el-tab-pane label="每日房量" name="inventory">
        <div class="section-toolbar inventory-toolbar">
          <div>
            <h2>每日房量</h2>
            <p>按入住日期维护可售总量；已预留和已售房量由订单自动维护。</p>
          </div>
          <div class="inventory-filters">
            <el-select v-model="inventoryRoomTypeId" placeholder="选择房型" @change="loadInventory">
              <el-option v-for="room in roomTypes" :key="room.id" :label="room.name" :value="room.id" />
            </el-select>
            <el-date-picker v-model="inventoryRange" type="daterange" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" :clearable="false" :disabled-date="disablePastDate" @change="loadInventory" />
            <el-button v-if="canWrite" plain :disabled="!inventoryRows.length" @click="openInventoryBatchDialog">批量设置</el-button>
            <el-button v-if="canWrite" type="primary" :disabled="!inventoryRows.length" @click="saveInventory">保存房量</el-button>
          </div>
        </div>
        <div class="data-panel">
          <el-table :data="inventoryRows" empty-text="请选择房型和日期范围">
            <el-table-column prop="stay_date" label="入住日期" width="160" />
            <el-table-column label="星期" width="100">
              <template #default="{ row }">{{ weekday(row.stay_date) }}</template>
            </el-table-column>
            <el-table-column label="可售总量" width="190">
              <template #default="{ row }"><el-input-number v-model="row.capacity" :min="row.reserved + row.sold" :max="100000" :disabled="!canWrite" /></template>
            </el-table-column>
            <el-table-column prop="reserved" label="已预留" width="100" />
            <el-table-column prop="sold" label="已售" width="100" />
            <el-table-column label="剩余" width="100">
              <template #default="{ row }"><strong>{{ Math.max(0, row.capacity - row.reserved - row.sold) }}</strong></template>
            </el-table-column>
            <el-table-column label="关闭销售" min-width="120">
              <template #default="{ row }"><el-switch v-model="row.closed" :disabled="!canWrite" /></template>
            </el-table-column>
          </el-table>
        </div>
      </el-tab-pane>

      <el-tab-pane label="价格日历" name="pricing">
        <div class="section-toolbar inventory-toolbar">
          <div>
            <h2>入住日期价格日历</h2>
            <p>未设置日期价时沿用价格计划基础价；日期价只影响后续报价，不改写已售套餐和历史预约快照。</p>
          </div>
          <div class="inventory-filters">
            <el-select v-model="calendarRoomTypeId" placeholder="选择房型" @change="changeCalendarRoomType">
              <el-option v-for="room in roomTypes" :key="room.id" :label="room.name" :value="room.id" />
            </el-select>
            <el-select v-model="calendarRatePlanId" placeholder="选择价格计划" :disabled="!calendarRatePlans.length" @change="loadRatePlanCalendar">
              <el-option v-for="rate in calendarRatePlans" :key="rate.id" :label="rate.name" :value="rate.id" />
            </el-select>
            <el-date-picker v-model="calendarRange" type="daterange" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" :clearable="false" :disabled-date="disablePastDate" @change="loadRatePlanCalendar" />
            <el-button v-if="canWrite" plain :disabled="!calendarRows.length" @click="openCalendarBatchDialog">批量套用</el-button>
            <el-button v-if="canWrite" type="primary" :disabled="!calendarRows.length" @click="saveRatePlanCalendar">保存价格</el-button>
          </div>
        </div>
        <el-alert type="info" :closable="false" show-icon title="基础价是价格计划的默认值。把日期价改回基础价后保存，系统会清除该日期覆盖，后续基础价调整仍可自然生效。" />
        <div class="data-panel calendar-panel">
          <el-table :data="calendarRows" empty-text="请选择房型、价格计划和日期范围">
            <el-table-column prop="stay_date" label="入住日期" width="150" />
            <el-table-column label="星期" width="80"><template #default="{ row }">{{ weekday(row.stay_date) }}</template></el-table-column>
            <el-table-column label="基础零售价" width="120"><template #default="{ row }">¥{{ money(row.base_retail_price_cents) }}</template></el-table-column>
            <el-table-column label="入住日零售价" width="190">
              <template #default="{ row }"><el-input-number v-model="row.retail_price" :min="0.01" :precision="2" :step="10" :disabled="!canWrite" /></template>
            </el-table-column>
            <el-table-column label="基础结算价" width="120"><template #default="{ row }">¥{{ money(row.base_settlement_price_cents) }}</template></el-table-column>
            <el-table-column label="入住日结算价" width="190">
              <template #default="{ row }"><el-input-number v-model="row.settlement_price" :min="0" :precision="2" :step="10" :disabled="!canWrite" /></template>
            </el-table-column>
            <el-table-column label="价格来源" min-width="110"><template #default="{ row }"><el-tag :type="row.has_override ? 'warning' : 'info'">{{ row.has_override ? '日期覆盖' : '基础价' }}</el-tag></template></el-table-column>
          </el-table>
        </div>
      </el-tab-pane>

      <el-tab-pane v-if="showPackages" label="酒景套餐" name="packages">
        <div class="section-toolbar">
          <div>
            <h2>固定酒景套餐</h2>
            <p>一个套餐单位对应一张专用门票和一条酒店预订；门票核销与住宿履约分别记录。</p>
          </div>
          <el-button v-if="canPackageWrite && selectedHotel.status === 'active'" type="primary" @click="openPackageDialog()">
            <el-icon><Plus /></el-icon>新增套餐
          </el-button>
        </div>
        <el-radio-group v-model="packageSection" class="package-section" @change="changePackageSection">
          <el-radio-button label="catalog">套餐配置</el-radio-button>
          <el-radio-button v-if="canViewReservations" label="entitlements">预约权益</el-radio-button>
          <el-radio-button v-if="canViewReservations" label="reservations">住宿预订</el-radio-button>
          <el-radio-button v-if="canViewReservations" label="sync-failures">预约同步异常</el-radio-button>
        </el-radio-group>
        <div v-if="packageSection === 'catalog'" class="data-panel">
          <el-table :data="currentHotelPackages" empty-text="当前酒店暂无酒景套餐">
            <el-table-column prop="product_name" label="套餐名称" min-width="180" />
            <el-table-column label="住宿内容" min-width="190"><template #default="{ row }">{{ row.room_type_name }} · {{ row.nights }}晚 × {{ row.rooms_per_package }}间</template></el-table-column>
            <el-table-column prop="rate_plan_name" label="价格计划" min-width="130" />
            <el-table-column label="预约方式" min-width="150"><template #default="{ row }"><div>{{ row.booking_mode === 'after_purchase' ? '购买后预约' : '下单时选日期' }}</div><small v-if="row.booking_mode === 'after_purchase'">{{ row.voucher_validity_days }}天内预约</small></template></el-table-column>
            <el-table-column label="套餐售价" width="110"><template #default="{ row }">¥{{ money(row.retail_price_cents) }}</template></el-table-column>
            <el-table-column label="结算拆分" min-width="180"><template #default="{ row }">门票 ¥{{ money(row.ticket_settlement_price_cents) }} / 酒店 ¥{{ money(row.hotel_settlement_price_cents) }}</template></el-table-column>
            <el-table-column label="状态" width="90"><template #default="{ row }"><el-tag :type="row.status === 'online' ? 'success' : 'info'">{{ row.status === 'online' ? '在售' : '下架' }}</el-tag></template></el-table-column>
            <el-table-column label="操作" width="140" fixed="right"><template #default="{ row }"><el-button v-if="canPackageWrite" link type="primary" @click="openPackageDialog(row)">编辑</el-button><el-button v-if="canPackageWrite" link type="danger" @click="removePackage(row)">删除</el-button></template></el-table-column>
          </el-table>
        </div>
        <div v-else-if="packageSection === 'entitlements'" class="reservation-workspace">
          <el-alert type="info" :closable="false" show-icon title="预约权益表示已经售出但尚未指定入住日期的酒景套餐。游客完成预约后，系统才锁定对应日期的门票库存和酒店房量。" />
          <div class="reservation-toolbar">
            <el-input v-model="reservationOrderNo" clearable placeholder="订单号" @keyup.enter="searchReservations" />
            <el-select v-model="entitlementStatus" clearable placeholder="全部状态" @change="searchReservations">
              <el-option label="待预约" value="pending_booking" /><el-option label="预约处理中" value="booking_pending" /><el-option label="已预约" value="booked" /><el-option label="取消处理中" value="cancel_pending" /><el-option label="已退款" value="refunded" /><el-option label="已关闭" value="cancelled" />
            </el-select>
            <el-button @click="searchReservations">查询</el-button>
          </div>
          <div class="data-panel">
            <el-table :data="packageEntitlements" empty-text="当前酒店暂无套餐预约权益">
              <el-table-column prop="entitlement_no" label="权益编号" min-width="210" show-overflow-tooltip />
              <el-table-column prop="order_no" label="订单号" min-width="190" />
              <el-table-column prop="product_name" label="套餐" min-width="170" />
              <el-table-column label="住宿内容" min-width="180"><template #default="{ row }">{{ row.hotel_name }} · {{ row.room_type_name }}</template></el-table-column>
              <el-table-column label="预约有效期" width="220"><template #default="{ row }">{{ shortDate(row.valid_from) }} 至 {{ shortDate(row.valid_until) }}</template></el-table-column>
              <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="entitlementStatusType(row.status)">{{ entitlementStatusText(row.status) }}</el-tag></template></el-table-column>
              <el-table-column label="平台同步" width="110"><template #default="{ row }"><el-tag :type="row.platform_sync_status === 'failed' ? 'danger' : (row.platform_sync_status === 'synced' ? 'success' : 'info')">{{ row.platform_sync_status === 'failed' ? '同步失败' : (row.platform_sync_status === 'synced' ? '已同步' : (row.platform_sync_status === 'pending' ? '待同步' : '未触发')) }}</el-tag></template></el-table-column>
            </el-table>
            <div class="reservation-pagination"><el-pagination v-model:current-page="entitlementPage" v-model:page-size="reservationPageSize" :page-sizes="[10,20,40]" layout="total, sizes, prev, pager, next" :total="entitlementTotal" @current-change="loadEntitlements" @size-change="changeReservationPageSize" /></div>
          </div>
        </div>
        <div v-else-if="packageSection === 'reservations'" class="reservation-workspace">
          <el-alert type="info" :closable="false" show-icon title="这里只登记或同步酒店、PMS、人工确认后的票务侧住宿结果，不提供排房、房卡或酒店前台入住功能。" />
          <div v-if="canViewReports" class="package-metrics">
            <div><span>本期售出套餐</span><strong>{{ packageSummary.sales_units || packageSummary.package_units || 0 }} 份</strong><small>按订单付款时间统计</small></div>
            <div><span>净销售额</span><strong>¥{{ money(packageSummary.net_sales_cents) }}</strong><small>销售 ¥{{ money(packageSummary.gross_sales_cents) }} / 退款 ¥{{ money(packageSummary.refunded_sales_cents) }}</small></div>
            <div><span>本期新增预约</span><strong>{{ packageSummary.booking_units || 0 }} 份</strong><small>按住宿预约创建时间统计</small></div>
            <div><span>本期计划入住</span><strong>{{ packageSummary.stay_units || 0 }} 份</strong><small>按预订入住日期统计，包含待入住</small></div>
            <div><span>本期售出待预约</span><strong>{{ packageSummary.awaiting_booking_units || 0 }} 份</strong><small>已付款但尚未选择入住日期</small></div>
            <div><span>预计履约与余量</span><strong>¥{{ money(Number(packageSummary.ticket_component_net_cents || 0) + Number(packageSummary.hotel_component_net_cents || 0)) }}</strong><small>经营余量 ¥{{ money(packageSummary.unallocated_margin_cents) }}</small></div>
          </div>
          <div class="reservation-toolbar">
            <el-input v-model="reservationOrderNo" clearable placeholder="订单号" @keyup.enter="searchReservations" />
            <el-select v-model="reservationStatus" clearable placeholder="全部状态" @change="searchReservations">
              <el-option label="待支付" value="reserved" /><el-option label="待入住" value="confirmed" /><el-option label="已入住" value="checked_in" /><el-option label="已离店" value="checked_out" /><el-option label="未到店" value="no_show" /><el-option label="已退款" value="refunded" /><el-option label="已取消" value="cancelled" />
            </el-select>
            <el-date-picker v-if="canViewReports" v-model="reportRange" type="daterange" range-separator="至" start-placeholder="统计开始" end-placeholder="统计结束" :clearable="false" @change="loadPackageSummary" />
            <el-button @click="searchReservations">查询</el-button>
            <el-button v-if="canExportReservations" @click="exportReservations">导出住宿名单</el-button>
          </div>
          <p v-if="canViewReports" class="report-basis">销售按付款期归属，后续退款会回写原付款期的最终净额；预约按创建时间、计划入住按入住日期统计，后续预约不会改变原销售期间。</p>
          <div class="data-panel">
          <el-table :data="currentHotelReservations" empty-text="当前酒店暂无套餐住宿预订">
            <el-table-column prop="reservation_no" label="预订号" min-width="190" />
            <el-table-column prop="order_no" label="订单号" min-width="190" />
            <el-table-column prop="product_name" label="套餐" min-width="170" />
            <el-table-column label="入住联系人" min-width="160"><template #default="{ row }"><div>{{ row.guest_name || '-' }}</div><small>{{ row.contact_phone || '-' }}</small></template></el-table-column>
            <el-table-column label="房型与价格" min-width="180"><template #default="{ row }">{{ row.room_type_name }} · {{ row.rate_plan_name }}</template></el-table-column>
            <el-table-column label="入住日期" width="210"><template #default="{ row }">{{ shortDate(row.check_in_date) }} 至 {{ shortDate(row.check_out_date) }}</template></el-table-column>
            <el-table-column prop="rooms" label="房间" width="80"><template #default="{ row }">{{ row.rooms }}间</template></el-table-column>
            <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="reservationStatusType(row.status)">{{ reservationStatusText(row.status) }}</el-tag></template></el-table-column>
            <el-table-column v-if="canOperateReservations" label="操作" min-width="170" fixed="right">
              <template #default="{ row }">
                <el-button v-if="row.status === 'confirmed'" link type="primary" @click="setReservationStatus(row, 'checked_in')">登记已入住</el-button>
                <el-button v-if="row.status === 'checked_in'" link type="primary" @click="setReservationStatus(row, 'checked_out')">登记已离店</el-button>
                <el-button v-if="row.status === 'confirmed'" link @click="setReservationStatus(row, 'no_show', true)">未到店</el-button>
                <el-button v-if="['checked_in','checked_out','no_show'].includes(row.status)" link @click="correctReservationStatus(row)">纠正</el-button>
              </template>
            </el-table-column>
          </el-table>
          <div class="reservation-pagination"><el-pagination v-model:current-page="reservationPage" v-model:page-size="reservationPageSize" :page-sizes="[10,20,40]" layout="total, sizes, prev, pager, next" :total="reservationTotal" @current-change="loadReservations" @size-change="changeReservationPageSize" /></div>
          </div>
        </div>
        <div v-else-if="packageSection === 'sync-failures'" class="reservation-workspace">
          <el-alert type="warning" :closable="false" show-icon title="这里只展示已停止自动重试的小红书预约同步异常。确认渠道故障已经排除后，可填写原因继续重试。" />
          <div class="sync-failure-toolbar">
            <el-select v-model="syncFailureType" clearable placeholder="全部业务阶段" @change="searchSyncFailures">
              <el-option label="预约确认" value="book" />
              <el-option label="取消预约" value="revoke" />
              <el-option label="退款同步" value="refund" />
            </el-select>
            <el-button @click="loadSyncFailures">刷新</el-button>
          </div>
          <div class="data-panel">
            <el-table v-loading="syncFailureLoading" :data="syncFailures" empty-text="当前没有需要处理的预约同步异常">
              <el-table-column prop="order_no" label="订单号" min-width="190" show-overflow-tooltip />
              <el-table-column prop="entitlement_no" label="权益编号" min-width="200" show-overflow-tooltip />
              <el-table-column label="业务阶段" width="110"><template #default="{ row }">{{ syncOperationTypeText(row.type) }}</template></el-table-column>
              <el-table-column label="失败阶段" min-width="190"><template #default="{ row }"><el-tag type="danger">{{ syncFailureStageText(row.failed_from_stage) }}</el-tag></template></el-table-column>
              <el-table-column label="已尝试" width="100"><template #default="{ row }">{{ Number(row.attempts || 0) }} / {{ Number(row.max_attempts || 0) }}</template></el-table-column>
              <el-table-column label="上次错误" min-width="280" show-overflow-tooltip><template #default="{ row }">{{ localizeDisplayText(row.last_error, '渠道同步失败') }}</template></el-table-column>
              <el-table-column label="最后更新" width="170"><template #default="{ row }">{{ dateTime(row.updated_at) }}</template></el-table-column>
              <el-table-column v-if="canOperateReservations" label="操作" width="110" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openSyncRetry(row)">继续重试</el-button></template></el-table-column>
            </el-table>
            <div class="reservation-pagination"><el-pagination v-model:current-page="syncFailurePage" v-model:page-size="syncFailurePageSize" :page-sizes="[10,20,40]" layout="total, sizes, prev, pager, next" :total="syncFailureTotal" @current-change="loadSyncFailures" @size-change="changeSyncFailurePageSize" /></div>
          </div>
        </div>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="hotelDialogVisible" :title="hotelForm.id ? '编辑酒店' : '新增酒店'" width="620px">
      <el-form ref="hotelFormRef" :model="hotelForm" :rules="hotelRules" label-position="top">
        <div class="form-grid">
          <el-form-item label="酒店编号" prop="code"><el-input v-model="hotelForm.code" :disabled="Boolean(hotelForm.id)" placeholder="例如 HOTEL01" /></el-form-item>
          <el-form-item label="酒店名称" prop="name"><el-input v-model="hotelForm.name" placeholder="面向游客展示的名称" /></el-form-item>
          <el-form-item label="入住时间" prop="check_in_time"><el-time-select v-model="hotelForm.check_in_time" start="08:00" step="00:30" end="23:30" /></el-form-item>
          <el-form-item label="退房时间" prop="check_out_time"><el-time-select v-model="hotelForm.check_out_time" start="06:00" step="00:30" end="18:00" /></el-form-item>
          <el-form-item label="联系人"><el-input v-model="hotelForm.contact_name" /></el-form-item>
          <el-form-item label="联系电话"><el-input v-model="hotelForm.contact_phone" /></el-form-item>
        </div>
        <el-form-item label="酒店地址"><el-input v-model="hotelForm.address" /></el-form-item>
        <el-form-item label="状态"><el-radio-group v-model="hotelForm.status"><el-radio-button label="active">营业中</el-radio-button><el-radio-button label="suspended">暂停</el-radio-button></el-radio-group></el-form-item>
      </el-form>
      <template #footer><el-button @click="hotelDialogVisible = false">取消</el-button><el-button type="primary" @click="saveHotel">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="roomDialogVisible" :title="roomForm.id ? '编辑房型' : '新增房型'" width="620px">
      <el-form ref="roomFormRef" :model="roomForm" :rules="roomRules" label-position="top">
        <div class="form-grid">
          <el-form-item label="房型编号" prop="code"><el-input v-model="roomForm.code" :disabled="Boolean(roomForm.id)" placeholder="例如 QUEEN" /></el-form-item>
          <el-form-item label="房型名称" prop="name"><el-input v-model="roomForm.name" placeholder="例如 山景大床房" /></el-form-item>
          <el-form-item label="最多入住人数" prop="max_guests"><el-input-number v-model="roomForm.max_guests" :min="1" :max="20" /></el-form-item>
          <el-form-item label="床型"><el-input v-model="roomForm.bed_type" placeholder="例如 1张1.8米大床" /></el-form-item>
        </div>
        <el-form-item label="房型说明"><el-input v-model="roomForm.description" type="textarea" :rows="3" /></el-form-item>
        <el-form-item label="状态"><el-radio-group v-model="roomForm.status"><el-radio-button label="active">启用</el-radio-button><el-radio-button label="suspended">暂停</el-radio-button></el-radio-group></el-form-item>
      </el-form>
      <template #footer><el-button @click="roomDialogVisible = false">取消</el-button><el-button type="primary" @click="saveRoom">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="rateDialogVisible" :title="`${activeRoom?.name || ''} · 价格计划`" width="780px">
      <div class="dialog-toolbar"><span>不同售卖方案共用房型库存。</span><el-button v-if="canWrite" type="primary" @click="openRateEdit()"><el-icon><Plus /></el-icon>新增价格</el-button></div>
      <el-table :data="activeRoom ? (ratePlans[activeRoom.id] || []) : []" empty-text="暂无价格计划">
        <el-table-column prop="code" label="编号" width="120" />
        <el-table-column prop="name" label="名称" min-width="130" />
        <el-table-column label="售价" width="100"><template #default="{ row }">¥{{ money(row.retail_price_cents) }}</template></el-table-column>
        <el-table-column label="结算价" width="100"><template #default="{ row }">¥{{ money(row.settlement_price_cents) }}</template></el-table-column>
        <el-table-column label="早餐" width="80"><template #default="{ row }">{{ row.breakfast_count ? `${row.breakfast_count}份` : '无' }}</template></el-table-column>
        <el-table-column label="操作" width="210"><template #default="{ row }"><el-button link type="primary" @click="openRateCalendar(activeRoom, row)">价格日历</el-button><el-button v-if="canWrite" link type="primary" @click="openRateEdit(row)">编辑</el-button><el-button v-if="canWrite" link type="danger" @click="removeRate(row)">删除</el-button></template></el-table-column>
      </el-table>
    </el-dialog>

    <el-dialog v-model="rateEditVisible" :title="rateForm.id ? '编辑价格计划' : '新增价格计划'" width="620px">
      <el-form ref="rateFormRef" :model="rateForm" :rules="rateRules" label-position="top">
        <div class="form-grid">
          <el-form-item label="价格编号" prop="code"><el-input v-model="rateForm.code" :disabled="Boolean(rateForm.id)" placeholder="例如 WITH_BREAKFAST" /></el-form-item>
          <el-form-item label="价格名称" prop="name"><el-input v-model="rateForm.name" placeholder="例如 含双早" /></el-form-item>
          <el-form-item label="零售价（元）" prop="retail_price"><el-input-number v-model="rateForm.retail_price" :min="0.01" :precision="2" :step="10" /></el-form-item>
          <el-form-item label="结算价（元）" prop="settlement_price"><el-input-number v-model="rateForm.settlement_price" :min="0" :precision="2" :step="10" /></el-form-item>
          <el-form-item label="早餐份数"><el-input-number v-model="rateForm.breakfast_count" :min="0" :max="20" /></el-form-item>
          <el-form-item label="状态"><el-radio-group v-model="rateForm.status"><el-radio-button label="active">启用</el-radio-button><el-radio-button label="suspended">暂停</el-radio-button></el-radio-group></el-form-item>
        </div>
        <el-form-item label="取消规则说明（仅展示）">
          <el-input v-model="rateForm.cancellation_policy" type="textarea" :rows="3" placeholder="例如 入住日前一天18:00前可免费取消" />
          <small class="field-hint">当前仅作为预订说明展示，系统不会据此自动取消或退款。</small>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="rateEditVisible = false">取消</el-button><el-button type="primary" @click="saveRate">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="inventoryBatchVisible" title="批量设置房量" width="560px">
      <el-alert type="info" :closable="false" show-icon :title="`当前房型：${roomTypes.find(row => row.id === inventoryRoomTypeId)?.name || '-'} · ${inventoryRange?.[0] ? formatDate(inventoryRange[0]) : '-'} 至 ${inventoryRange?.[1] ? formatDate(inventoryRange[1]) : '-'}`" />
      <el-form label-position="top" class="batch-form">
        <el-form-item label="应用星期">
          <el-checkbox-group v-model="inventoryBatch.weekdays">
            <el-checkbox v-for="(label, value) in weekdayOptions" :key="value" :label="Number(value)">{{ label }}</el-checkbox>
          </el-checkbox-group>
        </el-form-item>
        <el-form-item label="可售总量（留空表示不修改）"><el-input-number v-model="inventoryBatch.capacity" :min="0" :max="100000" :step="1" /></el-form-item>
        <el-form-item label="销售状态"><el-radio-group v-model="inventoryBatch.closedMode"><el-radio-button label="unchanged">不修改</el-radio-button><el-radio-button label="open">开放销售</el-radio-button><el-radio-button label="closed">关闭销售</el-radio-button></el-radio-group></el-form-item>
      </el-form>
      <small class="field-hint">批量设置只会修改当前日期范围内选中的星期；已预留或已售房量不会被覆盖，新的可售总量不能低于它们的合计。</small>
      <template #footer><el-button @click="inventoryBatchVisible = false">取消</el-button><el-button type="primary" @click="applyInventoryBatch">套用到页面</el-button></template>
    </el-dialog>

    <el-dialog v-model="calendarBatchVisible" title="批量套用日期价格" width="560px">
      <el-alert type="info" :closable="false" show-icon title="批量套用只修改当前价格日历的页面数据，确认无误后请点击“保存价格”提交。" />
      <el-form label-position="top" class="batch-form">
        <el-form-item label="应用星期">
          <el-checkbox-group v-model="calendarBatch.weekdays">
            <el-checkbox v-for="(label, value) in weekdayOptions" :key="value" :label="Number(value)">{{ label }}</el-checkbox>
          </el-checkbox-group>
        </el-form-item>
        <div class="form-grid">
          <el-form-item label="零售价（元）"><el-input-number v-model="calendarBatch.retailPrice" :min="0.01" :precision="2" :step="10" /></el-form-item>
          <el-form-item label="结算价（元）"><el-input-number v-model="calendarBatch.settlementPrice" :min="0" :precision="2" :step="10" /></el-form-item>
        </div>
      </el-form>
      <template #footer><el-button @click="calendarBatchVisible = false">取消</el-button><el-button type="primary" @click="applyCalendarBatch">套用到页面</el-button></template>
    </el-dialog>

    <el-dialog v-model="packageDialogVisible" :title="packageForm.id ? '编辑酒景套餐' : '新增酒景套餐'" width="680px">
      <el-alert type="info" :closable="false" show-icon title="请选择专门用于套餐销售的线上门票。该票种的售价是套餐总售价，结算价是门票部分结算价。" />
      <el-alert v-if="packageForm.locked" type="warning" :closable="false" show-icon title="该套餐已有订单，为保护历史履约，只能调整在售状态。需要更换内容时请新建套餐。" />
      <el-form ref="packageFormRef" :model="packageForm" :rules="packageRules" label-position="top" class="package-form">
        <el-form-item label="套餐门票" prop="product_id">
          <el-select v-model="packageForm.product_id" filterable class="full-width" placeholder="选择专用线上门票" :disabled="packageForm.locked"><el-option v-for="product in packageTicketProducts" :key="product.id" :label="`${product.name} · ¥${Number(product.price || 0).toFixed(2)}`" :value="product.id" /></el-select>
          <small class="field-hint">只展示每份套餐生成独立票码的线上门票。</small>
        </el-form-item>
        <div class="form-grid">
          <el-form-item label="房型" prop="room_type_id"><el-select v-model="packageForm.room_type_id" class="full-width" :disabled="packageForm.locked" @change="packageForm.rate_plan_id = null"><el-option v-for="room in activeRoomTypes" :key="room.id" :label="room.name" :value="room.id" /></el-select></el-form-item>
          <el-form-item label="酒店价格计划" prop="rate_plan_id"><el-select v-model="packageForm.rate_plan_id" class="full-width" :disabled="packageForm.locked"><el-option v-for="rate in packageRatePlans" :key="rate.id" :label="`${rate.name} · ¥${money(rate.retail_price_cents)}`" :value="rate.id" /></el-select></el-form-item>
          <el-form-item label="连住晚数" prop="nights"><el-input-number v-model="packageForm.nights" :min="1" :max="30" :disabled="packageForm.locked" /></el-form-item>
          <el-form-item label="每份套餐房间数" prop="rooms_per_package"><el-input-number v-model="packageForm.rooms_per_package" :min="1" :max="10" :disabled="packageForm.locked" /></el-form-item>
          <el-form-item label="酒店结算价（元）" prop="hotel_settlement_price"><el-input-number v-model="packageForm.hotel_settlement_price" :min="0" :precision="2" :step="10" :disabled="packageForm.locked" /></el-form-item>
          <el-form-item label="状态"><el-radio-group v-model="packageForm.status"><el-radio-button label="offline">先保存下架</el-radio-button><el-radio-button label="online">立即在售</el-radio-button></el-radio-group></el-form-item>
        </div>
        <el-form-item label="预约方式" prop="booking_mode">
          <el-radio-group v-model="packageForm.booking_mode" :disabled="packageForm.locked">
            <el-radio-button label="at_purchase">下单时选择入住日期</el-radio-button>
            <el-radio-button label="after_purchase">先购买，后预约入住</el-radio-button>
          </el-radio-group>
          <small class="field-hint">后预约模式支付时不占用指定日期房量，游客预约成功后才锁定门票日期与酒店房量。</small>
        </el-form-item>
        <div v-if="packageForm.booking_mode === 'after_purchase'" class="form-grid">
          <el-form-item label="预约有效期（天）" prop="voucher_validity_days"><el-input-number v-model="packageForm.voucher_validity_days" :min="1" :max="730" :disabled="packageForm.locked" /></el-form-item>
          <el-form-item label="至少提前（天）" prop="min_advance_days"><el-input-number v-model="packageForm.min_advance_days" :min="0" :max="365" :disabled="packageForm.locked" /></el-form-item>
          <el-form-item label="允许取消后重约次数" prop="max_reschedules"><el-input-number v-model="packageForm.max_reschedules" :min="0" :max="20" :disabled="packageForm.locked" /></el-form-item>
        </div>
      </el-form>
      <template #footer><el-button @click="packageDialogVisible = false">取消</el-button><el-button type="primary" @click="savePackage">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="syncRetryVisible" title="继续重试小红书同步" width="560px" @closed="resetSyncRetry">
      <div v-if="selectedSyncFailure" class="sync-retry-detail">
        <el-descriptions :column="1" border>
          <el-descriptions-item label="业务阶段">{{ syncOperationTypeText(selectedSyncFailure.type) }}</el-descriptions-item>
          <el-descriptions-item label="失败阶段">{{ syncFailureStageText(selectedSyncFailure.failed_from_stage) }}</el-descriptions-item>
          <el-descriptions-item label="订单号">{{ selectedSyncFailure.order_no || '-' }}</el-descriptions-item>
          <el-descriptions-item label="权益编号">{{ selectedSyncFailure.entitlement_no || '-' }}</el-descriptions-item>
          <el-descriptions-item label="上次错误">{{ localizeDisplayText(selectedSyncFailure.last_error, '渠道同步失败') }}</el-descriptions-item>
        </el-descriptions>
        <el-form label-position="top">
          <el-form-item label="重试原因" required>
            <el-input v-model="syncRetryReason" type="textarea" :rows="3" maxlength="255" show-word-limit placeholder="说明已排除的问题或本次继续处理的依据" />
          </el-form-item>
        </el-form>
      </div>
      <template #footer>
        <el-button @click="syncRetryVisible = false">取消</el-button>
        <el-button type="primary" :loading="syncRetrySubmitting" :disabled="!syncRetryReason.trim()" @click="retrySyncFailure">继续重试</el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { Plus } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'
import { activeCapabilitySet, activeSupplierBusinessTypeSet, configuredCapabilitySet, configuredSupplierBusinessTypeSet, readStoredUser } from '@/utils/tenantAccess'
import { hasPermission } from '@/utils/permissions'
import { localizeDisplayText } from '@/utils/localize'

const loading = ref(false)
const hotels = ref<any[]>([])
const selectedHotelId = ref<number | null>(null)
const roomTypes = ref<any[]>([])
const ratePlans = ref<Record<number, any[]>>({})
const activeTab = ref('rooms')
const activeRoom = ref<any>(null)
const hotelDialogVisible = ref(false)
const roomDialogVisible = ref(false)
const rateDialogVisible = ref(false)
const rateEditVisible = ref(false)
const packageDialogVisible = ref(false)
const inventoryBatchVisible = ref(false)
const calendarBatchVisible = ref(false)
const hotelFormRef = ref()
const roomFormRef = ref()
const rateFormRef = ref()
const packageFormRef = ref()
const syncRetryVisible = ref(false)
const syncRetrySubmitting = ref(false)
const selectedSyncFailure = ref<any>(null)
const syncRetryReason = ref('')

const activeBusinessTypes = computed(() => activeSupplierBusinessTypeSet(readStoredUser()))
const configuredBusinessTypes = computed(() => configuredSupplierBusinessTypeSet(readStoredUser()))
const supplierActive = computed(() => activeCapabilitySet(readStoredUser()).has('supplier'))
const hotelBusinessActive = computed(() => activeBusinessTypes.value.has('hotel'))
const historyOnly = computed(() => !supplierActive.value || !hotelBusinessActive.value)
const canWrite = computed(() => supplierActive.value && hotelBusinessActive.value && hasPermission(readStoredUser(), 'catalog.write'))
const showPackages = computed(() => configuredBusinessTypes.value.has('scenic') && configuredBusinessTypes.value.has('hotel'))
const canPackageWrite = computed(() => supplierActive.value && activeBusinessTypes.value.has('scenic') && hotelBusinessActive.value && hasPermission(readStoredUser(), 'catalog.write'))
const canViewReservations = computed(() => hasPermission(readStoredUser(), 'hotel_reservations.read'))
const canOperateReservations = computed(() => configuredCapabilitySet(readStoredUser()).has('supplier') && configuredBusinessTypes.value.has('scenic') && configuredBusinessTypes.value.has('hotel') && hasPermission(readStoredUser(), 'hotel_reservations.write'))
const canExportReservations = computed(() => canViewReservations.value && hasPermission(readStoredUser(), 'hotel_reservations.export'))
const canViewReports = computed(() => hasPermission(readStoredUser(), 'reports.read'))
const selectedHotel = computed(() => hotels.value.find(item => item.id === selectedHotelId.value))

const hotelForm = reactive<any>({ id: 0, code: '', name: '', address: '', contact_name: '', contact_phone: '', check_in_time: '14:00', check_out_time: '12:00', status: 'active' })
const roomForm = reactive<any>({ id: 0, code: '', name: '', max_guests: 2, bed_type: '', description: '', status: 'active' })
const rateForm = reactive<any>({ id: 0, code: '', name: '', retail_price: 0.01, settlement_price: 0, breakfast_count: 0, cancellation_policy: '', status: 'active' })
const packageForm = reactive<any>({ id: 0, product_id: null, room_type_id: null, rate_plan_id: null, nights: 1, rooms_per_package: 1, hotel_settlement_price: 0, booking_mode: 'at_purchase', voucher_validity_days: 90, min_advance_days: 0, max_reschedules: 1, status: 'offline', locked: false })
const hotelRules = { code: [{ required: true, message: '请输入酒店编号', trigger: 'blur' }], name: [{ required: true, message: '请输入酒店名称', trigger: 'blur' }], check_in_time: [{ required: true, message: '请选择入住时间', trigger: 'change' }], check_out_time: [{ required: true, message: '请选择退房时间', trigger: 'change' }] }
const roomRules = { code: [{ required: true, message: '请输入房型编号', trigger: 'blur' }], name: [{ required: true, message: '请输入房型名称', trigger: 'blur' }], max_guests: [{ required: true, type: 'number', min: 1, message: '入住人数至少为1人', trigger: 'change' }] }
const rateRules = { code: [{ required: true, message: '请输入价格编号', trigger: 'blur' }], name: [{ required: true, message: '请输入价格名称', trigger: 'blur' }], retail_price: [{ required: true, type: 'number', min: 0.01, message: '零售价必须大于0', trigger: 'change' }] }
const packageRules = { product_id: [{ required: true, type: 'number', min: 1, message: '请选择套餐门票', trigger: 'change' }], room_type_id: [{ required: true, type: 'number', min: 1, message: '请选择房型', trigger: 'change' }], rate_plan_id: [{ required: true, type: 'number', min: 1, message: '请选择价格计划', trigger: 'change' }], nights: [{ required: true, type: 'number', min: 1, message: '连住晚数至少为1', trigger: 'change' }], rooms_per_package: [{ required: true, type: 'number', min: 1, message: '房间数至少为1', trigger: 'change' }], booking_mode: [{ required: true, message: '请选择预约方式', trigger: 'change' }] }

const packageSection = ref('catalog')
const packageTicketProducts = ref<any[]>([])
const hotelPackages = ref<any[]>([])
const hotelReservations = ref<any[]>([])
const packageEntitlements = ref<any[]>([])
const currentHotelPackages = computed(() => hotelPackages.value.filter(row => row.hotel_id === selectedHotelId.value))
const currentHotelReservations = computed(() => hotelReservations.value)
const reservationStatus = ref('')
const entitlementStatus = ref('')
const reservationOrderNo = ref('')
const reservationPage = ref(1)
const reservationPageSize = ref(20)
const reservationTotal = ref(0)
const entitlementPage = ref(1)
const entitlementTotal = ref(0)
const syncFailures = ref<any[]>([])
const syncFailureLoading = ref(false)
const syncFailureType = ref('')
const syncFailurePage = ref(1)
const syncFailurePageSize = ref(20)
const syncFailureTotal = ref(0)
const packageSummary = ref<any>({})
const reportStart = new Date(); reportStart.setHours(0, 0, 0, 0); reportStart.setDate(1)
const reportEnd = new Date(); reportEnd.setHours(0, 0, 0, 0)
const reportRange = ref<[Date, Date]>([reportStart, reportEnd])
const activeRoomTypes = computed(() => roomTypes.value.filter(row => row.status === 'active'))
const packageRatePlans = computed(() => (ratePlans.value[packageForm.room_type_id] || []).filter(row => row.status === 'active'))

const start = new Date(); start.setHours(0, 0, 0, 0)
const end = new Date(start); end.setDate(end.getDate() + 13)
const inventoryRange = ref<[Date, Date]>([start, end])
const inventoryRoomTypeId = ref<number | null>(null)
const inventoryRows = ref<any[]>([])
const calendarRoomTypeId = ref<number | null>(null)
const calendarRatePlanId = ref<number | null>(null)
const calendarRows = ref<any[]>([])
const calendarStart = new Date(start); const calendarEnd = new Date(start); calendarEnd.setDate(calendarEnd.getDate() + 13)
const calendarRange = ref<[Date, Date]>([calendarStart, calendarEnd])
const weekdayOptions = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']
const inventoryBatch = reactive<any>({ weekdays: [0, 1, 2, 3, 4, 5, 6], capacity: null, closedMode: 'unchanged' })
const calendarBatch = reactive<any>({ weekdays: [0, 1, 2, 3, 4, 5, 6], retailPrice: 0.01, settlementPrice: 0 })
const calendarRatePlans = computed(() => calendarRoomTypeId.value ? (ratePlans.value[calendarRoomTypeId.value] || []) : [])

const loadHotels = async () => {
  loading.value = true
  try {
    const response = await request.get('/hotels')
    hotels.value = response.data.data || []
    if (!hotels.value.some(item => item.id === selectedHotelId.value)) selectedHotelId.value = hotels.value[0]?.id || null
    await loadHotelWorkspace()
    if (showPackages.value) await loadPackageWorkspace()
  } finally { loading.value = false }
}

const loadPackageWorkspace = async () => {
  const [productsResponse, packagesResponse] = await Promise.all([
    canPackageWrite.value ? request.get('/products', { params: { type: 'online', page_size: 100 } }) : Promise.resolve({ data: { data: [] } }),
    request.get('/scenic-hotel-packages'),
  ])
  packageTicketProducts.value = (productsResponse.data.data || []).filter((row: any) => row.type === 'online' && row.code_mode === 'ticket')
  hotelPackages.value = packagesResponse.data.data || []
  await Promise.all([
    canViewReservations.value ? loadReservations() : Promise.resolve(),
    canViewReservations.value ? loadEntitlements() : Promise.resolve(),
    canViewReservations.value && canViewReports.value ? loadPackageSummary() : Promise.resolve(),
  ])
}

const loadReservations = async () => {
  if (!selectedHotelId.value) { hotelReservations.value = []; reservationTotal.value = 0; return }
  const response = await request.get('/scenic-hotel-packages/reservations', { params: { hotel_id: selectedHotelId.value, status: reservationStatus.value || undefined, order_no: reservationOrderNo.value.trim() || undefined, page: reservationPage.value, page_size: reservationPageSize.value } })
  hotelReservations.value = response.data.data || []
  reservationTotal.value = Number(response.data.total || 0)
}

const loadEntitlements = async () => {
  if (!selectedHotelId.value) { packageEntitlements.value = []; entitlementTotal.value = 0; return }
  const response = await request.get('/scenic-hotel-packages/entitlements', { params: { hotel_id: selectedHotelId.value, status: entitlementStatus.value || undefined, order_no: reservationOrderNo.value.trim() || undefined, page: entitlementPage.value, page_size: reservationPageSize.value } })
  packageEntitlements.value = response.data.data || []
  entitlementTotal.value = Number(response.data.total || 0)
}

const loadSyncFailures = async () => {
  if (!canViewReservations.value) { syncFailures.value = []; syncFailureTotal.value = 0; return }
  syncFailureLoading.value = true
  try {
    const response = await request.get('/scenic-hotel-packages/booking-sync-operations/failed', {
      params: { page: syncFailurePage.value, page_size: syncFailurePageSize.value, type: syncFailureType.value || undefined },
    })
    syncFailures.value = response.data.data || []
    syncFailureTotal.value = Number(response.data.total || 0)
  } catch (error) {
    syncFailures.value = []
    syncFailureTotal.value = 0
    throw error
  } finally {
    syncFailureLoading.value = false
  }
}

const searchSyncFailures = async () => { syncFailurePage.value = 1; await loadSyncFailures() }
const changeSyncFailurePageSize = async () => { syncFailurePage.value = 1; await loadSyncFailures() }
const changePackageSection = async (section: string | number | boolean | undefined) => {
  if (section === 'sync-failures' && canViewReservations.value) await loadSyncFailures()
}
const openSyncRetry = (row: any) => {
  selectedSyncFailure.value = row
  syncRetryReason.value = ''
  syncRetryVisible.value = true
}
const resetSyncRetry = () => {
  selectedSyncFailure.value = null
  syncRetryReason.value = ''
}
const retrySyncFailure = async () => {
  const reason = syncRetryReason.value.trim()
  if (!selectedSyncFailure.value || !reason || !canOperateReservations.value) return
  syncRetrySubmitting.value = true
  try {
    await request.post(`/scenic-hotel-packages/booking-sync-operations/${selectedSyncFailure.value.id}/retry`, { reason })
    syncRetryVisible.value = false
    ElMessage.success('已重新加入同步队列')
    await loadSyncFailures()
  } finally {
    syncRetrySubmitting.value = false
  }
}

const loadPackageSummary = async () => {
  if (!selectedHotelId.value || !reportRange.value?.length || !canViewReports.value) return
  const response = await request.get('/scenic-hotel-packages/business-summary', { params: { hotel_id: selectedHotelId.value, start_date: formatDate(reportRange.value[0]), end_date: formatDate(reportRange.value[1]) } })
  packageSummary.value = response.data || {}
}

const searchReservations = async () => { reservationPage.value = 1; entitlementPage.value = 1; await Promise.all([loadReservations(), loadEntitlements()]) }
const changeReservationPageSize = async () => { reservationPage.value = 1; entitlementPage.value = 1; await Promise.all([loadReservations(), loadEntitlements()]) }

const setReservationStatus = async (row: any, status: string, requireReason = false) => {
  let reason = ''
  if (requireReason) {
    const result = await ElMessageBox.prompt('请填写原因，操作会写入审计记录。', status === 'no_show' ? '登记未到店' : '纠正住宿状态', { inputValidator: value => Boolean(String(value || '').trim()) || '必须填写原因' })
    reason = String(result.value || '').trim()
  }
  await request.patch(`/scenic-hotel-packages/reservations/${row.id}/status`, { status, reason })
  ElMessage.success('住宿履约状态已更新')
  await Promise.all([loadReservations(), canViewReports.value ? loadPackageSummary() : Promise.resolve()])
}

const correctReservationStatus = async (row: any) => {
  const target = row.status === 'checked_in' ? 'confirmed' : row.status === 'checked_out' ? 'checked_in' : 'confirmed'
  await setReservationStatus(row, target, true)
}

const exportReservations = async () => {
  const response = await request.get('/scenic-hotel-packages/reservations/export', { params: { hotel_id: selectedHotelId.value, status: reservationStatus.value || undefined, order_no: reservationOrderNo.value.trim() || undefined }, responseType: 'blob' })
  const url = URL.createObjectURL(response.data)
  const link = document.createElement('a'); link.href = url; link.download = `${selectedHotel.value?.name || '酒店'}-住宿名单.csv`; link.click(); URL.revokeObjectURL(url)
}

const loadHotelWorkspace = async () => {
  if (!selectedHotelId.value) { roomTypes.value = []; inventoryRows.value = []; calendarRows.value = []; return }
  const response = await request.get(`/hotels/${selectedHotelId.value}/room-types`)
  roomTypes.value = response.data.data || []
  const plans = await Promise.all(roomTypes.value.map(async room => [room.id, (await request.get(`/hotels/${selectedHotelId.value}/room-types/${room.id}/rate-plans`)).data.data || []]))
  ratePlans.value = Object.fromEntries(plans)
  if (!roomTypes.value.some(item => item.id === inventoryRoomTypeId.value)) inventoryRoomTypeId.value = roomTypes.value[0]?.id || null
  if (!roomTypes.value.some(item => item.id === calendarRoomTypeId.value)) calendarRoomTypeId.value = roomTypes.value[0]?.id || null
  if (!calendarRatePlans.value.some(item => item.id === calendarRatePlanId.value)) calendarRatePlanId.value = calendarRatePlans.value[0]?.id || null
  if (activeTab.value === 'inventory') await loadInventory()
  if (activeTab.value === 'pricing') await loadRatePlanCalendar()
}

const switchHotel = async () => {
  reservationPage.value = 1
  await loadHotelWorkspace()
  if (showPackages.value) await loadPackageWorkspace()
}
const changeHotelTab = async (tab: string | number) => {
  if (tab === 'pricing') {
    if (!calendarRoomTypeId.value) calendarRoomTypeId.value = roomTypes.value[0]?.id || null
    if (!calendarRatePlanId.value) calendarRatePlanId.value = calendarRatePlans.value[0]?.id || null
    await loadRatePlanCalendar()
  }
}

const openHotelDialog = (row?: any) => {
  Object.assign(hotelForm, row ? { ...row } : { id: 0, code: '', name: '', address: '', contact_name: '', contact_phone: '', check_in_time: '14:00', check_out_time: '12:00', status: 'active' })
  hotelDialogVisible.value = true
}
const saveHotel = async () => {
  await hotelFormRef.value?.validate()
  const payload = { code: hotelForm.code, name: hotelForm.name, address: hotelForm.address, contact_name: hotelForm.contact_name, contact_phone: hotelForm.contact_phone, check_in_time: hotelForm.check_in_time, check_out_time: hotelForm.check_out_time, status: hotelForm.status }
  if (hotelForm.id) await request.put(`/hotels/${hotelForm.id}`, payload)
  else await request.post('/hotels', payload)
  hotelDialogVisible.value = false; ElMessage.success('酒店资料已保存'); await loadHotels()
}
const removeHotel = async () => {
  if (!selectedHotel.value) return
  await ElMessageBox.confirm('仅空酒店可以删除；已有房型时请改为暂停。', '删除酒店', { type: 'warning' })
  await request.delete(`/hotels/${selectedHotel.value.id}`); ElMessage.success('酒店已删除'); await loadHotels()
}

const openRoomDialog = (row?: any) => {
  Object.assign(roomForm, row ? { ...row } : { id: 0, code: '', name: '', max_guests: 2, bed_type: '', description: '', status: 'active' })
  roomDialogVisible.value = true
}
const saveRoom = async () => {
  await roomFormRef.value?.validate()
  const payload = { code: roomForm.code, name: roomForm.name, max_guests: roomForm.max_guests, bed_type: roomForm.bed_type, description: roomForm.description, status: roomForm.status }
  if (roomForm.id) await request.put(`/hotels/${selectedHotelId.value}/room-types/${roomForm.id}`, payload)
  else await request.post(`/hotels/${selectedHotelId.value}/room-types`, payload)
  roomDialogVisible.value = false; ElMessage.success('房型已保存'); await loadHotelWorkspace()
}
const removeRoom = async (row: any) => {
  await ElMessageBox.confirm('仅没有价格和房量记录的房型可以删除。', '删除房型', { type: 'warning' })
  await request.delete(`/hotels/${selectedHotelId.value}/room-types/${row.id}`); ElMessage.success('房型已删除'); await loadHotelWorkspace()
}

const openRateDialog = (room: any) => { activeRoom.value = room; rateDialogVisible.value = true }
const openRateCalendar = (room: any, rate: any) => {
  rateDialogVisible.value = false
  calendarRoomTypeId.value = room.id
  calendarRatePlanId.value = rate.id
  activeTab.value = 'pricing'
}
const openRateEdit = (row?: any) => {
  Object.assign(rateForm, row ? { ...row, retail_price: row.retail_price_cents / 100, settlement_price: row.settlement_price_cents / 100 } : { id: 0, code: '', name: '', retail_price: 0.01, settlement_price: 0, breakfast_count: 0, cancellation_policy: '', status: 'active' })
  rateEditVisible.value = true
}
const saveRate = async () => {
  await rateFormRef.value?.validate()
  if (rateForm.settlement_price > rateForm.retail_price) { ElMessage.warning('结算价不能高于零售价'); return }
  const payload = { code: rateForm.code, name: rateForm.name, retail_price_cents: Math.round(rateForm.retail_price * 100), settlement_price_cents: Math.round(rateForm.settlement_price * 100), breakfast_count: rateForm.breakfast_count, cancellation_policy: rateForm.cancellation_policy, status: rateForm.status }
  const base = `/hotels/${selectedHotelId.value}/room-types/${activeRoom.value.id}/rate-plans`
  if (rateForm.id) await request.put(`${base}/${rateForm.id}`, payload); else await request.post(base, payload)
  rateEditVisible.value = false; ElMessage.success('价格计划已保存'); await loadHotelWorkspace()
}
const removeRate = async (row: any) => {
  await ElMessageBox.confirm('确认删除该价格计划？', '删除价格计划', { type: 'warning' })
  await request.delete(`/hotels/${selectedHotelId.value}/room-types/${activeRoom.value.id}/rate-plans/${row.id}`); ElMessage.success('价格计划已删除'); await loadHotelWorkspace()
}

const openPackageDialog = (row?: any) => {
  Object.assign(packageForm, row ? { id: row.id, product_id: row.product_id, room_type_id: row.room_type_id, rate_plan_id: row.rate_plan_id, nights: row.nights, rooms_per_package: row.rooms_per_package, hotel_settlement_price: row.hotel_settlement_price_cents / 100, booking_mode: row.booking_mode || 'at_purchase', voucher_validity_days: row.voucher_validity_days || 90, min_advance_days: row.min_advance_days || 0, max_reschedules: row.max_reschedules || 0, status: row.status, locked: Number(row.reservation_count || 0) + Number(row.entitlement_count || 0) > 0 } : { id: 0, product_id: null, room_type_id: activeRoomTypes.value[0]?.id || null, rate_plan_id: null, nights: 1, rooms_per_package: 1, hotel_settlement_price: 0, booking_mode: 'at_purchase', voucher_validity_days: 90, min_advance_days: 0, max_reschedules: 1, status: 'offline', locked: false })
  packageDialogVisible.value = true
}
const savePackage = async () => {
  await packageFormRef.value?.validate()
  const product = packageTicketProducts.value.find(row => row.id === packageForm.product_id)
  const ticketSettlement = Number(product?.settlement_price || 0)
  if (ticketSettlement + packageForm.hotel_settlement_price > Number(product?.price || 0)) { ElMessage.warning('门票与酒店结算价之和不能高于套餐售价'); return }
  const payload = { product_id: packageForm.product_id, hotel_id: selectedHotelId.value, room_type_id: packageForm.room_type_id, rate_plan_id: packageForm.rate_plan_id, nights: packageForm.nights, rooms_per_package: packageForm.rooms_per_package, hotel_settlement_price_cents: Math.round(packageForm.hotel_settlement_price * 100), booking_mode: packageForm.booking_mode, voucher_validity_days: packageForm.booking_mode === 'after_purchase' ? packageForm.voucher_validity_days : 0, min_advance_days: packageForm.booking_mode === 'after_purchase' ? packageForm.min_advance_days : 0, max_reschedules: packageForm.booking_mode === 'after_purchase' ? packageForm.max_reschedules : 0, status: packageForm.status }
  if (packageForm.id) await request.put(`/scenic-hotel-packages/${packageForm.id}`, payload); else await request.post('/scenic-hotel-packages', payload)
  packageDialogVisible.value = false; ElMessage.success('酒景套餐已保存'); await loadPackageWorkspace()
}
const removePackage = async (row: any) => {
  await ElMessageBox.confirm('已有订单的套餐不能删除，只能下架。确认继续？', '删除酒景套餐', { type: 'warning' })
  await request.delete(`/scenic-hotel-packages/${row.id}`); ElMessage.success('酒景套餐已删除'); await loadPackageWorkspace()
}

const openInventory = async (room: any) => { inventoryRoomTypeId.value = room.id; activeTab.value = 'inventory'; await loadInventory() }
const loadInventory = async () => {
  if (!selectedHotelId.value || !inventoryRoomTypeId.value || !inventoryRange.value?.length) { inventoryRows.value = []; return }
  const from = formatDate(inventoryRange.value[0]); const to = formatDate(inventoryRange.value[1])
  const response = await request.get(`/hotels/${selectedHotelId.value}/room-types/${inventoryRoomTypeId.value}/inventory`, { params: { start_date: from, end_date: to } })
  const existing = new Map((response.data.data || []).map((row: any) => [String(row.stay_date).slice(0, 10), row]))
  const rows: any[] = []
  for (let cursor = new Date(inventoryRange.value[0]); cursor <= inventoryRange.value[1]; cursor.setDate(cursor.getDate() + 1)) {
    const date = formatDate(cursor); const row: any = existing.get(date)
    rows.push({ stay_date: date, capacity: row?.capacity || 0, reserved: row?.reserved || 0, sold: row?.sold || 0, closed: Boolean(row?.closed) })
  }
  inventoryRows.value = rows
}
const saveInventory = async () => {
  await request.put(`/hotels/${selectedHotelId.value}/room-types/${inventoryRoomTypeId.value}/inventory`, { items: inventoryRows.value.map(row => ({ stay_date: row.stay_date, capacity: row.capacity, closed: row.closed })) })
  ElMessage.success('每日房量已保存'); await loadInventory()
}

const openInventoryBatchDialog = () => {
  if (!inventoryRows.value.length) return
  inventoryBatch.weekdays = [0, 1, 2, 3, 4, 5, 6]
  inventoryBatch.capacity = null
  inventoryBatch.closedMode = 'unchanged'
  inventoryBatchVisible.value = true
}
const applyInventoryBatch = () => {
  const weekdays = new Set((inventoryBatch.weekdays || []).map((value: number) => Number(value)))
  const targets = inventoryRows.value.filter(row => weekdays.has(new Date(`${row.stay_date}T00:00:00`).getDay()))
  if (!targets.length) { ElMessage.warning('请至少选择一个应用星期'); return }
  const capacity = inventoryBatch.capacity === null || inventoryBatch.capacity === undefined || inventoryBatch.capacity === '' ? null : Number(inventoryBatch.capacity)
  if (capacity !== null && targets.some(row => capacity < Number(row.reserved || 0) + Number(row.sold || 0))) {
    ElMessage.warning('批量可售总量不能低于已预留和已售房量')
    return
  }
  targets.forEach(row => {
    if (capacity !== null) row.capacity = capacity
    if (inventoryBatch.closedMode === 'open') row.closed = false
    if (inventoryBatch.closedMode === 'closed') row.closed = true
  })
  inventoryBatchVisible.value = false
  ElMessage.success(`已套用 ${targets.length} 个日期，请确认后保存房量`)
}

const loadRatePlanCalendar = async () => {
  if (!selectedHotelId.value || !calendarRoomTypeId.value || !calendarRatePlanId.value || !calendarRange.value?.length) { calendarRows.value = []; return }
  const from = formatDate(calendarRange.value[0]); const to = formatDate(calendarRange.value[1])
  const response = await request.get(`/hotels/${selectedHotelId.value}/room-types/${calendarRoomTypeId.value}/rate-plans/${calendarRatePlanId.value}/calendar`, { params: { start_date: from, end_date: to } })
  calendarRows.value = (response.data.data || []).map((row: any) => ({ ...row, retail_price: Number(row.retail_price_cents || 0) / 100, settlement_price: Number(row.settlement_price_cents || 0) / 100 }))
}
const changeCalendarRoomType = async () => {
  calendarRatePlanId.value = calendarRatePlans.value[0]?.id || null
  await loadRatePlanCalendar()
}
const saveRatePlanCalendar = async () => {
  if (!calendarRows.value.length || !calendarRoomTypeId.value || !calendarRatePlanId.value) return
  const items: any[] = []
  for (const row of calendarRows.value) {
    const retail = Math.round(Number(row.retail_price || 0) * 100)
    const settlement = Math.round(Number(row.settlement_price || 0) * 100)
    if (retail <= 0 || settlement < 0 || settlement > retail) { ElMessage.warning(`${row.stay_date} 的价格无效，结算价不能高于零售价`); return }
    items.push({ stay_date: row.stay_date, retail_price_cents: retail, settlement_price_cents: settlement, clear_override: retail === Number(row.base_retail_price_cents) && settlement === Number(row.base_settlement_price_cents) })
  }
  await request.put(`/hotels/${selectedHotelId.value}/room-types/${calendarRoomTypeId.value}/rate-plans/${calendarRatePlanId.value}/calendar`, { items })
  ElMessage.success('价格日历已保存')
  await loadRatePlanCalendar()
}
const openCalendarBatchDialog = () => {
  if (!calendarRows.value.length) return
  const first = calendarRows.value[0]
  calendarBatch.weekdays = [0, 1, 2, 3, 4, 5, 6]
  calendarBatch.retailPrice = Number(first.retail_price || 0)
  calendarBatch.settlementPrice = Number(first.settlement_price || 0)
  calendarBatchVisible.value = true
}
const applyCalendarBatch = () => {
  const retail = Math.round(Number(calendarBatch.retailPrice || 0) * 100)
  const settlement = Math.round(Number(calendarBatch.settlementPrice || 0) * 100)
  if (retail <= 0 || settlement < 0 || settlement > retail) { ElMessage.warning('结算价不能高于零售价，且零售价必须大于0'); return }
  const weekdays = new Set((calendarBatch.weekdays || []).map((value: number) => Number(value)))
  const targets = calendarRows.value.filter(row => weekdays.has(new Date(`${row.stay_date}T00:00:00`).getDay()))
  if (!targets.length) { ElMessage.warning('请至少选择一个应用星期'); return }
  targets.forEach(row => {
    row.retail_price = retail / 100
    row.settlement_price = settlement / 100
    row.has_override = retail !== Number(row.base_retail_price_cents) || settlement !== Number(row.base_settlement_price_cents)
    row.source = row.has_override ? 'override' : 'base'
  })
  calendarBatchVisible.value = false
  ElMessage.success(`已套用 ${targets.length} 个日期，请确认后保存价格`)
}

const formatDate = (value: Date) => `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, '0')}-${String(value.getDate()).padStart(2, '0')}`
const weekday = (value: string) => `周${'日一二三四五六'[new Date(`${value}T00:00:00`).getDay()]}`
const disablePastDate = (value: Date) => value.getTime() < start.getTime()
const money = (cents: number) => (Number(cents || 0) / 100).toFixed(2)
const shortDate = (value: string) => String(value || '').slice(0, 10)
const reservationStatusText = (status: string) => ({ reserved: '待支付', confirmed: '待入住', checked_in: '已入住', checked_out: '已离店', no_show: '未到店', cancelled: '已取消', refunded: '已退款' } as Record<string, string>)[status] || status
const reservationStatusType = (status: string) => ({ reserved: 'warning', confirmed: 'primary', checked_in: 'success', checked_out: 'info', no_show: 'warning', cancelled: 'info', refunded: 'danger' } as Record<string, string>)[status] || 'info'
const entitlementStatusText = (status: string) => ({ pending_booking: '待预约', booking_pending: '预约处理中', booked: '已预约', cancel_pending: '取消处理中', cancelled: '已关闭', refunded: '已退款', expired: '已过期' } as Record<string, string>)[status] || status
const entitlementStatusType = (status: string) => ({ pending_booking: 'warning', booking_pending: 'primary', booked: 'success', cancel_pending: 'warning', cancelled: 'info', refunded: 'danger', expired: 'info' } as Record<string, string>)[status] || 'info'
const syncOperationTypeText = (type: string) => ({ book: '预约确认', revoke: '取消预约', refund: '退款同步' } as Record<string, string>)[type] || '同步处理'
const syncFailureStageText = (stage: string) => ({ pending: '等待发送平台', remote_succeeded: '平台已成功，等待本地收尾', confirm_pending: '平台确认后本地落地', compensation_pending: '撤销平台预约并回退本地占用' } as Record<string, string>)[stage] || '同步处理'
const dateTime = (value: string) => {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString('zh-CN', { hour12: false })
}

onMounted(loadHotels)
</script>

<style scoped>
.hotel-page { display: flex; flex-direction: column; gap: 16px; }
.hotel-switcher { display: grid; grid-template-columns: auto minmax(220px, 340px) 1fr auto; align-items: center; gap: 14px; padding: 14px 16px; background: #fff; border: 1px solid var(--ui-border); border-radius: var(--ui-radius); }
.hotel-switcher-label { color: var(--ui-text-secondary); font-size: 13px; font-weight: 650; }
.hotel-summary { min-width: 0; display: flex; align-items: center; gap: 10px; color: var(--ui-text-secondary); }
.hotel-summary strong { color: var(--ui-text); }
.hotel-summary span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.hotel-switcher-actions, .inventory-filters, .dialog-toolbar { display: flex; align-items: center; gap: 8px; }
.hotel-tabs { margin-top: 2px; }
.section-toolbar { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 14px; }
.section-toolbar h2 { margin: 0; font-size: 18px !important; }
.section-toolbar p { margin: 4px 0 0; font-size: 13px; }
.inventory-toolbar { align-items: flex-end; }
.inventory-filters { flex-wrap: wrap; justify-content: flex-end; }
.inventory-filters .el-select { width: 180px; }
.calendar-panel { margin-top: 14px; }
.batch-form { margin-top: 18px; }
.dialog-toolbar { justify-content: space-between; margin-bottom: 14px; color: var(--ui-text-secondary); }
.package-section { margin-bottom: 14px; }
.reservation-workspace { display: flex; flex-direction: column; gap: 14px; }
.package-metrics { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 1px; overflow: hidden; border: 1px solid var(--ui-border); border-radius: var(--ui-radius); background: var(--ui-border); }
.package-metrics > div { display: flex; min-width: 0; flex-direction: column; gap: 5px; padding: 16px; background: #fff; }
.package-metrics span, .package-metrics small { color: var(--ui-text-secondary); }
.package-metrics strong { font-size: 22px; }
.reservation-toolbar { display: grid; grid-template-columns: minmax(160px, 240px) 140px minmax(260px, 360px) auto auto; gap: 8px; }
.sync-failure-toolbar { display: flex; align-items: center; gap: 8px; }
.sync-failure-toolbar .el-select { width: 180px; }
.sync-retry-detail { display: flex; flex-direction: column; gap: 18px; }
.reservation-pagination { display: flex; justify-content: flex-end; padding: 14px 0 2px; }
.package-form { margin-top: 18px; }
.full-width { width: 100%; }
.field-hint { display: block; margin-top: 6px; color: var(--ui-text-secondary); line-height: 1.5; }
.form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0 16px; }
@media (max-width: 900px) {
  .hotel-switcher { grid-template-columns: 1fr; }
  .hotel-switcher-label { display: none; }
  .hotel-summary { flex-wrap: wrap; }
  .hotel-switcher-actions { justify-content: flex-start; }
  .inventory-toolbar { align-items: stretch; flex-direction: column; }
  .inventory-filters { align-items: stretch; flex-direction: column; }
  .inventory-filters .el-select, .inventory-filters :deep(.el-date-editor) { width: 100% !important; }
  .package-metrics { grid-template-columns: 1fr 1fr; }
  .reservation-toolbar { grid-template-columns: 1fr 1fr; }
  .reservation-toolbar :deep(.el-date-editor) { width: 100%; }
}
@media (max-width: 640px) {
  .form-grid, .package-metrics, .reservation-toolbar { grid-template-columns: 1fr; }
  .sync-failure-toolbar { align-items: stretch; flex-direction: column; }
  .sync-failure-toolbar .el-select { width: 100%; }
}
</style>
