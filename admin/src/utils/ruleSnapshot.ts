export type RuleSnapshotItem = {
  checkpoint_id?: number
  checkpoint_name: string
  max_per_check_in: number
}

export type RuleSnapshotGroup = {
  key: string
  group_name: string
  max_total_check_in: number
  items: RuleSnapshotItem[]
}

/**
 * Parse the server's rule projection for a readable diff. A null result means
 * the stored value was not a rule projection and should be shown as raw text;
 * an empty array is a valid projection with no groups.
 */
export function parseRuleSnapshot(value: unknown): RuleSnapshotGroup[] | null {
  if (typeof value !== 'string' || !value.trim()) return null
  try {
    const parsed = JSON.parse(value)
    if (!parsed || !Array.isArray(parsed.groups)) return null
    return parsed.groups.map((group: any, index: number) => ({
      key: `${String(group?.group_name || 'group')}-${index}`,
      group_name: String(group?.group_name || '未命名规则组'),
      max_total_check_in: Number(group?.max_total_check_in || 0),
      items: Array.isArray(group?.items) ? group.items.map((item: any) => ({
        checkpoint_id: Number(item?.checkpoint_id || 0) || undefined,
        checkpoint_name: String(item?.checkpoint_name || (item?.checkpoint_id ? `#${item.checkpoint_id}` : '未命名检票点')),
        max_per_check_in: Number(item?.max_per_check_in || 0),
      })) : [],
    }))
  } catch {
    return null
  }
}

export function ruleGroupMode(group: RuleSnapshotGroup): string {
  return group.max_total_check_in > 0 ? `任选 ${group.max_total_check_in} 个` : '全部点位'
}
