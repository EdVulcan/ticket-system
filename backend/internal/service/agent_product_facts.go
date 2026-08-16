package service

import (
	"regexp"
	"strconv"
	"strings"
)

// agentProductUserFacts are facts extracted from the operator's turns. Model
// output may normalize these facts, but it may not create a new value for a
// field that has no user-supplied source.
type agentProductUserFacts struct {
	ProductType     string   `json:"product_type,omitempty"`
	Price           *float64 `json:"price,omitempty"`
	SettlementPrice *float64 `json:"settlement_price,omitempty"`
	ScenicAreaName  string   `json:"scenic_area_name,omitempty"`
	CheckpointNames []string `json:"checkpoint_names,omitempty"`
}

var (
	agentOnlineProductPattern  = regexp.MustCompile(`(?i)(线上|在线|网售|小程序票)`)
	agentOfflineProductPattern = regexp.MustCompile(`(?i)(窗口|线下|pos|现场票)`)
	agentPricePattern          = regexp.MustCompile(`(?i)(?:票面售价|售价|零售价|销售价|票价)\s*(?:是|为|：|:)?\s*([0-9]+(?:\.[0-9]+)?)`)
	agentSettlementPattern     = regexp.MustCompile(`(?i)(?:供应商)?(?:结算价|供货价|成本价)\s*(?:是|为|：|:)?\s*([0-9]+(?:\.[0-9]+)?)`)
)

func mergeAgentProductUserFacts(previous agentProductUserFacts, input string) (agentProductUserFacts, error) {
	facts := previous
	text := strings.TrimSpace(input)
	online := agentOnlineProductPattern.MatchString(text)
	offline := agentOfflineProductPattern.MatchString(text)
	if online && offline {
		return facts, agentInvalid("一次只能创建一种票种类型，请明确选择线上票或窗口/POS 票")
	}
	if online {
		facts.ProductType = "online"
	}
	if offline {
		facts.ProductType = "offline"
	}
	if value, ok := parseAgentMoney(agentSettlementPattern, text); ok {
		facts.SettlementPrice = &value
	}
	if value, ok := parseAgentMoney(agentPricePattern, text); ok {
		facts.Price = &value
	}
	return facts, nil
}

func parseAgentMoney(pattern *regexp.Regexp, input string) (float64, bool) {
	matches := pattern.FindStringSubmatch(input)
	if len(matches) != 2 {
		return 0, false
	}
	value, err := strconv.ParseFloat(matches[1], 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

func sanitizeAgentProductCandidate(input string, previous agentTaskContext, source *agentProductCandidate, facts *agentProductUserFacts) (*agentProductCandidate, error) {
	if source == nil || facts == nil {
		return &agentProductCandidate{}, nil
	}
	candidate := *source
	// User facts are authoritative. A provider cannot choose a product type or
	// price when the operator did not state one, and cannot override a value in
	// a previous turn with a different model-generated value.
	if facts.ProductType == "" {
		candidate.ProductType = ""
	} else {
		candidate.ProductType = facts.ProductType
	}
	if facts.Price == nil {
		candidate.Price = nil
	} else {
		value := *facts.Price
		candidate.Price = &value
	}
	if facts.SettlementPrice == nil {
		candidate.SettlementPrice = nil
	} else {
		value := *facts.SettlementPrice
		candidate.SettlementPrice = &value
	}

	if strings.TrimSpace(candidate.ScenicAreaName) != "" &&
		!agentTextContains(input, candidate.ScenicAreaName) &&
		!strings.EqualFold(strings.TrimSpace(previous.UserFacts.ScenicAreaName), strings.TrimSpace(candidate.ScenicAreaName)) {
		candidate.ScenicAreaName = ""
	} else if strings.TrimSpace(candidate.ScenicAreaName) != "" {
		facts.ScenicAreaName = strings.TrimSpace(candidate.ScenicAreaName)
	}

	filteredGroups := make([]agentRuleDraftGroup, 0, len(candidate.Groups))
	for _, group := range candidate.Groups {
		filteredItems := make([]agentRuleDraftItem, 0, len(group.Items))
		for _, item := range group.Items {
			name := strings.TrimSpace(item.CheckpointName)
			if name == "" {
				continue
			}
			if agentTextContains(input, name) || agentStringInList(previous.UserFacts.CheckpointNames, name) {
				item.CheckpointID = 0
				filteredItems = append(filteredItems, item)
				if !agentStringInList(facts.CheckpointNames, name) {
					facts.CheckpointNames = append(facts.CheckpointNames, name)
				}
			}
		}
		if len(filteredItems) > 0 {
			// Group totals are optional and are not a substitute for an item's
			// per-check-in limit. A provider can easily mistake a number such as
			// "elevator: 10 times" for a group total; ignore only malformed
			// provider values so the server can still preview the explicit items.
			if group.MaxTotalCheckIn < 0 || group.MaxTotalCheckIn > len(filteredItems) {
				group.MaxTotalCheckIn = 0
			}
			group.Items = filteredItems
			filteredGroups = append(filteredGroups, group)
		}
	}
	if len(filteredGroups) == 0 {
		candidate.Groups = nil
	} else {
		candidate.Groups = filteredGroups
	}

	// Optional fields use server defaults unless the current turn explicitly
	// names the corresponding setting. This prevents the model from turning a
	// guessed refund, stock, date, or code policy into a previewed fact.
	if !agentHasAny(strings.ToLower(input), []string{"有效期", "有效天数", "有效日期"}) {
		candidate.ValidityType = ""
		candidate.ValidityDays = nil
		candidate.ValidityStart = ""
		candidate.ValidityEnd = ""
	}
	if !agentHasAny(strings.ToLower(input), []string{"规则名称", "规则名"}) {
		candidate.RuleName = ""
	}
	if !agentHasAny(strings.ToLower(input), []string{"订单码", "票码", "编码模式"}) {
		candidate.CodeMode = ""
	}
	if !agentHasAny(strings.ToLower(input), []string{"库存", "库存类型", "每日库存"}) {
		candidate.StockType = ""
		candidate.DailyStock = nil
	}
	if !agentHasAny(strings.ToLower(input), []string{"实名", "实名制"}) {
		candidate.RealNameRequired = nil
	}
	if !agentHasAny(strings.ToLower(input), []string{"退款", "退票"}) {
		candidate.RefundType = ""
		candidate.RefundRule = ""
	}
	if !agentHasAny(strings.ToLower(input), []string{"标签", "闸机语音", "语音", "手机号限购", "身份证限购"}) {
		candidate.Tags = ""
		candidate.GateVoiceCode = ""
		candidate.LimitPerPhone = nil
		candidate.LimitPerID = nil
	}
	return &candidate, nil
}

func enrichAgentProductUserFacts(facts agentProductUserFacts, product *agentProductDraft) agentProductUserFacts {
	if product == nil {
		return facts
	}
	for _, group := range product.Groups {
		for _, item := range group.Items {
			name := strings.TrimSpace(item.CheckpointName)
			if name != "" && !agentStringInList(facts.CheckpointNames, name) {
				facts.CheckpointNames = append(facts.CheckpointNames, name)
			}
		}
	}
	return facts
}

func mergeAgentRuleDraftGroups(previous, incoming []agentRuleDraftGroup) []agentRuleDraftGroup {
	result := make([]agentRuleDraftGroup, 0, len(previous)+len(incoming))
	for _, group := range previous {
		result = append(result, group)
	}
	for _, incomingGroup := range incoming {
		groupIndex := -1
		for index := range result {
			if strings.EqualFold(strings.TrimSpace(result[index].GroupName), strings.TrimSpace(incomingGroup.GroupName)) {
				groupIndex = index
				break
			}
		}
		if groupIndex < 0 {
			result = append(result, incomingGroup)
			continue
		}
		result[groupIndex].MaxTotalCheckIn = incomingGroup.MaxTotalCheckIn
		for _, incomingItem := range incomingGroup.Items {
			found := false
			for itemIndex := range result[groupIndex].Items {
				if strings.EqualFold(strings.TrimSpace(result[groupIndex].Items[itemIndex].CheckpointName), strings.TrimSpace(incomingItem.CheckpointName)) {
					result[groupIndex].Items[itemIndex] = incomingItem
					found = true
					break
				}
			}
			if !found {
				result[groupIndex].Items = append(result[groupIndex].Items, incomingItem)
			}
		}
	}
	return result
}

func agentTextContains(input, value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && strings.Contains(strings.ToLower(input), strings.ToLower(value))
}

func agentStringInList(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(wanted)) {
			return true
		}
	}
	return false
}
