const brand = require('../config/brand');
const commerce = require('../config/commerce');

const products = [
  {
    id: 'p_001',
    categoryId: 'rice',
    name: '招牌鸡腿饭',
    description: '外酥里嫩的照烧鸡腿，配时蔬和热米饭',
    price: 1800,
    originalPrice: 2200,
    sold: 328,
    emoji: '🍗',
    color: '#FBE7D8',
    tag: '本店招牌',
    isOnSale: true,
    stock: 28,
    options: [
      { id: 'spicy', name: '口味', required: true, values: ['不辣', '微辣', '香辣'] },
      { id: 'rice', name: '米饭', required: true, values: ['正常饭量', '加饭'] }
    ]
  },
  {
    id: 'p_002',
    categoryId: 'rice',
    name: '黑椒牛肉饭',
    description: '黑椒牛柳搭配脆爽洋葱，酱香浓郁',
    price: 2200,
    originalPrice: 2600,
    sold: 196,
    emoji: '🥩',
    color: '#E9E4D3',
    tag: '热卖',
    isOnSale: true,
    stock: 16,
    options: [
      { id: 'spicy', name: '口味', required: true, values: ['不辣', '微辣', '香辣'] },
      { id: 'egg', name: '加料', required: false, values: ['不加蛋', '加太阳蛋 +2元'] }
    ]
  },
  {
    id: 'p_003',
    categoryId: 'noodle',
    name: '番茄肥牛拌面',
    description: '酸甜番茄汤汁，拌入鲜嫩肥牛和手工面',
    price: 1600,
    originalPrice: 1800,
    sold: 142,
    emoji: '🍜',
    color: '#F6DCD3',
    tag: '今日推荐',
    isOnSale: true,
    stock: 22,
    options: [
      { id: 'spicy', name: '口味', required: true, values: ['不辣', '微辣', '香辣'] }
    ]
  },
  {
    id: 'p_004',
    categoryId: 'snack',
    name: '香酥鸡米花',
    description: '现点现炸，外脆内嫩，适合加餐分享',
    price: 1200,
    originalPrice: 1400,
    sold: 89,
    emoji: '🍿',
    color: '#F5E5B9',
    tag: '加餐',
    isOnSale: true,
    stock: 40,
    options: [
      { id: 'spicy', name: '撒料', required: true, values: ['原味', '椒盐', '香辣'] }
    ]
  },
  {
    id: 'p_005',
    categoryId: 'drink',
    name: '柠檬气泡水',
    description: '清爽柠檬和细密气泡，冰镇更好喝',
    price: 800,
    originalPrice: 1000,
    sold: 247,
    emoji: '🍋',
    color: '#E7F0CB',
    tag: '清爽',
    isOnSale: true,
    stock: 60,
    options: []
  },
  {
    id: 'p_006',
    categoryId: 'drink',
    name: '桂花乌龙茶',
    description: '淡淡桂花香，少糖不腻，搭配正餐刚好',
    price: 900,
    originalPrice: 1100,
    sold: 176,
    emoji: '🧋',
    color: '#E6E1D8',
    tag: '少糖',
    isOnSale: true,
    stock: 50,
    options: []
  },
  {
    id: 'p_retail_001',
    categoryId: 'retail_snacks',
    fulfillmentType: commerce.FULFILLMENT.COURIER,
    name: '坚果分享装·原味',
    description: '独立分装，口感酥脆，适合居家或办公室分享',
    price: 2680,
    originalPrice: 3200,
    sold: 521,
    emoji: '🐇',
    color: '#F6E1D8',
    tag: '精选商品',
    isOnSale: true,
    stock: 80,
    options: [
      { id: 'flavor', name: '口味', required: true, values: ['经典麻辣', '香辣少油'] },
      { id: 'weight', name: '重量', required: true, values: ['250g', '500g +18元'] }
    ]
  },
  {
    id: 'p_retail_002',
    categoryId: 'retail_condiments',
    fulfillmentType: commerce.FULFILLMENT.COURIER,
    name: '手工拌酱礼盒·香辣',
    description: '香辣风味，密封包装，适合拌面拌饭和日常佐餐',
    price: 3280,
    originalPrice: 3800,
    sold: 286,
    emoji: '🌶️',
    color: '#F4E5C9',
    tag: '人气小食',
    isOnSale: true,
    stock: 45,
    options: [
      { id: 'flavor', name: '口味', required: true, values: ['香辣', '麻辣'] },
      { id: 'weight', name: '规格', required: true, values: ['150g', '300g +16元'] }
    ]
  },
  {
    id: 'p_retail_003',
    categoryId: 'retail_gifts',
    fulfillmentType: commerce.FULFILLMENT.COURIER,
    name: '日常零食组合装',
    description: '多种零食一盒组合，适合追剧、出行或分享',
    price: 4980,
    originalPrice: 5680,
    sold: 173,
    emoji: '🥢',
    color: '#E6E9D9',
    tag: '组合装',
    isOnSale: true,
    stock: 32,
    options: [
      { id: 'flavor', name: '口味', required: true, values: ['经典麻辣', '香辣少油'] }
    ]
  }
];

module.exports = {
  store: {
    id: 'store_001',
    name: brand.name,
    slogan: brand.tagline,
    announcement: '订单高峰时段，预计配送时间以结算页为准',
    phone: '18000001234',
    businessStatus: 'OPEN',
    courierStatus: 'OPEN',
    businessHours: '10:30 - 21:30',
    minGoodsAmount: 1500,
    defaultDeliveryFee: 300,
    courierMinGoodsAmount: 0,
    defaultShippingFee: 800,
    freeShippingThreshold: 8800,
    shippingCarrier: '默认快递',
    deliveryText: '满 15 元起送 · 配送费 3 元起',
    shippingText: '满 88 元包邮'
  },
  categories: [
    { id: 'all', name: '全部' },
    { id: 'rice', name: '招牌盖饭', channel: commerce.FULFILLMENT.TAKEAWAY },
    { id: 'noodle', name: '面食', channel: commerce.FULFILLMENT.TAKEAWAY },
    { id: 'snack', name: '小吃', channel: commerce.FULFILLMENT.TAKEAWAY },
    { id: 'drink', name: '饮品', channel: commerce.FULFILLMENT.TAKEAWAY },
    { id: 'retail_snacks', name: '零食', channel: commerce.FULFILLMENT.COURIER },
    { id: 'retail_condiments', name: '调味品', channel: commerce.FULFILLMENT.COURIER },
    { id: 'retail_gifts', name: '组合装', channel: commerce.FULFILLMENT.COURIER }
  ],
  products,
  defaultAddress: {
    id: 'address_demo_001',
    addressType: 'DELIVERY',
    zoneId: 'zone_demo_001',
    province: '四川省',
    city: '成都市',
    district: '武侯区',
    detailAddress: '科华北路 88 号 2 栋 302',
    contactName: '朋友',
    contactPhone: '13800000000',
    remark: '工作日请放前台',
    isDefault: true,
    deliveryFee: 300
  },
  defaultShippingAddress: {
    id: 'address_demo_shipping_001',
    addressType: 'SHIPPING',
    province: '四川省',
    city: '成都市',
    district: '武侯区',
    detailAddress: '科华北路 88 号 2 栋 302',
    contactName: '朋友',
    contactPhone: '13800000000',
    remark: '工作日请放前台',
    isDefault: false,
    deliveryFee: 0
  },
  coupons: [
    {
      id: 'coupon_demo_001',
      name: '新客优惠券',
      description: '满 25 元可用 · 不抵扣配送费',
      discountAmount: 300,
      minGoodsAmount: 2500,
      status: 'AVAILABLE',
      expireAt: '2026-09-30'
    }
  ],
  campaign: {
    id: 'campaign_001',
    title: '好友助力，双方得券',
    description: '邀请 1 位好友帮你助力，发起者和好友按规则分别获得优惠券',
    starterReward: { discountAmount: 300, minGoodsAmount: 2500, validDays: 7, templateEndsAt: '2026-09-30' },
    helperReward: { discountAmount: 200, minGoodsAmount: 3000, validDays: 5, templateEndsAt: '2026-09-30' },
    endText: '长期有效 · 每人每期仅限一次'
  }
};
