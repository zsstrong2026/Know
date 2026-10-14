package service

import "github.com/Tencent/WeKnora/internal/types"

// detectBickering 检测"互呛"：两个智能体就同一议题反复来回复述、且用户未再介入。
//
// 判定语义（按时间正序遍历，msgs 为旧->新）：
//  1. 从尾部回溯，找最后一条非智能体消息（user/host/system 等），其后的全部智能体消息
//     作为 observation tail；tail 长度不足 maxExchange*2 则不可能构成 maxExchange 个完整来回。
//  2. tail 只允许涉及两个 senderID，且相邻两条 senderID 必须不同（严格交替）；
//     一旦出现第三方或同一人连发即判定不是"一对一互呛"。
//  3. 完整来回数 = len(tail)/2，需 >= maxExchange 才触发。
//
// 触发时返回参与互呛的两个 senderID，按字典序排序以保证结果稳定；未触发返回空值。
func detectBickering(msgs []*types.MeetingMessage, maxExchange int) (bool, [2]string) {
	if maxExchange < 1 {
		maxExchange = 1
	}
	// 1. 取尾部：最后一条非智能体消息之后的全部智能体消息
	start := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].SenderKind != types.MeetingSenderAgent {
			start = i + 1
			break
		}
	}
	tail := msgs[start:]
	if len(tail) < maxExchange*2 {
		return false, [2]string{}
	}
	// 2. 尾部只允许涉及两个 senderID，且严格交替（相邻不同）
	var pair [2]string
	for i, m := range tail {
		switch m.SenderID {
		case pair[0]:
			// 命中已知成员
		case pair[1]:
			// 命中已知成员
		default:
			switch {
			case pair[0] == "":
				pair[0] = m.SenderID
			case pair[1] == "":
				pair[1] = m.SenderID
			default:
				return false, [2]string{} // 出现第三方
			}
		}
		if i > 0 && tail[i].SenderID == tail[i-1].SenderID {
			return false, [2]string{} // 同一人连发，非严格交替
		}
	}
	// 3. 完整来回数需达到阈值
	if len(tail)/2 < maxExchange {
		return false, [2]string{}
	}
	// 4. 排序保证对稳定（便于事件与日志幂等）
	if pair[0] > pair[1] {
		pair[0], pair[1] = pair[1], pair[0]
	}
	return true, pair
}
