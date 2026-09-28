package app

// Outfit 是跨分类共用的定稿服装套装：前台只展示名称，服务端把 Prompt 填入定稿提示词的 {{clothes}}。
type Outfit struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Prompt string `json:"prompt,omitempty"`
}

const defaultOutfitID = "default"

func defaultOutfits() []Outfit {
	return []Outfit{
		{ID: defaultOutfitID, Name: "默认", Prompt: outfitDefault},
		{ID: "black_formal", Name: "黑色礼服", Prompt: outfitBlackFormal},
		{ID: "white_tee", Name: "白色 T-shirt 日常休闲", Prompt: outfitWhiteTee},
		{ID: "navy_polo", Name: "深蓝学院风", Prompt: outfitNavyPolo},
		{ID: "black_hoodie", Name: "黑色连帽卫衣街头风", Prompt: outfitBlackHoodie},
		{ID: "grey_suit", Name: "浅灰色西装休闲", Prompt: outfitGreySuit},
		{ID: "sport", Name: "运动套装", Prompt: outfitSport},
		{ID: "chinese", Name: "中式风格", Prompt: outfitChinese},
		{ID: "seasonal", Name: "季节性外套", Prompt: outfitSeasonal},
	}
}

func findOutfit(id string) (Outfit, bool) {
	for _, o := range defaultOutfits() {
		if o.ID == id {
			return o, true
		}
	}
	return Outfit{}, false
}

func outfitKnown(id string) bool {
	_, ok := findOutfit(id)
	return ok
}

// outfitPromptText 返回定稿提示词【服装】段正文；未知或空编号回落到默认套装。
func outfitPromptText(id string) string {
	if o, ok := findOutfit(id); ok {
		return o.Prompt
	}
	o, _ := findOutfit(defaultOutfitID)
	return o.Prompt
}

const outfitDefault = `上衣：白色翻领短袖，简化为适合微型身体的轮廓和少量褶皱，省略胸前标志。
下装：简洁深蓝色短裤 + 小白鞋（照片未显示部分自行补全）。
服装贴合小身体，不用宽大衣服增加体积。`

const outfitBlackFormal = `上衣：黑色缎面翻领燕尾服式短版夹克，贴合微型躯干，保留缎面光泽感，省略胸针细节，领口处白色衬衫领和黑色蝴蝶结领结可见。
下装：黑色礼服短裤，侧缝有细缎条。
鞋：黑色亮面小皮鞋。
服装贴合小身体，线条简洁，不用宽大廓形增加体积。`

const outfitWhiteTee = `上衣：纯白圆领短袖 T-shirt，简洁无图案，少量胸口褶皱。
下装：浅卡其色休闲短裤。
鞋：白色低帮帆布鞋。
服装贴合小身体，整体轻盈干净。`

const outfitNavyPolo = `上衣：深藏青色翻领 polo 衫，短袖，领口有白色细线条。
下装：灰色直筒短裤。
鞋：白色运动鞋。
服装贴合小身体，廓形利落。`

const outfitBlackHoodie = `上衣：黑色短版连帽卫衣，帽子自然搭落在背后，袖口有白色罗纹，省略胸口印花。
下装：深灰色慢跑短裤，侧缝白色细条。
鞋：黑白配色厚底运动鞋。
服装贴合小身体，不用宽大衣服增加体积。`

const outfitGreySuit = `上衣：浅灰色单排扣短版西装外套，内搭白色立领衬衫，无领带，衣领自然翻开。
下装：同色系浅灰西裤短裤。
鞋：白色小皮鞋。
服装贴合小身体，保留廓形感，省略口袋方巾等细节。`

const outfitSport = `上衣：亮蓝色短袖运动速干 T 恤，领口和袖口有白色撞色包边，贴合微型躯干，无品牌标志。
下装：深灰色运动短裤，侧缝一条白色细条纹，松紧腰。
鞋：白色跑步鞋，鞋底带浅灰缓震线条。
服装贴合小身体，面料垂坠感简化，不用宽大衣服增加体积。`

const outfitChinese = `上衣：藏青色立领对襟短褂，盘扣四枚，领口与襟边有浅金色滚边，短袖，衣摆收进下装。
下装：同色系藏青长裤改短裤（呼应微型比例），裤脚有浅金细边。
鞋：黑色布鞋，白袜露出脚踝。
服装贴合小身体，结构简化，省去刺绣等繁复纹样，保留立领与盘扣的辨识度。`

const outfitSeasonal = `上衣：米白色短款连帽羽绒外套，横向绗缝压线，拉链拉至胸口，帽子搭落在背后，内搭一件同色系薄毛衣。
下装：深卡其色灯芯绒短裤，侧缝隐约可见。
鞋：棕色短靴，鞋帮略高。
围巾：浅灰色针织细围巾，自然绕在颈部一周。
服装贴合小身体，外套体积感控制在小幅蓬松，避免遮住肢体和比例。`
