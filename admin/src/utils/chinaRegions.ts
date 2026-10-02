import areaData from 'china-area-data/v5/data.json'

export type ChinaRegionOption = {
  value: string
  label: string
  children?: ChinaRegionOption[]
}

type AreaMap = Record<string, Record<string, string>>

const areas = areaData as AreaMap

// The package uses `市辖区` as the city node for municipalities. The public
// storefront picker returns the municipality name for both province and city,
// so normalize the admin selector to the same values before saving a zone.
const municipalityProvinceCodes = new Set(['110000', '120000', '310000', '500000'])

function childrenFor(parentCode: string, municipalityName = ''): ChinaRegionOption[] {
  return Object.entries(areas[parentCode] || {}).map(([code, label]) => ({
    value: municipalityName && label === '市辖区' ? municipalityName : label,
    label: municipalityName && label === '市辖区' ? municipalityName : label,
    children: Object.keys(areas[code] || {}).length ? childrenFor(code) : undefined,
  }))
}

export const chinaRegionOptions: ChinaRegionOption[] = Object.entries(areas['86'] || {}).map(([provinceCode, provinceName]) => ({
  value: provinceName,
  label: provinceName,
  children: childrenFor(provinceCode, municipalityProvinceCodes.has(provinceCode) ? provinceName : ''),
}))

export function findChinaRegionPath(province: string, city: string, district: string): string[] {
  const target = [province, city, district].map(value => String(value || '').trim())
  if (target.some(value => !value)) return []
  return chinaRegionOptions.some(option => option.value === target[0]) &&
    (chinaRegionOptions.find(option => option.value === target[0])?.children || []).some(option => option.value === target[1] &&
      (option.children || []).some(child => child.value === target[2]))
    ? target
    : []
}

