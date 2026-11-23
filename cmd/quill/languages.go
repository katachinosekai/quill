package main

import (
	"sort"
	"strings"
)

// autoLang is the picker's first entry: whisper detects the spoken language
// itself ("auto" is passed straight to whisper) using the multilingual model.
const autoLang = "auto"

type language struct{ code, name, native string }

var languages = []language{
	{autoLang, "Auto-detect", ""},
	{"en", "English", "English"},
	{"zh", "Chinese", "中文"},
	{"de", "German", "Deutsch"},
	{"es", "Spanish", "Español"},
	{"ru", "Russian", "Русский"},
	{"ko", "Korean", "한국어"},
	{"fr", "French", "Français"},
	{"ja", "Japanese", "日本語"},
	{"pt", "Portuguese", "Português"},
	{"tr", "Turkish", "Türkçe"},
	{"pl", "Polish", "Polski"},
	{"ca", "Catalan", "Català"},
	{"nl", "Dutch", "Nederlands"},
	{"ar", "Arabic", "العربية"},
	{"sv", "Swedish", "Svenska"},
	{"it", "Italian", "Italiano"},
	{"id", "Indonesian", "Bahasa Indonesia"},
	{"hi", "Hindi", "हिन्दी"},
	{"fi", "Finnish", "Suomi"},
	{"vi", "Vietnamese", "Tiếng Việt"},
	{"he", "Hebrew", "עברית"},
	{"uk", "Ukrainian", "Українська"},
	{"el", "Greek", "Ελληνικά"},
	{"ms", "Malay", "Bahasa Melayu"},
	{"cs", "Czech", "Čeština"},
	{"ro", "Romanian", "Română"},
	{"da", "Danish", "Dansk"},
	{"hu", "Hungarian", "Magyar"},
	{"ta", "Tamil", "தமிழ்"},
	{"no", "Norwegian", "Norsk"},
	{"th", "Thai", "ไทย"},
	{"ur", "Urdu", "اردو"},
	{"hr", "Croatian", "Hrvatski"},
	{"bg", "Bulgarian", "Български"},
	{"lt", "Lithuanian", "Lietuvių"},
	{"la", "Latin", "Latina"},
	{"mi", "Maori", "Māori"},
	{"ml", "Malayalam", "മലയാളം"},
	{"cy", "Welsh", "Cymraeg"},
	{"sk", "Slovak", "Slovenčina"},
	{"te", "Telugu", "తెలుగు"},
	{"fa", "Persian", "فارسی"},
	{"lv", "Latvian", "Latviešu"},
	{"bn", "Bengali", "বাংলা"},
	{"sr", "Serbian", "Српски"},
	{"az", "Azerbaijani", "Azərbaycan"},
	{"sl", "Slovenian", "Slovenščina"},
	{"kn", "Kannada", "ಕನ್ನಡ"},
	{"et", "Estonian", "Eesti"},
	{"mk", "Macedonian", "Македонски"},
	{"br", "Breton", "Brezhoneg"},
	{"eu", "Basque", "Euskara"},
	{"is", "Icelandic", "Íslenska"},
	{"hy", "Armenian", "Հայերեն"},
	{"ne", "Nepali", "नेपाली"},
	{"mn", "Mongolian", "Монгол"},
	{"bs", "Bosnian", "Bosanski"},
	{"kk", "Kazakh", "Қазақша"},
	{"sq", "Albanian", "Shqip"},
	{"sw", "Swahili", "Kiswahili"},
	{"gl", "Galician", "Galego"},
	{"mr", "Marathi", "मराठी"},
	{"pa", "Punjabi", "ਪੰਜਾਬੀ"},
	{"si", "Sinhala", "සිංහල"},
	{"km", "Khmer", "ខ្មែរ"},
	{"sn", "Shona", "chiShona"},
	{"yo", "Yoruba", "Yorùbá"},
	{"so", "Somali", "Soomaali"},
	{"af", "Afrikaans", "Afrikaans"},
	{"oc", "Occitan", "Occitan"},
	{"ka", "Georgian", "ქართული"},
	{"be", "Belarusian", "Беларуская"},
	{"tg", "Tajik", "Тоҷикӣ"},
	{"sd", "Sindhi", "سنڌي"},
	{"gu", "Gujarati", "ગુજરાતી"},
	{"am", "Amharic", "አማርኛ"},
	{"yi", "Yiddish", "ייִדיש"},
	{"lo", "Lao", "ລາວ"},
	{"uz", "Uzbek", "Oʻzbek"},
	{"fo", "Faroese", "Føroyskt"},
	{"ht", "Haitian Creole", "Kreyòl ayisyen"},
	{"ps", "Pashto", "پښتو"},
	{"tk", "Turkmen", "Türkmen"},
	{"nn", "Nynorsk", "Nynorsk"},
	{"mt", "Maltese", "Malti"},
	{"sa", "Sanskrit", "संस्कृतम्"},
	{"lb", "Luxembourgish", "Lëtzebuergesch"},
	{"my", "Myanmar", "မြန်မာ"},
	{"bo", "Tibetan", "བོད་སྐད་"},
	{"tl", "Tagalog", "Tagalog"},
	{"mg", "Malagasy", "Malagasy"},
	{"as", "Assamese", "অসমীয়া"},
	{"tt", "Tatar", "Татарча"},
	{"haw", "Hawaiian", "ʻŌlelo Hawaiʻi"},
	{"ln", "Lingala", "Lingála"},
	{"ha", "Hausa", "Hausa"},
	{"ba", "Bashkir", "Башҡортса"},
	{"jw", "Javanese", "Basa Jawa"},
	{"su", "Sundanese", "Basa Sunda"},
	{"yue", "Cantonese", "粵語"},
}

func init() {
	// Auto-detect first, then English, then the rest alphabetically.
	rest := languages[2:]
	less := sortLess() // the user's language's collation (locale.go)
	sort.Slice(rest, func(i, j int) bool { return less(rest[i].name, rest[j].name) })
}

func languageName(code string) string {
	for _, l := range languages {
		if l.code == code {
			return l.name
		}
	}
	return code
}

func languageFor(code string) language {
	for _, l := range languages {
		if l.code == code {
			return l
		}
	}
	return language{code: code, name: code}
}

// languageLabel is the picker/menu text: "German — Deutsch (de)".
func languageLabel(l language) string {
	if l.code == autoLang {
		return l.name
	}
	name := l.name
	if l.native != "" && l.native != l.name {
		name += " — " + l.native
	}
	return name + "  (" + l.code + ")"
}

// langChip is the short text on the input chip: "Auto" or "DE".
func langChip(code string) string {
	if code == autoLang {
		return "Auto"
	}
	return strings.ToUpper(code)
}

// filterLanguages keeps the languages whose English name, native name or
// code matches q (case-insensitive); an empty q keeps all.
func filterLanguages(q string) []language {
	q = foldKey(strings.TrimSpace(q))
	var out []language
	for _, l := range languages {
		if q == "" || foldContains(l.name, q) ||
			(l.native != "" && foldContains(l.native, q)) || strings.HasPrefix(l.code, q) {
			out = append(out, l)
		}
	}
	return out
}
