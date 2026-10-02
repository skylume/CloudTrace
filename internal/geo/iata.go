package geo

import "strings"

// 本文件是数据中心代码（IATA 机场码）与归属地的映射表。
//
// 表以「空格分隔的 KEY=VAL」形式放在常量字符串里，查表用子串查找。不用
// 包级 map 是为了守住「不持有包级可变状态」：常量表天然可重复调用、结果
// 一致，也不会被并发读写问题缠上。
//
// 每段字符串自带首尾空格，拼接后分隔符不会丢。

// airportTable 是数据中心代码到中文地名的映射。
//
// Cloudflare 的 colo 是机场码，本身不含任何地理信息，「HKG」对用户来说
// 并不比「香港」更好懂，所以展示层要过一道这张表。
const airportTable = " HKG=香港 TPE=台北 KHH=高雄 MFM=澳门 " +
	" NRT=东京 HND=东京 KIX=大阪 NGO=名古屋 " +
	" FUK=福冈 CTS=札幌 OKA=冲绳 " +
	" ICN=首尔 GMP=首尔 PUS=釜山 " +
	" SIN=新加坡 BKK=曼谷 DMK=曼谷 " +
	" KUL=吉隆坡 HKT=普吉岛 " +
	" MNL=马尼拉 CEB=宿务 " +
	" HAN=河内 SGN=胡志明市 " +
	" JKT=雅加达 DPS=巴厘岛 " +
	" DEL=德里 BOM=孟买 MAA=金奈 " +
	" DXB=迪拜 AUH=阿布扎比 " +
	" SJC=圣何塞 LAX=洛杉矶 SFO=旧金山 " +
	" SEA=西雅图 PDX=波特兰 " +
	" LAS=拉斯维加斯 PHX=菲尼克斯 " +
	" DEN=丹佛 DFW=达拉斯 IAH=休斯顿 " +
	" ORD=芝加哥 MSP=明尼阿波利斯 " +
	" ATL=亚特兰大 MIA=迈阿密 MCO=奥兰多 " +
	" JFK=纽约 EWR=纽约 LGA=纽约 " +
	" BOS=波士顿 PHL=费城 IAD=华盛顿 " +
	" YYZ=多伦多 YVR=温哥华 YUL=蒙特利尔 " +
	" LHR=伦敦 LGW=伦敦 STN=伦敦 " +
	" CDG=巴黎 ORY=巴黎 " +
	" FRA=法兰克福 MUC=慕尼黑 TXL=柏林 " +
	" AMS=阿姆斯特丹 EIN=埃因霍温 " +
	" MAD=马德里 BCN=巴塞罗那 " +
	" FCO=罗马 MXP=米兰 LIN=米兰 " +
	" ZRH=苏黎世 GVA=日内瓦 " +
	" VIE=维也纳 PRG=布拉格 " +
	" WAW=华沙 KRK=克拉科夫 " +
	" HEL=赫尔辛基 OSL=奥斯陆 ARN=斯德哥尔摩 " +
	" CPH=哥本哈根 " +
	" SYD=悉尼 MEL=墨尔本 BNE=布里斯班 " +
	" PER=珀斯 ADL=阿德莱德 " +
	" AKL=奥克兰 WLG=惠灵顿 " +
	" GRU=圣保罗 GIG=里约热内卢 EZE=布宜诺斯艾利斯 " +
	" SCL=圣地亚哥 LIM=利马 BOG=波哥大 " +
	" JNB=约翰内斯堡 CPT=开普敦 CAI=开罗 " +
	" BRU=布鲁塞尔"

// nameToCountryTable 是中文地名到两位国家代码的映射。
//
// 有了它才能把「数据中心」映射到「国家」，进而支持「只看中国大陆的节点」
// 这类按国家筛选的快捷选项。
const nameToCountryTable = " 香港=HK 澳门=MO 台北=TW 高雄=TW " +
	" 东京=JP 大阪=JP 名古屋=JP 福冈=JP 札幌=JP 冲绳=JP " +
	" 首尔=KR 釜山=KR " +
	" 新加坡=SG 曼谷=TH 普吉岛=TH 吉隆坡=MY " +
	" 马尼拉=PH 宿务=PH 河内=VN 胡志明市=VN " +
	" 雅加达=ID 巴厘岛=ID " +
	" 德里=IN 孟买=IN 金奈=IN " +
	" 迪拜=AE 阿布扎比=AE " +
	" 伦敦=GB 巴黎=FR 法兰克福=DE 柏林=DE 慕尼黑=DE " +
	" 阿姆斯特丹=NL 埃因霍温=NL 布鲁塞尔=BE " +
	" 马德里=ES 巴塞罗那=ES 米兰=IT 罗马=IT " +
	" 苏黎世=CH 日内瓦=CH 维也纳=AT " +
	" 斯德哥尔摩=SE 奥斯陆=NO 哥本哈根=DK 赫尔辛基=FI " +
	" 华沙=PL 克拉科夫=PL 布拉格=CZ " +
	" 纽约=US 洛杉矶=US 旧金山=US 西雅图=US 芝加哥=US " +
	" 达拉斯=US 迈阿密=US 亚特兰大=US 波士顿=US 费城=US " +
	" 华盛顿=US 丹佛=US 休斯顿=US 拉斯维加斯=US " +
	" 菲尼克斯=US 波特兰=US 奥兰多=US 明尼阿波利斯=US 圣何塞=US " +
	" 多伦多=CA 温哥华=CA 蒙特利尔=CA " +
	" 悉尼=AU 墨尔本=AU 布里斯班=AU 珀斯=AU 阿德莱德=AU " +
	" 奥克兰=NZ 惠灵顿=NZ " +
	" 圣保罗=BR 里约热内卢=BR 布宜诺斯艾利斯=AR " +
	" 圣地亚哥=CL 利马=PE 波哥大=CO " +
	" 约翰内斯堡=ZA 开普敦=ZA 开罗=EG"

// RegionName 把数据中心代码翻译成中文地名。
//
// 认不出来时**原样返回代码**：显示「XYZ」比显示空白好，至少用户还能拿它
// 去搜索；空白会让人以为这个节点没有地区信息。
func RegionName(colo string) string {
	code := normalizeCode(colo)
	if code == "" {
		return ""
	}
	if name := lookupTable(airportTable, code); name != "" {
		return name
	}
	return code
}

// AirportName 返回数据中心代码对应的中文地名；未知时返回空串。
//
// 与 RegionName 的区别是「未知」的表达：这里要的是「知道不知道」，
// 而展示层要的是「总得显示点什么」。
func AirportName(colo string) string {
	return lookupTable(airportTable, normalizeCode(colo))
}

// CountryFromColo 把数据中心代码映射成两位国家代码；未知时返回空串。
//
// 用于按国家筛选节点：Cloudflare 的 colo 是机场码，本身不含国家信息。
func CountryFromColo(colo string) string {
	name := AirportName(colo)
	if name == "" {
		return ""
	}
	return lookupTable(nameToCountryTable, name)
}

// normalizeCode 归一化代码：去掉空白并转大写。
//
// 上游给的代码大小写不统一（trace 里是全大写，远程源里可能出现小写），
// 大小写不敏感才不会漏掉一半。
func normalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// lookupTable 在「空格分隔 KEY=VAL」的常量表里查一个键。
//
// 前后各补一个空格再匹配，保证匹配的是完整的一段而不是某个键的后缀：
// 表里有 HKG 与 KHG 时，只按 "HKG=" 找会把 "KHG=" 也匹配上。
func lookupTable(table, key string) string {
	if key == "" {
		return ""
	}
	needle := " " + key + "="
	i := strings.Index(table, needle)
	if i < 0 {
		return ""
	}
	rest := table[i+len(needle):]
	if j := strings.IndexByte(rest, ' '); j >= 0 {
		return rest[:j]
	}
	return rest
}
