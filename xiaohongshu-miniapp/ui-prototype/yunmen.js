// Throwaway local interaction model. Never connect these sample orders to production.
const photos={waterfall:'https://www.sg.gov.cn/img/0/146/146113/2424533.jpg',bridge:'https://www.sg.gov.cn/img/0/146/146115/2424533.jpg'};
const paths={back:'<path d="m14 5-7 7 7 7"/>',next:'<path d="m10 5 7 7-7 7"/>',close:'<path d="m6 6 12 12M18 6 6 18"/>',minus:'<path d="M5 12h14"/>',plus:'<path d="M5 12h14M12 5v14"/>',pin:'<path d="M19 10c0 5-7 11-7 11S5 15 5 10a7 7 0 1 1 14 0Z"/><circle cx="12" cy="10" r="2"/>',ticket:'<path d="M3 5h18v5a2 2 0 0 0 0 4v5H3v-5a2 2 0 0 0 0-4Z"/><path d="M8 5v3m0 3v2m0 3v3"/>',orders:'<path d="M6 3h12v18l-3-2-3 2-3-2-3 2Z"/><path d="M9 8h6M9 12h6"/>',check:'<circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/>',clock:'<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',code:'<path d="M8 3H3v5m13-5h5v5M3 16v5h5m13-5v5h-5"/><rect x="7" y="7" width="10" height="10" rx="1"/>'};
const icon=n=>`<svg viewBox="0 0 24 24" aria-hidden="true">${paths[n]}</svg>`;
const products=[{name:'云门山景区成人门票',short:'成人门票',desc:'山林漫步，感受云门山水',price:80,image:photos.waterfall},{name:'门票＋游玩项目套餐',short:'游玩套餐',desc:'景区门票与项目组合 · 示例',price:128,image:photos.bridge}];
const screens=[['home','选购门票'],['detail','商品详情'],['checkout','确认订单'],['orders','我的订单'],['ticket','票券详情']];
const urlParams=new URLSearchParams(location.search);let screen=screens.some(x=>x[0]===urlParams.get('screen'))?urlParams.get('screen'):'home';
let selected=0,quantity=1,category='全部',orderTab='全部',order=null,ticketIndex=1,error='',chosenDate='',calendarOpen=false,calendarMonth=null;
const today=new Date();const dateISO=d=>`${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,'0')}-${String(d.getDate()).padStart(2,'0')}`;
const tomorrow=new Date(today.getFullYear(),today.getMonth(),today.getDate()+1);const todayISO=dateISO(today),tomorrowISO=dateISO(tomorrow);const shortDate=s=>`${Number(s.slice(5,7))}月${Number(s.slice(8))}日`;
const money=(v,unit='')=>`<span class="money"><small>¥</small>${v}<em>${unit}</em></span>`;
const product=()=>products[selected];const amount=()=>product().price*quantity;
const nav=()=>`<nav class="nav-bottom" aria-label="小程序导航"><button data-go="home" class="${screen==='home'?'active':''}">${icon('ticket')}购票</button><button data-go="orders" class="${screen==='orders'?'active':''}">${icon('orders')}订单</button></nav>`;
const primary=(text,action)=>`<button class="primary" data-action="${action}">${text}</button>`;
function productMini(p=product(),q=quantity){return `<div class="checkout-product"><img src="${p.image}" alt="云门山景区实景"><div><h3>${p.name}</h3><p class="product-note">${p.short} · ${q} 张</p></div></div>`}
function dateFields(){return `<div class="form-label">游玩日期</div><div class="date-options"><button class="date-option ${chosenDate===todayISO?'selected':''}" data-date="${todayISO}">今天<strong>${shortDate(todayISO)}</strong></button><button class="date-option ${chosenDate===tomorrowISO?'selected':''}" data-date="${tomorrowISO}">明天<strong>${shortDate(tomorrowISO)}</strong></button><button class="date-option more-date ${chosenDate&&![todayISO,tomorrowISO].includes(chosenDate)?'selected':''}" data-action="more-date" aria-expanded="${calendarOpen}" aria-controls="calendar-panel">更多日期<strong>${chosenDate&&![todayISO,tomorrowISO].includes(chosenDate)?shortDate(chosenDate):'选择日期'}</strong></button></div>${calendarOpen?calendarView():''}<div class="quantity-row"><span>购买数量</span><div class="stepper"><button aria-label="减少数量" data-action="minus" ${quantity===1?'disabled':''}>${icon('minus')}</button><span>${quantity}</span><button aria-label="增加数量" data-action="plus" ${quantity===10?'disabled':''}>${icon('plus')}</button></div></div>`}
function calendarView(){
 const year=calendarMonth.getFullYear(),month=calendarMonth.getMonth();
 const offset=(calendarMonth.getDay()+6)%7,days=new Date(year,month+1,0).getDate();
 const cells=Array.from({length:offset},()=>'<span></span>');
 for(let day=1;day<=days;day++){const iso=dateISO(new Date(year,month,day));cells.push(`<button class="calendar-day ${chosenDate===iso?'selected':''}" data-date="${iso}" aria-label="${year}年${month+1}月${day}日" aria-pressed="${chosenDate===iso}" ${iso<todayISO?'disabled':''}>${day}</button>`)}
 const earliest=year===today.getFullYear()&&month===today.getMonth();
 return `<div class="calendar-panel" id="calendar-panel"><div class="calendar-heading"><button aria-label="上个月" data-action="prev-month" ${earliest?'disabled':''}>${icon('back')}</button><strong aria-live="polite">${year}年${month+1}月</strong><button aria-label="下个月" data-action="next-month">${icon('next')}</button></div><div class="calendar-grid">${['一','二','三','四','五','六','日'].map(d=>'<span class="weekday">'+d+'</span>').join('')}${cells.join('')}</div></div>`;
}
function go(next){closeSheet();screen=next;error='';history.replaceState(null,'',`?screen=${screen}`);render()}
function render(){
 document.querySelector('#screens').innerHTML=screens.map(([s,label],i)=>`<button class="${screen===s?'active':''}" data-go="${s}"><span>0${i+1}</span>${label}</button>`).join('');
 document.querySelector('#back').innerHTML=screen==='home'?'':icon('back');document.querySelector('#back').style.visibility=screen==='home'?'hidden':'visible';
 let html='',foot='';const p=product();
 if(screen==='home'){
 html=`<img class="hero-photo" src="${photos.waterfall}" alt="云门山云上飞瀑、玻璃桥与山林"><section class="intro"><div class="intro-line"><div><h2>山水之间，尽兴游玩</h2><div class="location">${icon('pin')}韶关 · 乳源云门山</div></div></div></section><div class="categories">${['全部','门票','游玩套餐'].map(c=>`<button data-category="${c}" class="${category===c?'active':''}">${c}</button>`).join('')}</div><div class="list">${products.map((x,i)=>(category==='门票'&&i!==0)||(category==='游玩套餐'&&i!==1)?'':`<button class="product-row" data-product="${i}"><img src="${x.image}" alt="${i?'云门山玻璃桥':'云门山飞瀑'}"><div class="product-info"><h3>${x.name}</h3><p class="product-note">${x.desc}</p><div class="conditions"><span>需选择日期</span><span>电子凭证</span></div><div class="product-bottom">${money(x.price)}<span class="small-cta">查看详情</span></div></div></button>`).join('')}</div><p class="end-note">已经选好？购票后可在「订单」中查看</p>`;foot=nav();
 }else if(screen==='detail'){
 html=`<img class="detail-photo" src="${p.image}" alt="云门山景区实景"><section class="section"><h2>${p.name}</h2><p class="detail-summary">${p.desc}</p><div>${money(p.price,'/ 张')}</div></section><section class="section"><h3>购买须知</h3><div class="rule-row"><span>使用日期</span><span>按预订时选择的日期使用</span></div><div class="rule-row"><span>入园凭证</span><span>支付后在订单内查看电子凭证</span></div><div class="rule-row"><span>退改说明</span><span>以实际商品规则与订单状态为准</span></div></section><section class="section"><h3>费用包含</h3><p class="purchase-note">${selected?'景区门票与游玩项目组合。具体包含项目、适用人群与限制条件，以正式商品信息为准。':'云门山景区成人门票。具体开放区域、适用人群与限制条件，以正式商品信息为准。'}</p></section>`;foot=`<div class="footer"><div class="footer-line"><div class="money-wrap"><div class="caption">售价</div>${money(p.price)}</div>${primary('选择日期 · 预订','choose')}</div></div>`;
 }else if(screen==='checkout'){
 html=`<div class="heading"><h2>确认订单</h2></div><section class="section">${productMini()}</section><section class="section">${dateFields()}</section><section class="section"><h3>购买须知</h3><p class="purchase-note">请确认游玩日期与数量。支付成功后可在订单中查看凭证，退改按商品实际规则执行。</p><div class="summary"><span>门票 ${p.price} 元 × ${quantity}</span><span>¥${amount().toFixed(2)}</span></div>${error?`<div class="inline-error" role="alert">${error}</div>`:''}</section>`;foot=`<div class="footer"><div class="footer-line"><div><div class="caption">应付总额</div>${money(amount())}</div>${primary('确认并支付','pay')}</div></div>`;
 }else if(screen==='orders'){
 html=`<div class="heading"><h2>我的订单</h2></div><div class="categories">${['全部','待支付','已支付'].map(t=>`<button data-order-tab="${t}" class="${orderTab===t?'active':''}">${t}</button>`).join('')}</div>`;
 if(!order||(orderTab!=='全部'&&orderTab!==(order.paid?'已支付':'待支付'))){html+='<div class="empty">暂无相关订单<br><button class="plain-link" data-go="home">去选购门票</button></div>';}else{html+=`<section class="section"><div class="order-top"><span>云门山景区</span><span class="status">${order.paid?'已支付':'待支付'}</span></div>${productMini(products[order.product],order.quantity)}<p class="product-note" style="margin-top:14px">游玩日期 ${order.date}</p><div class="order-total">${order.paid?'实付':'待支付'}${money(order.amount.toFixed(2))}</div><div class="order-action"><button class="outline" data-go="ticket">${order.paid?'查看凭证':'继续支付'}</button></div></section>`}foot=nav();
 }else{
 if(!order){html='<div class="empty">还没有订单<br><button class="plain-link" data-go="home">先体验一次购票</button></div>';foot=nav();}
 else{html=`<div class="status-heading">${icon(order.paid?'check':'clock')}<div><h2>${order.paid?'已支付，查看凭证':'订单待支付'}</h2><p>${order.paid?'使用前请核对日期与商品规则':'订单已保留，无需重复下单'}</p></div></div><section class="ticket"><h3>${products[order.product].name}</h3><p class="ticket-sub">游玩日期 ${order.date} · ${order.quantity} 张</p>${order.paid?`<div class="credential"><div class="code-placeholder">${icon('code')}电子凭证展示区</div><p>设计示意 · 非真实票码，不可用于入园</p><div class="ticket-switch"><button aria-label="上一张凭证" data-action="prev-ticket" ${ticketIndex===1?'disabled':''}>${icon('back')}</button><span>第 ${ticketIndex} / ${order.quantity} 张</span><button aria-label="下一张凭证" data-action="next-ticket" ${ticketIndex===order.quantity?'disabled':''}>${icon('next')}</button></div></div>`:`<div style="padding:24px 0"><div class="caption">待支付金额</div>${money(order.amount.toFixed(2))}</div>`}<div class="meta"><span>订单编号</span><span>DEMO-20260907-001</span></div><div class="meta"><span>${order.paid?'实付金额':'订单金额'}</span><span>¥${order.amount.toFixed(2)}</span></div>${error?`<div class="inline-error" role="alert">${error}</div>`:''}</section><p class="end-note">凭证结构仅为示意，正式版遵循现有票权规则</p>`;foot=`<div class="footer">${order.paid?'':primary('继续支付','pay')}<div class="text-center"><button class="plain-link" data-go="orders">查看全部订单</button></div></div>`;}
 }
 document.querySelector('#content').innerHTML=`<div class="screen">${html}</div>`;document.querySelector('#footer').innerHTML=foot;document.querySelector('#content').scrollTop=0;
}
function closeSheet(){calendarOpen=false;document.querySelector('#sheet-root').innerHTML=''}
function sheet(type){
 let content=type==='choose'?`<div class="sheet-title"><h3>选择日期与数量</h3><button class="close-sheet" data-action="close" aria-label="关闭">${icon('close')}</button></div>${dateFields()}${error?`<div class="inline-error">${error}</div>`:''}<div class="footer"><div class="footer-line">${money(amount())}${primary('下一步','checkout')}</div></div>`:`<div class="sheet-title"><h3>支付体验预览</h3><button class="close-sheet" data-action="close" aria-label="关闭">${icon('close')}</button></div><p class="sheet-note">这是设计演示，不会发起真实扣款。请选择一种结果，查看后续页面。</p><div class="payment-choice">${primary('模拟支付成功','success')}<button class="outline" data-action="failure">模拟支付未完成</button><button class="plain-link" data-action="cancel-pay">取消，保留订单</button></div>`;
 document.querySelector('#sheet-root').innerHTML=`<div class="sheet-backdrop" data-sheet="${type}"><section class="sheet" role="dialog" aria-modal="true" aria-label="${type==='choose'?'选择日期与数量':'支付体验预览'}">${content}</section></div>`;
 document.querySelector('.sheet button')?.focus();
}
function changeSelection(){const type=document.querySelector('[data-sheet]')?.dataset.sheet;if(type==='choose')sheet('choose');else{const pos=document.querySelector('#content').scrollTop;render();document.querySelector('#content').scrollTop=pos}}
document.addEventListener('click',e=>{
 const b=e.target.closest('button,[data-product]');if(e.target.classList.contains('sheet-backdrop'))return closeSheet();if(!b||b.disabled)return;
 if(b.dataset.go)return go(b.dataset.go);
 if(b.dataset.product!==undefined){selected=Number(b.dataset.product);quantity=1;chosenDate='';return go('detail')}
 if(b.dataset.category){category=b.dataset.category;return render()}
 if(b.dataset.orderTab){orderTab=b.dataset.orderTab;return render()}
 if(b.dataset.date){chosenDate=b.dataset.date;calendarOpen=false;error='';return changeSelection()}
 switch(b.dataset.action){
 case 'choose':error='';calendarOpen=false;sheet('choose');break;
 case 'more-date':calendarOpen=!calendarOpen;if(calendarOpen){const base=chosenDate?new Date(chosenDate+'T12:00:00'):today;calendarMonth=new Date(base.getFullYear(),base.getMonth(),1);}changeSelection();break;
 case 'prev-month':calendarMonth=new Date(calendarMonth.getFullYear(),calendarMonth.getMonth()-1,1);changeSelection();break;
 case 'next-month':calendarMonth=new Date(calendarMonth.getFullYear(),calendarMonth.getMonth()+1,1);changeSelection();break;
 case 'close':closeSheet();break;
 case 'plus':quantity=Math.min(10,quantity+1);changeSelection();break;
 case 'minus':quantity=Math.max(1,quantity-1);changeSelection();break;
 case 'checkout':if(!chosenDate){error='请选择游玩日期';sheet('choose')}else go('checkout');break;
 case 'pay':if(screen==='checkout'){if(!chosenDate){error='请选择游玩日期';return render()}order={product:selected,quantity,date:chosenDate,amount:amount(),paid:false};ticketIndex=1;}sheet('payment');break;
 case 'success':order.paid=true;go('ticket');break;
 case 'failure':go('ticket');error='支付未完成，订单已保留。你可以稍后继续支付。';render();break;
 case 'cancel-pay':go('ticket');break;
 case 'prev-ticket':ticketIndex=Math.max(1,ticketIndex-1);render();break;
 case 'next-ticket':ticketIndex=Math.min(order.quantity,ticketIndex+1);render();break;
 }
});

document.addEventListener('keydown',e=>{if(e.key==='Escape')closeSheet();if(e.key==='Tab'&&document.querySelector('.sheet')){const els=[...document.querySelectorAll('.sheet button:not(:disabled),.sheet input')];if(e.shiftKey&&document.activeElement===els[0]){e.preventDefault();els.at(-1).focus()}else if(!e.shiftKey&&document.activeElement===els.at(-1)){e.preventDefault();els[0].focus()}}});
document.querySelector('#back').onclick=()=>go(screen==='checkout'?'detail':screen==='ticket'?'orders':'home');render();
