package app

import "sort"

// 日常按一天的沟通顺序排列，通用情绪放在最后；编号保持稳定以兼容已有作品。
func defaultDailyActions() []Action {
	return []Action{
		{"morning", "早呀", "☀", dailyActionMorning},
		{"wake_up", "起床啦", "🥱", dailyActionWakeUp},
		{"heading_out", "我出门啦", "🎒", dailyActionHeadingOut},
		{"arrived", "我到啦", "📍", dailyActionArrived},
		{"busy", "先忙一会", "📝", dailyActionBusy},
		{"cheer", "加油呀", "✊", dailyActionCheer},
		{"eat_ask", "吃饭了么", "🍚", dailyActionEatAsk},
		{"eat", "干饭", "🍜", dailyActionEat},
		{"nap", "午休一下", "💤", dailyActionNap},
		{"drink_water", "喝水啦", "🥛", dailyActionDrinkWater},
		{"tea_break", "下午茶", "🧋", dailyActionTeaBreak},
		{"off_work", "下班啦", "🎉", dailyActionOffWork},
		{"wash_dishes", "我来洗碗", "🧽", dailyActionWashDishes},
		{"play", "出去玩", "🏃", dailyActionPlay},
		{"walk", "一起散步", "🚶", dailyActionWalk},
		{"gaming", "开黑吗", "🎮", dailyActionGaming},
		{"hug", "抱抱你", "🤗", dailyActionHug},
		{"thanks", "辛苦啦", "🫶", dailyActionThanks},
		{"sleep_early", "早点睡", "🌙", dailyActionSleepEarly},
		{"good_night", "晚安", "☾", dailyActionGoodNight},
		{"hello_wave", "挥手问好", "👋", dailyActionHelloWave},
		{"cry", "放声大哭", "😭", dailyActionCry},
		{"dance", "转圈跳舞", "💃", dailyActionDance},
		{"gift", "拆礼物", "🎁", dailyActionGift},
		{"stomp", "生气跺脚", "💢", dailyActionStomp},
		{"shock", "惊讶后仰", "😲", dailyActionShock},
		{"heart", "双手比心", "♡", dailyActionHeart},
		{"tease", "调侃你", "😜", dailyActionTease},
	}
}

// 足以容纳新增的一天场景及旧配置中最多12个自定义动作。
func categoryActionLimit(id string) int {
	if id == "daily" {
		return 48
	}
	return 12
}

const dailyActionMorningLegacy = "微带困意揉眼一下（手不遮五官），再小幅挥手问好两次，浅笑，放下回站姿。举手幅度小，手臂保持短小，头顶与举手最高点距格顶至少约格高10%透明边距，脚底距格底同样留边；禁止邻格头顶渗进本格脚下。"
const dailyActionEatAskLegacy = "关切表情，胸前端极小白碗向前递一点并点头，再收回回站姿。"
const dailyActionPlayLegacy = "右手侧上招手两次，身体轻跳一次落回，开心浅笑；幅度小，不拉长四肢。"
const dailyActionTeaseLegacy = "眨眼一下，再轻微吐舌或歪头坏笑点头，回平静站姿；吐舌幅度很小。"

func normalizeDailyActions(have, defs []Action) []Action {
	// 只更新仍为出厂原文的提示词，后台编辑的名称、图标与过程原样保留。
	for _, old := range []struct{ id, prompt string }{
		{"eat", "捧小碗低头大口吃两口，腮帮微鼓，抬头满足呼气，回站姿；五官不糊。"},
		{"eat", dailyActionEatLegacy},
		{"morning", "微带困意揉眼一下（不遮五官），再挥手问好两次，浅笑，放下回站姿。"},
		{"morning", dailyActionMorningLegacy},
		{"eat_ask", dailyActionEatAskLegacy},
		{"play", dailyActionPlayLegacy},
		{"tease", dailyActionTeaseLegacy},
	} {
		have = upgradeActionPrompt(have, defs, old.id, old.prompt)
	}
	have = migrateDailyMissToHeart(have)
	have = dedupeDailyHeart(have)
	ids := make([]string, 0, len(defs))
	order := make(map[string]int, len(defs))
	for i, a := range defs {
		ids = append(ids, a.ID)
		order[a.ID] = i
	}
	have = ensureActionsWithLimit(have, defs, categoryActionLimit("daily"), ids...)
	// 自定义动作排在内置动作之后，彼此的原有顺序不变。
	rank := func(id string) int {
		if i, ok := order[id]; ok {
			return i
		}
		return len(defs)
	}
	sort.SliceStable(have, func(i, j int) bool { return rank(have[i].ID) < rank(have[j].ID) })
	return have
}
