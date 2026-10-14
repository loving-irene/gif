package app

import "strings"

// 后台配置会把动作过程与定稿提示词存进数据库，因此只改代码里的默认值不会影响已有部署。
// 这里记录“上一版出厂提示词”的摘要：读取配置时，仍与旧出厂原文完全一致的条目升级为新版，
// 管理员改过的内容（摘要不同）原样保留。只存摘要而不保留旧原文，避免代码里堆积几十段废弃文案。
//
// 摘要取 hash()（SHA-256 十六进制）的前16位，对旧原文 strings.TrimSpace 后计算。
var retiredActionPromptHashes = map[string]string{
	"bike": "239a0355b818aefe", "basketball": "b5a625c8085d81e4", "billiards": "8f7144045e344177",
	"smoke": "9e10ef540a6e22d0", "fish": "750bd96c67ef37be",
	"selfie_peace": "51a275e30c3b884b", "skirt_spin": "f0233ba8a7c5eb3d", "blush": "d2195909eb6d58e5",
	"wish": "b2c58c931da2d7bb", "milk_tea": "0a27a14430d85b13", "petal": "d5747b35787aea9a",
	"bubbles": "1ad3e0790f59ca51", "lollipop": "d819d60320feadb1", "peekaboo": "783d05270789ea75",
	"blocks": "4435670c1b9b648c", "airplane": "eee301c2c19dc09f", "carry_me": "5bacd966015bc956",
	"morning": "298a6a8a9f975e31", "wake_up": "2c4d44aec020809a", "heading_out": "7ce883623ebb2b58",
	"arrived": "3538c26f1f256109", "busy": "0061b807ad485d1a", "cheer": "a210b28a0ba1fe29",
	"eat_ask": "b70af13376f586e6", "eat": "4235ab8aa9920833", "nap": "2ed63fa475c84109",
	"drink_water": "fef56e5b73b56946", "tea_break": "3b072b9dcfe1c26e", "off_work": "a1d239ecbc27412c",
	"wash_dishes": "c48fe00d04230a1a", "play": "6fdc11ec69857060", "walk": "6c8a57e88d857e5e",
	"gaming": "a9569abcade742dc", "hug": "f580ced8c9ab4bf2", "thanks": "94cab75196328e12",
	"sleep_early": "6902a91736689ebd", "good_night": "73ee9a55cf3b94f1", "hello_wave": "2ae327b70d0b0d89",
	"cry": "cd97b79bff2a6639", "dance": "dcdee7c684838746", "gift": "4b33e5a5a9c6e16c",
	"stomp": "6d32e1b520144d55", "shock": "726c102022657e18", "heart": "a6b752ab901f58e6",
	"tease": "d311c51bbbb47254",
}

// retiredDraftPromptHash 是上一版内置定稿提示词（含大段重复的比例声明）的摘要。
const retiredDraftPromptHash = "bb3f4815608e08b2"

func promptDigest(prompt string) string { return hash(strings.TrimSpace(prompt))[:16] }

// upgradeRetiredActionPrompts 把仍是上一版出厂原文的动作过程换成 defs 中的新版，其余保持不变。
func upgradeRetiredActionPrompts(have, defs []Action) []Action {
	fresh := make(map[string]string, len(defs))
	for _, a := range defs {
		fresh[a.ID] = a.Prompt
	}
	for i := range have {
		want, ok := retiredActionPromptHashes[have[i].ID]
		neu, known := fresh[have[i].ID]
		if ok && known && promptDigest(have[i].Prompt) == want {
			have[i].Prompt = neu
		}
	}
	return have
}
