package service

const commerceMaxInt64 = int64(1<<63 - 1)
const commerceMinInt64 = -1 << 63

func commerceSafeAddInt64(left, right int64) (int64, bool) {
	if right > 0 && left > commerceMaxInt64-right {
		return 0, false
	}
	if right < 0 && left < commerceMinInt64-right {
		return 0, false
	}
	return left + right, true
}

func commerceSafeMulInt64(left, right int64) (int64, bool) {
	if left == 0 || right == 0 {
		return 0, true
	}
	if (left == commerceMinInt64 && right == -1) || (right == commerceMinInt64 && left == -1) {
		return 0, false
	}
	result := left * right
	if result/right != left {
		return 0, false
	}
	return result, true
}

func commerceSafeAddInt(left, right int) (int, bool) {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	if right > 0 && left > maxInt-right {
		return 0, false
	}
	if right < 0 && left < minInt-right {
		return 0, false
	}
	return left + right, true
}

func commerceSafeIncrementInt64(value int64) (int64, bool) {
	return commerceSafeAddInt64(value, 1)
}
