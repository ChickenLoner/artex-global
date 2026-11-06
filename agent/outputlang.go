package agent

// User-facing output language (display language) selection. artex-global forces the agent to write the natural language shown to the user in one of
// five languages: English, Korean, Chinese, Spanish or Japanese. The agent's
// "brain" (the benchmarked Chinese reasoning body, section [A]) is left untouched regardless of language. What is chosen here
// is only the language of the surface the user reads.

// OutputLanguage is a hook the server wires to return the output language code
// ("en"/"ko"/"zh"/"es"/"ja"/"th") chosen from settings (config ARTEX_LANG, runtime settings KV). If it is nil or
// returns an empty value, the default below is used. The server injects it the same way as PromptOverride.
var OutputLanguage func() string

// Default output language when the server has not wired the hook (e.g. standalone tests of the agent package).
// The default is Korean (it is the fallback for standalone agent-package tests; in production the server wires the OutputLanguage hook
// with English, the default of the settings layer). English, Chinese, Spanish and Japanese are switched via settings.
const defaultOutputLang = "ko"

// SupportedOutputLangs is the list of output language codes that can be switched to.
var SupportedOutputLangs = []string{"en", "ko", "zh", "es", "ja", "th"}

// normalizeLang narrows a code outside the supported list to the default.
func normalizeLang(code string) string {
	for _, c := range SupportedOutputLangs {
		if c == code {
			return code
		}
	}
	return defaultOutputLang
}

// resolveOutputLang returns the output language code to apply now. It consults the hook the server wired
// first, and uses the default if there is none.
func resolveOutputLang() string {
	if OutputLanguage != nil {
		if code := OutputLanguage(); code != "" {
			return normalizeLang(code)
		}
	}
	return defaultOutputLang
}

// langPhrase is the notation used inside a (Chinese-written) instruction to say "write in this language".
// The native-language name is placed in parentheses so the model does not confuse which language is meant.
var langPhrase = map[string]string{
	"ko": "韩语（한국어）",
	"en": "英语（English）",
	"zh": "中文（简体中文）",
	"es": "西班牙语（Español）",
	"ja": "日语（日本語）",
	"th": "泰语（ภาษาไทย）",
}

// langBare is the short language name used when referring to a language repeatedly inside an instruction.
var langBare = map[string]string{
	"ko": "韩语",
	"en": "英语",
	"zh": "中文",
	"es": "西班牙语",
	"ja": "日语",
	"th": "泰语",
}
